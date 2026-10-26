// Package store persists the wrapper's identities, sessions and resource ownership.
// It never stores a Syncthing API credential or a raw browser session token.
package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

var (
	ErrNotFound             = errors.New("store: not found")
	ErrRegistrationDisabled = errors.New("store: registration disabled")
	ErrConflict             = errors.New("store: resource already registered")
	ErrForbidden            = errors.New("store: resource belongs to another user")
	ErrBlocked              = errors.New("store: user writes blocked pending operator repair")
)

type Store struct {
	db      *sql.DB
	changes sync.Mutex
}

// LockChanges serializes daemon/config snapshots with multi-step operations.
// Callers must release it after both the external change and ownership write.
func (s *Store) LockChanges() func() {
	s.changes.Lock()
	return s.changes.Unlock
}

type User struct {
	ID          string
	Issuer      string
	Subject     string
	Name        string
	Email       string
	StorageArea string
}

func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // foreign_keys and busy_timeout are connection-local; serialize writes.
	var schemaVersion int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&schemaVersion); err != nil || schemaVersion > 1 {
		db.Close()
		return nil, fmt.Errorf("unsupported store schema version %d: %v", schemaVersion, err)
	}
	for _, query := range []string{
		`PRAGMA foreign_keys = ON`,
		`PRAGMA busy_timeout = 5000`,
		`CREATE TABLE IF NOT EXISTS users (id TEXT PRIMARY KEY, issuer TEXT NOT NULL, subject TEXT NOT NULL, name TEXT NOT NULL, email TEXT NOT NULL, storage_area TEXT NOT NULL UNIQUE, UNIQUE(issuer, subject))`,
		`CREATE TABLE IF NOT EXISTS sessions (token_hash BLOB PRIMARY KEY, user_id TEXT NOT NULL REFERENCES users(id), expires_at INTEGER NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS folders (id TEXT PRIMARY KEY, user_id TEXT NOT NULL REFERENCES users(id), name TEXT NOT NULL, label TEXT NOT NULL, group_name TEXT NOT NULL DEFAULT '', UNIQUE(user_id, name))`,
		`CREATE TABLE IF NOT EXISTS devices (id TEXT PRIMARY KEY, user_id TEXT NOT NULL REFERENCES users(id), name TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS shares (folder_id TEXT NOT NULL REFERENCES folders(id) ON DELETE CASCADE, device_id TEXT NOT NULL REFERENCES devices(id) ON DELETE CASCADE, PRIMARY KEY(folder_id, device_id))`,
		`CREATE TABLE IF NOT EXISTS operations (id TEXT PRIMARY KEY, user_id TEXT NOT NULL UNIQUE REFERENCES users(id), kind TEXT NOT NULL, resource_id TEXT NOT NULL, state TEXT NOT NULL, reason TEXT NOT NULL DEFAULT '')`,
		`CREATE UNIQUE INDEX IF NOT EXISTS operations_resource ON operations(kind, resource_id)`,
		`CREATE TABLE IF NOT EXISTS blocks (user_id TEXT PRIMARY KEY REFERENCES users(id), reason TEXT NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS global_blocks (id INTEGER PRIMARY KEY CHECK(id=1), reason TEXT NOT NULL)`,
	} {
		if _, err = db.Exec(query); err != nil {
			db.Close()
			return nil, fmt.Errorf("initialize store: %w", err)
		}
	}
	if err = migrateFolders(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate folders: %w", err)
	}
	// A restarted process may never auto-complete a partial external write.
	if _, err = db.Exec(`UPDATE operations SET state='blocked',reason='interrupted operation; operator reconciliation required' WHERE state='pending'`); err != nil {
		db.Close()
		return nil, fmt.Errorf("block interrupted operations: %w", err)
	}
	return &Store{db: db}, nil
}

func migrateFolders(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var version int
	if err := tx.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		return err
	}
	if version > 1 {
		return fmt.Errorf("unsupported store schema version %d", version)
	}
	if version == 1 {
		return tx.Commit()
	}
	rows, err := tx.Query(`PRAGMA table_info(folders)`)
	if err != nil {
		return err
	}
	columns := make(map[string]bool)
	for rows.Next() {
		var number, required, primary int
		var name, kind string
		var defaultValue sql.NullString
		if err := rows.Scan(&number, &name, &kind, &required, &defaultValue, &primary); err != nil {
			rows.Close()
			return err
		}
		columns[name] = true
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	if !columns["label"] {
		if _, err := tx.Exec(`ALTER TABLE folders ADD COLUMN label TEXT NOT NULL DEFAULT ''`); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE folders SET label=name`); err != nil {
			return err
		}
	}
	if !columns["group_name"] {
		if _, err := tx.Exec(`ALTER TABLE folders ADD COLUMN group_name TEXT NOT NULL DEFAULT ''`); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`PRAGMA user_version=1`); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) Close() error { return s.db.Close() }

func randomID() (string, error) {
	var b [24]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", b[:]), nil
}

func scanUser(row interface{ Scan(...any) error }) (User, error) {
	var u User
	err := row.Scan(&u.ID, &u.Issuer, &u.Subject, &u.Name, &u.Email, &u.StorageArea)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	return u, err
}

const userColumns = `id, issuer, subject, name, email, storage_area`

func (s *Store) FindUser(ctx context.Context, issuer, subject string) (User, error) {
	return scanUser(s.db.QueryRowContext(ctx, `SELECT `+userColumns+` FROM users WHERE issuer=? AND subject=?`, issuer, subject))
}

func (s *Store) UpsertUser(ctx context.Context, issuer, subject, name, email string, allowRegistration bool) (User, error) {
	if issuer == "" || subject == "" {
		return User{}, fmt.Errorf("issuer and subject required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return User{}, err
	}
	defer tx.Rollback()
	u, err := scanUser(tx.QueryRowContext(ctx, `SELECT `+userColumns+` FROM users WHERE issuer=? AND subject=?`, issuer, subject))
	if err == nil {
		if _, err = tx.ExecContext(ctx, `UPDATE users SET name=?, email=? WHERE id=?`, name, email, u.ID); err != nil {
			return User{}, err
		}
		u.Name, u.Email = name, email
	} else if errors.Is(err, ErrNotFound) {
		if !allowRegistration {
			return User{}, ErrRegistrationDisabled
		}
		id, e := randomID()
		if e != nil {
			return User{}, e
		}
		area, e := randomID()
		if e != nil {
			return User{}, e
		}
		u = User{ID: id, Issuer: issuer, Subject: subject, Name: name, Email: email, StorageArea: area}
		_, err = tx.ExecContext(ctx, `INSERT INTO users(id, issuer, subject, name, email, storage_area) VALUES(?, ?, ?, ?, ?, ?)`, id, issuer, subject, name, email, area)
		if err != nil {
			return User{}, err
		}
	} else {
		return User{}, err
	}
	if err = tx.Commit(); err != nil {
		return User{}, err
	}
	return u, nil
}

// CreateSession accepts an already cryptographically random, unguessable browser token.
// Callers must not use OIDC identity claims or predictable values as tokens.
func (s *Store) CreateSession(ctx context.Context, token, userID string, expires time.Time) error {
	if token == "" || userID == "" {
		return fmt.Errorf("session token and user required")
	}
	hash := sha256.Sum256([]byte(token))
	_, err := s.db.ExecContext(ctx, `INSERT INTO sessions(token_hash, user_id, expires_at) VALUES(?, ?, ?)`, hash[:], userID, expires.UnixNano())
	return err
}

func (s *Store) SessionUser(ctx context.Context, token string) (User, error) {
	if token == "" {
		return User{}, ErrNotFound
	}
	hash := sha256.Sum256([]byte(token))
	u, err := scanUser(s.db.QueryRowContext(ctx, `SELECT `+userColumns+` FROM sessions JOIN users ON users.id=sessions.user_id WHERE token_hash=? AND expires_at>?`, hash[:], time.Now().UnixNano()))
	return u, err
}

func (s *Store) DeleteSession(ctx context.Context, token string) error {
	if token == "" {
		return nil
	}
	hash := sha256.Sum256([]byte(token))
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash=?`, hash[:])
	return err
}

// Folder and Device deliberately contain no full filesystem path or daemon credential.
// Subpath is the single directory name beneath the user's private storage area.
// The SQLite name column stores this legacy value without moving existing data.
type Folder struct{ ID, UserID, Label, Group, Subpath string }
type Device struct{ ID, UserID, Name string }

func scanFolder(row interface{ Scan(...any) error }) (Folder, error) {
	var f Folder
	err := row.Scan(&f.ID, &f.UserID, &f.Label, &f.Group, &f.Subpath)
	if errors.Is(err, sql.ErrNoRows) {
		return Folder{}, ErrNotFound
	}
	return f, err
}
func scanDevice(row interface{ Scan(...any) error }) (Device, error) {
	var d Device
	err := row.Scan(&d.ID, &d.UserID, &d.Name)
	if errors.Is(err, sql.ErrNoRows) {
		return Device{}, ErrNotFound
	}
	return d, err
}

// FolderOwner and DeviceOwner support reconciliation of daemon configuration.
// Do not expose them to untrusted browser requests; use Folder and Device for scoped reads.
const folderColumns = `id,user_id,label,group_name,name`

func (s *Store) FolderOwner(ctx context.Context, id string) (Folder, error) {
	return scanFolder(s.db.QueryRowContext(ctx, `SELECT `+folderColumns+` FROM folders WHERE id=?`, id))
}
func (s *Store) DeviceOwner(ctx context.Context, id string) (Device, error) {
	return scanDevice(s.db.QueryRowContext(ctx, `SELECT id,user_id,name FROM devices WHERE id=?`, id))
}
func (s *Store) Folder(ctx context.Context, userID, id string) (Folder, error) {
	f, err := s.FolderOwner(ctx, id)
	if err != nil {
		return Folder{}, err
	}
	if f.UserID != userID {
		return Folder{}, ErrForbidden
	}
	return f, nil
}
func (s *Store) Device(ctx context.Context, userID, id string) (Device, error) {
	d, err := s.DeviceOwner(ctx, id)
	if err != nil {
		return Device{}, err
	}
	if d.UserID != userID {
		return Device{}, ErrForbidden
	}
	return d, nil
}

func (s *Store) AllUsers(ctx context.Context) ([]User, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+userColumns+` FROM users ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []User{}
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, u)
	}
	return result, rows.Err()
}
func (s *Store) UserFolders(ctx context.Context, userID string) ([]Folder, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+folderColumns+` FROM folders WHERE user_id=? ORDER BY label,id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Folder{}
	for rows.Next() {
		f, err := scanFolder(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, f)
	}
	return result, rows.Err()
}
func (s *Store) UserDevices(ctx context.Context, userID string) ([]Device, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,user_id,name FROM devices WHERE user_id=? ORDER BY name,id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Device{}
	for rows.Next() {
		d, err := scanDevice(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, d)
	}
	return result, rows.Err()
}

// WriteStatus reports whether a user's writes are blocked, including interrupted
// operations surviving restart. Reason is for operator logs, not direct UI display.
func (s *Store) WriteStatus(ctx context.Context, userID string) (bool, string, error) {
	var reason string
	err := s.db.QueryRowContext(ctx, `SELECT reason FROM global_blocks WHERE id=1`).Scan(&reason)
	if err == nil {
		return true, reason, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return false, "", err
	}
	err = s.db.QueryRowContext(ctx, `SELECT reason FROM blocks WHERE user_id=?`, userID).Scan(&reason)
	if err == nil {
		return true, reason, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return false, "", err
	}
	var kind, resource, state string
	err = s.db.QueryRowContext(ctx, `SELECT kind,resource_id,state FROM operations WHERE user_id=?`, userID).Scan(&kind, &resource, &state)
	if err == nil {
		return true, fmt.Sprintf("%s %s (%s)", kind, resource, state), nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return false, "", nil
	}
	return false, "", err
}

func writeGuard(ctx context.Context, tx *sql.Tx, userID string, operationID []string) error {
	if len(operationID) > 1 {
		return fmt.Errorf("one operation ID allowed")
	}
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM global_blocks`).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return ErrBlocked
	}
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM blocks WHERE user_id=?`, userID).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return ErrBlocked
	}
	var id, state string
	err := tx.QueryRowContext(ctx, `SELECT id,state FROM operations WHERE user_id=?`, userID).Scan(&id, &state)
	if errors.Is(err, sql.ErrNoRows) {
		if len(operationID) != 0 {
			return ErrBlocked
		}
		return nil
	}
	if err != nil {
		return err
	}
	if state != "pending" || len(operationID) != 1 || id != operationID[0] {
		return ErrBlocked
	}
	return nil
}
func (s *Store) write(ctx context.Context, userID string, operationID []string, fn func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = writeGuard(ctx, tx, userID, operationID); err != nil {
		return err
	}
	if err = fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

func conflict(err error) error {
	if err == nil {
		return nil
	}
	// SQLite constraint codes are surfaced by the driver as errors with Code().
	type coded interface{ Code() int }
	var c coded
	if errors.As(err, &c) && c.Code()&0xff == 19 {
		return ErrConflict
	}
	return err
}

// Pass an operation ID to writes performed within BeginOperation/CompleteOperation.
// Without it, no write is allowed while an operation is pending or a user blocked.
func (s *Store) CreateFolder(ctx context.Context, userID, id, name string, operationID ...string) error {
	return s.CreateFolderDetails(ctx, Folder{ID: id, UserID: userID, Label: name, Subpath: name}, operationID...)
}
func (s *Store) CreateFolderDetails(ctx context.Context, f Folder, operationID ...string) error {
	if f.ID == "" || f.Label == "" || f.Subpath == "" || f.UserID == "" {
		return fmt.Errorf("folder ID, label, subpath and user required")
	}
	return s.write(ctx, f.UserID, operationID, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO folders(id,user_id,name,label,group_name) VALUES(?,?,?,?,?)`, f.ID, f.UserID, f.Subpath, f.Label, f.Group)
		return conflict(err)
	})
}
func (s *Store) CreateDevice(ctx context.Context, userID, id, name string, operationID ...string) error {
	if id == "" || name == "" || userID == "" {
		return fmt.Errorf("device ID, name and user required")
	}
	return s.write(ctx, userID, operationID, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO devices(id,user_id,name) VALUES(?,?,?)`, id, userID, name)
		return conflict(err)
	})
}
func ownedFolder(ctx context.Context, tx *sql.Tx, userID, id string) error {
	var owner string
	err := tx.QueryRowContext(ctx, `SELECT user_id FROM folders WHERE id=?`, id).Scan(&owner)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if owner != userID {
		return ErrForbidden
	}
	return nil
}
func ownedDevice(ctx context.Context, tx *sql.Tx, userID, id string) error {
	var owner string
	err := tx.QueryRowContext(ctx, `SELECT user_id FROM devices WHERE id=?`, id).Scan(&owner)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if owner != userID {
		return ErrForbidden
	}
	return nil
}
func (s *Store) DeleteFolder(ctx context.Context, userID, id string, operationID ...string) error {
	return s.write(ctx, userID, operationID, func(tx *sql.Tx) error {
		if err := ownedFolder(ctx, tx, userID, id); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM folders WHERE id=?`, id)
		return err
	})
}
func (s *Store) DeleteDevice(ctx context.Context, userID, id string, operationID ...string) error {
	return s.write(ctx, userID, operationID, func(tx *sql.Tx) error {
		if err := ownedDevice(ctx, tx, userID, id); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM devices WHERE id=?`, id)
		return err
	})
}
func (s *Store) SetShare(ctx context.Context, userID, folderID, deviceID string, enabled bool, operationID ...string) error {
	return s.write(ctx, userID, operationID, func(tx *sql.Tx) error {
		if err := ownedFolder(ctx, tx, userID, folderID); err != nil {
			return err
		}
		if err := ownedDevice(ctx, tx, userID, deviceID); err != nil {
			return err
		}
		if enabled {
			_, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO shares(folder_id,device_id) VALUES(?,?)`, folderID, deviceID)
			return err
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM shares WHERE folder_id=? AND device_id=?`, folderID, deviceID)
		return err
	})
}
func (s *Store) FolderShares(ctx context.Context, userID, folderID string) ([]Device, error) {
	if _, err := s.Folder(ctx, userID, folderID); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT d.id,d.user_id,d.name FROM devices d JOIN shares s ON s.device_id=d.id WHERE s.folder_id=? AND d.user_id=? ORDER BY d.name,d.id`, folderID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Device{}
	for rows.Next() {
		d, err := scanDevice(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, d)
	}
	return result, rows.Err()
}
func (s *Store) DeviceShares(ctx context.Context, userID, deviceID string) ([]Folder, error) {
	if _, err := s.Device(ctx, userID, deviceID); err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, `SELECT f.id,f.user_id,f.label,f.group_name,f.name FROM folders f JOIN shares s ON s.folder_id=f.id WHERE s.device_id=? AND f.user_id=? ORDER BY f.label,f.id`, deviceID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Folder{}
	for rows.Next() {
		f, err := scanFolder(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, f)
	}
	return result, rows.Err()
}

// BeginOperation durably records an external write before its first side effect.
// On restart a pending operation remains blocked until explicitly reconciled.
func (s *Store) BeginOperation(ctx context.Context, userID, kind, resourceID string) (string, error) {
	if userID == "" || kind == "" || resourceID == "" {
		return "", fmt.Errorf("operation user, kind and resource required")
	}
	id, err := randomID()
	if err != nil {
		return "", err
	}
	err = s.write(ctx, userID, nil, func(tx *sql.Tx) error {
		if kind == "create-device" || kind == "create-folder" {
			table := "devices"
			if kind == "create-folder" {
				table = "folders"
			}
			var count int
			if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM `+table+` WHERE id=?`, resourceID).Scan(&count); err != nil {
				return err
			}
			if count > 0 {
				return ErrConflict
			}
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO operations(id,user_id,kind,resource_id,state) VALUES(?,?,?,?,'pending')`, id, userID, kind, resourceID)
		return conflict(err)
	})
	return id, err
}
func (s *Store) CompleteOperation(ctx context.Context, userID, operationID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var state string
	err = tx.QueryRowContext(ctx, `SELECT state FROM operations WHERE user_id=? AND id=?`, userID, operationID).Scan(&state)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if state != "pending" {
		return ErrBlocked
	}
	var count int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM blocks WHERE user_id=?`, userID).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return ErrBlocked
	}
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM global_blocks`).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return ErrBlocked
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM operations WHERE id=?`, operationID); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *Store) FailOperation(ctx context.Context, userID, operationID, reason string) error {
	if reason == "" {
		return fmt.Errorf("failure reason required")
	}
	res, err := s.db.ExecContext(ctx, `UPDATE operations SET state='blocked',reason=? WHERE id=? AND user_id=? AND state='pending'`, reason, operationID, userID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// BlockUser records drift or an unattributable partial write. Only the operator
// should clear it after verifying external daemon configuration and filesystem.
func (s *Store) BlockUser(ctx context.Context, userID, reason string) error {
	if userID == "" || reason == "" {
		return fmt.Errorf("user and reason required")
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO blocks(user_id,reason) VALUES(?,?) ON CONFLICT(user_id) DO UPDATE SET reason=excluded.reason`, userID, reason)
	return err
}

// ResolveUser is an operator-only reconciliation hook, never an HTTP user action.
func (s *Store) ResolveUser(ctx context.Context, userID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `DELETE FROM operations WHERE user_id=?`, userID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM blocks WHERE user_id=?`, userID); err != nil {
		return err
	}
	return tx.Commit()
}

// BlockAllWrites records unattributable drift, even before anyone has signed in.
// The block persists and applies to accounts registered after it was observed.
func (s *Store) BlockAllWrites(ctx context.Context, reason string) error {
	if reason == "" {
		return fmt.Errorf("global block reason required")
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO global_blocks(id,reason) VALUES(1,?) ON CONFLICT(id) DO UPDATE SET reason=excluded.reason`, reason)
	return err
}

// ResolveGlobal is an operator-only recovery hook after external reconciliation.
func (s *Store) ResolveGlobal(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM global_blocks WHERE id=1`)
	return err
}

// OperationOwner identifies an in-progress or interrupted operation that
// reserves a daemon resource before its ownership record is committed.
func (s *Store) OperationOwner(ctx context.Context, kind, resourceID string) (string, error) {
	var userID string
	err := s.db.QueryRowContext(ctx, `SELECT user_id FROM operations WHERE kind=? AND resource_id=?`, kind, resourceID).Scan(&userID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return userID, err
}
