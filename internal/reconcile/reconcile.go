package reconcile

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"time"

	"syncthing-server/internal/storage"
	"syncthing-server/internal/store"
	"syncthing-server/internal/syncthing"
)

var ErrDrift = errors.New("configuration or storage drift detected")

type Checker struct {
	users  *store.Store
	daemon *syncthing.Client
	files  *storage.Storage
	log    *slog.Logger
}

func New(users *store.Store, daemon *syncthing.Client, files *storage.Storage, log *slog.Logger) *Checker {
	return &Checker{users: users, daemon: daemon, files: files, log: log}
}

// Check compares owned resources against the daemon and each user's top-level
// directories. Files inside a registered folder are intentionally not inspected.
func (c *Checker) Check(ctx context.Context) error {
	unlock := c.users.LockChanges()
	defer unlock()
	users, err := c.users.AllUsers(ctx)
	if err != nil {
		return err
	}
	daemonFolders, err := c.daemon.Folders(ctx)
	if err != nil {
		return err
	}
	daemonDevices, err := c.daemon.Devices(ctx)
	if err != nil {
		return err
	}
	serverID, err := c.daemon.MyID(ctx)
	if err != nil {
		return err
	}
	folderByID := make(map[string]syncthing.Folder, len(daemonFolders))
	for _, folder := range daemonFolders {
		folderByID[folder.ID] = folder
	}
	deviceByID := make(map[string]syncthing.Device, len(daemonDevices))
	for _, device := range daemonDevices {
		deviceByID[device.DeviceID] = device
	}
	var drift bool
	mark := func(userID, reason string) error {
		drift = true
		c.log.Error("configuration or storage drift", "userID", userID, "reason", reason)
		return c.users.BlockUser(ctx, userID, reason)
	}
	markAll := func(reason string) error {
		drift = true
		c.log.Error("unowned daemon configuration", "reason", reason)
		return c.users.BlockAllWrites(ctx, reason)
	}
	for _, folder := range daemonFolders {
		if _, err := c.users.FolderOwner(ctx, folder.ID); errors.Is(err, store.ErrNotFound) {
			if _, err = c.users.OperationOwner(ctx, "create-folder", folder.ID); err == nil {
				continue
			} else if !errors.Is(err, store.ErrNotFound) {
				return err
			}
			if _, err = c.users.FolderOwner(ctx, folder.ID); err == nil {
				continue
			} else if !errors.Is(err, store.ErrNotFound) {
				return err
			}
			if err := markAll("unowned Syncthing folder"); err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
	}
	for _, device := range daemonDevices {
		if device.DeviceID == serverID {
			continue
		}
		if _, err := c.users.DeviceOwner(ctx, device.DeviceID); errors.Is(err, store.ErrNotFound) {
			if _, err = c.users.OperationOwner(ctx, "create-device", device.DeviceID); err == nil {
				continue
			} else if !errors.Is(err, store.ErrNotFound) {
				return err
			}
			if _, err = c.users.DeviceOwner(ctx, device.DeviceID); err == nil {
				continue
			} else if !errors.Is(err, store.ErrNotFound) {
				return err
			}
			if err := markAll("unowned Syncthing peer device"); err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
	}
	for _, user := range users {
		// A pending write may be between daemon and filesystem steps. It remains
		// blocked for other requests, but does not itself trigger drift detection.
		blocked, _, err := c.users.WriteStatus(ctx, user.ID)
		if err != nil {
			return err
		}
		if blocked {
			continue
		}
		folders, err := c.users.UserFolders(ctx, user.ID)
		if err != nil {
			return err
		}
		devices, err := c.users.UserDevices(ctx, user.ID)
		if err != nil {
			return err
		}
		registered := make(map[string]bool, len(folders))
		for _, folder := range folders {
			registered[folder.Subpath] = true
			expected, err := c.files.Path(user.StorageArea, folder.Subpath)
			if err != nil {
				return err
			}
			actual, ok := folderByID[folder.ID]
			if !ok || filepath.Clean(actual.Path) != filepath.Clean(expected) || actual.Type != "sendreceive" || actual.Label != folder.Label || actual.Group != folder.Group {
				if err := mark(user.ID, "registered Syncthing folder missing or configuration changed"); err != nil {
					return err
				}
				continue
			}
			exists, err := c.files.Exists(user.StorageArea, folder.Subpath)
			if err != nil || !exists {
				if err := mark(user.ID, "registered folder directory missing or invalid"); err != nil {
					return err
				}
			}
			expectedShares, err := c.users.FolderShares(ctx, user.ID, folder.ID)
			if err != nil {
				return err
			}
			expectedDevice := make(map[string]bool, len(expectedShares))
			for _, share := range expectedShares {
				expectedDevice[share.ID] = true
			}
			seen := make(map[string]bool, len(actual.Devices))
			for _, share := range actual.Devices {
				if share.DeviceID == serverID {
					continue
				}
				seen[share.DeviceID] = true
				if !expectedDevice[share.DeviceID] {
					if err := mark(user.ID, "folder sharing changed outside wrapper"); err != nil {
						return err
					}
				}
			}
			for id := range expectedDevice {
				if !seen[id] {
					if err := mark(user.ID, "registered folder share missing"); err != nil {
						return err
					}
				}
			}
		}
		names, err := c.files.List(user.StorageArea)
		if err != nil {
			if err := mark(user.ID, "user storage area invalid"); err != nil {
				return err
			}
			continue
		}
		for _, name := range names {
			if !registered[name] {
				if err := mark(user.ID, "unexpected directory in user storage area"); err != nil {
					return err
				}
			}
		}
		for _, device := range devices {
			remote, ok := deviceByID[device.ID]
			if !ok || remote.Name != device.Name {
				if err := mark(user.ID, "registered peer device missing or changed in Syncthing"); err != nil {
					return err
				}
			}
		}
	}
	if drift {
		return ErrDrift
	}
	return nil
}

func (c *Checker) Run(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if err := c.Check(ctx); err != nil && ctx.Err() == nil {
			c.log.Error("reconciliation check failed", "error", fmt.Sprintf("%v", err))
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
