package pending

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"syncthing-server/internal/store"
	"syncthing-server/internal/syncthing"
)

type Worker struct {
	daemon *syncthing.Client
	users  *store.Store
	log    *slog.Logger
}

func New(daemon *syncthing.Client, users *store.Store, log *slog.Logger) *Worker {
	return &Worker{daemon: daemon, users: users, log: log}
}

// Scan refetches authoritative pending state, so missed daemon events do not
// leave an unknown device or folder offer trusted indefinitely.
func (w *Worker) Scan(ctx context.Context) error {
	unlock := w.users.LockChanges()
	defer unlock()
	devices, err := w.daemon.PendingDevices(ctx)
	if err != nil {
		return fmt.Errorf("read pending devices: %w", err)
	}
	for id := range devices {
		_, err := w.users.DeviceOwner(ctx, id)
		if err == nil {
			continue
		}
		if !errors.Is(err, store.ErrNotFound) {
			return err
		}
		if err := w.daemon.DismissPendingDevice(ctx, id); err != nil {
			return fmt.Errorf("dismiss pending device: %w", err)
		}
		w.log.Warn("dismissed unknown peer device request", "deviceID", id)
	}
	offers, err := w.daemon.PendingFolders(ctx)
	if err != nil {
		return fmt.Errorf("read pending folder offers: %w", err)
	}
	for folderID, raw := range offers {
		var offer struct {
			OfferedBy map[string]struct {
				Label string `json:"label"`
			} `json:"offeredBy"`
		}
		if err := json.Unmarshal(raw, &offer); err != nil {
			return fmt.Errorf("decode pending folder offer: %w", err)
		}
		for deviceID, detail := range offer.OfferedBy {
			device, err := w.users.DeviceOwner(ctx, deviceID)
			if errors.Is(err, store.ErrNotFound) {
				if err := w.daemon.DismissPendingFolder(ctx, folderID, deviceID); err != nil {
					return err
				}
				w.log.Warn("dismissed folder offer from unknown peer", "deviceID", deviceID, "folderID", folderID)
				continue
			}
			if err != nil {
				return err
			}
			folder, err := w.users.FolderOwner(ctx, folderID)
			if err != nil && !errors.Is(err, store.ErrNotFound) {
				return err
			}
			if err == nil && folder.UserID == device.UserID {
				shares, err := w.users.FolderShares(ctx, folder.UserID, folderID)
				if err != nil {
					return err
				}
				legitimate := false
				for _, share := range shares {
					if share.ID == deviceID {
						legitimate = true
						break
					}
				}
				if legitimate {
					continue
				}
			}
			if err := w.daemon.IgnorePendingFolder(ctx, folderID, deviceID, detail.Label); err != nil {
				return fmt.Errorf("ignore unsolicited folder offer: %w", err)
			}
			w.log.Warn("ignored unsolicited folder offer", "deviceID", deviceID, "folderID", folderID)
		}
	}
	return nil
}

func (w *Worker) Run(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if err := w.Scan(ctx); err != nil && ctx.Err() == nil {
			w.log.Error("pending request scan failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
