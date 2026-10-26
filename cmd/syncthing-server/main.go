package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"syncthing-server/internal/config"
	"syncthing-server/internal/oidc"
	"syncthing-server/internal/pending"
	"syncthing-server/internal/reconcile"
	"syncthing-server/internal/storage"
	"syncthing-server/internal/store"
	"syncthing-server/internal/syncthing"
	"syncthing-server/internal/web"
)

var syncthingVersion = regexp.MustCompile(`^v([0-9]+)\.([0-9]+)\.([0-9]+)$`)

func checkSyncthingVersion(version string, major int) error {
	parts := syncthingVersion.FindStringSubmatch(version)
	if parts == nil {
		return fmt.Errorf("unsupported Syncthing version %q (expected stable v%d.1.0 or newer)", version, major)
	}
	got, err := strconv.Atoi(parts[1])
	if err != nil || got != major {
		return fmt.Errorf("unsupported Syncthing major version %q (expected v%d)", version, major)
	}
	minor, err := strconv.Atoi(parts[2])
	if err != nil || (got == 2 && minor < 1) {
		return fmt.Errorf("Syncthing v2.1.0 or newer is required for folder groups: %q", version)
	}
	return nil
}

func run(ctx context.Context, path string, log *slog.Logger) error {
	cfg, err := config.Load(path)
	if err != nil {
		return err
	}
	daemon, err := syncthing.NewClient(cfg.SyncthingURL, cfg.SyncthingAPIKey, nil)
	if err != nil {
		return err
	}
	version, err := daemon.Version(ctx)
	if err != nil {
		return fmt.Errorf("check Syncthing version: %w", err)
	}
	if err := checkSyncthingVersion(version.Version, cfg.SyncthingMajor); err != nil {
		return err
	}
	provider, err := oidc.New(ctx, cfg.OIDCIssuer, cfg.OIDCClientID, cfg.OIDCClientSecret, strings.TrimRight(cfg.BaseURL, "/")+"/callback")
	if err != nil {
		return err
	}
	users, err := store.Open(cfg.DatabasePath)
	if err != nil {
		return err
	}
	defer users.Close()
	files, err := storage.Open(cfg.StorageRoot)
	if err != nil {
		return err
	}
	defer files.Close()
	checker := reconcile.New(users, daemon, files, log)
	if err := checker.Check(ctx); err != nil {
		if !errors.Is(err, reconcile.ErrDrift) {
			return fmt.Errorf("check storage and configuration: %w", err)
		}
		known, err := users.AllUsers(ctx)
		if err != nil {
			return err
		}
		if len(known) == 0 {
			return errors.New("unowned daemon configuration before any user registration")
		}
		log.Error("startup detected drift; affected users' writes blocked")
	}
	workerCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go checker.Run(workerCtx, 30*time.Second)
	go pending.New(daemon, users, log).Run(workerCtx, 15*time.Second)
	server := &http.Server{
		Addr:              cfg.Listen,
		Handler:           web.New(cfg, users, daemon, provider, files),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      90 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	done := make(chan error, 1)
	go func() { done <- server.ListenAndServe() }()
	log.Info("wrapper listening", "address", cfg.Listen)
	select {
	case <-ctx.Done():
		stop, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer stopCancel()
		return server.Shutdown(stop)
	case err := <-done:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func main() {
	path := flag.String("config", os.Getenv("APP_CONFIG"), "path to non-secret JSON application configuration")
	flag.Parse()
	log := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	if *path == "" {
		log.Error("configuration path required", "flag", "-config")
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, *path, log); err != nil {
		log.Error("wrapper stopped", "error", err)
		os.Exit(1)
	}
}
