package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"strings"
)

type Admin struct {
	Issuer  string `json:"issuer"`
	Subject string `json:"subject"`
}

type Config struct {
	Listen              string
	BaseURL             string
	OIDCIssuer          string
	OIDCClientID        string
	OIDCClientSecret    string
	SyncthingURL        string
	SyncthingMajor      int
	SyncthingAPIKey     string
	StorageRoot         string
	DatabasePath        string
	RegistrationEnabled bool
	Admins              []Admin
}

func Load(path string) (Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return Config{}, fmt.Errorf("open application configuration: %w", err)
	}
	defer f.Close()
	var raw struct {
		Listen              string  `json:"listen"`
		BaseURL             string  `json:"baseURL"`
		OIDCIssuer          string  `json:"oidcIssuer"`
		OIDCClientID        string  `json:"oidcClientID"`
		SyncthingURL        string  `json:"syncthingURL"`
		SyncthingMajor      int     `json:"syncthingMajor"`
		StorageRoot         string  `json:"storageRoot"`
		DatabasePath        string  `json:"databasePath"`
		RegistrationEnabled *bool   `json:"registrationEnabled"`
		Admins              []Admin `json:"admins"`
	}
	decoder := json.NewDecoder(f)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&raw); err != nil {
		return Config{}, fmt.Errorf("decode application configuration: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return Config{}, errors.New("unexpected content after application configuration")
	}
	cfg := Config{
		Listen: raw.Listen, BaseURL: raw.BaseURL, OIDCIssuer: raw.OIDCIssuer,
		OIDCClientID: raw.OIDCClientID, OIDCClientSecret: os.Getenv("APP_OIDC_CLIENT_SECRET"),
		SyncthingURL: raw.SyncthingURL, SyncthingMajor: raw.SyncthingMajor, SyncthingAPIKey: os.Getenv("APP_SYNCTHING_API_KEY"),
		StorageRoot: raw.StorageRoot, DatabasePath: raw.DatabasePath,
		RegistrationEnabled: raw.RegistrationEnabled == nil || *raw.RegistrationEnabled, Admins: raw.Admins,
	}
	if cfg.Listen == "" {
		cfg.Listen = ":8080"
	}
	if cfg.SyncthingMajor == 0 {
		cfg.SyncthingMajor = 2
	}
	if cfg.SyncthingMajor < 0 {
		return Config{}, errors.New("syncthingMajor must be positive")
	}
	if cfg.BaseURL == "" || cfg.OIDCIssuer == "" || cfg.OIDCClientID == "" || cfg.SyncthingURL == "" || cfg.StorageRoot == "" || cfg.DatabasePath == "" || cfg.OIDCClientSecret == "" || cfg.SyncthingAPIKey == "" {
		return Config{}, errors.New("baseURL, oidcIssuer, oidcClientID, syncthingURL, storageRoot, databasePath, and environment secrets are required")
	}
	for _, s := range []string{cfg.BaseURL, cfg.OIDCIssuer, cfg.SyncthingURL} {
		u, err := url.Parse(s)
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return Config{}, fmt.Errorf("invalid service URL: %q", s)
		}
	}
	daemonURL, _ := url.Parse(cfg.SyncthingURL)
	host := daemonURL.Hostname()
	ip := net.ParseIP(host)
	if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return Config{}, errors.New("syncthingURL must bind to a loopback host")
	}
	if !strings.HasPrefix(cfg.StorageRoot, "/") || !strings.HasPrefix(cfg.DatabasePath, "/") {
		return Config{}, errors.New("storageRoot and databasePath must be absolute")
	}
	for _, admin := range cfg.Admins {
		if admin.Issuer == "" || admin.Subject == "" {
			return Config{}, errors.New("admin issuer and subject must be set")
		}
	}
	return cfg, nil
}
