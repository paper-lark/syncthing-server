// Package syncthing provides a narrowly scoped, server-side Syncthing REST client.
// It does not perform ownership checks; callers must authorize every operation.
package syncthing

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Client holds the daemon credential; never pass it to a browser.
type Client struct {
	base *url.URL
	key  string
	http *http.Client
}

// NewClient accepts a base URL, optionally with a GUI path prefix, and an API key.
// A nil httpClient uses a bounded default HTTP client.
func NewClient(baseURL, apiKey string, httpClient *http.Client) (*Client, error) {
	u, err := url.Parse(baseURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || apiKey == "" {
		return nil, errors.New("invalid Syncthing endpoint or API key")
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 75 * time.Second}
	}
	// X-API-Key is not treated as a sensitive header by net/http redirect
	// handling. Never forward it to a redirected URL, even on the same host.
	client := *httpClient
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{base: u, key: apiKey, http: &client}, nil
}

// HTTPError reports a daemon error without returning arbitrary daemon response text,
// which can contain secrets or untrusted data.
type HTTPError struct {
	StatusCode   int
	Method, Path string
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("syncthing %s %s: HTTP %d", e.Method, e.Path, e.StatusCode)
}

const maxResponseBytes = 8 << 20

func (c *Client) do(ctx context.Context, method, endpoint string, query url.Values, body any, out any) error {
	u := *c.base
	prefix := strings.TrimRight(u.EscapedPath(), "/")
	u.RawPath = prefix + endpoint
	// Path must be decoded while RawPath retains escaped identifiers.
	path, err := url.PathUnescape(u.RawPath)
	if err != nil {
		return err
	}
	u.Path = path
	u.RawQuery = query.Encode()
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), reader)
	if err != nil {
		return err
	}
	req.Header.Set("X-API-Key", c.key)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return &HTTPError{StatusCode: resp.StatusCode, Method: method, Path: endpoint}
	}
	if out == nil {
		return nil
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return err
	}
	if len(data) > maxResponseBytes {
		return errors.New("syncthing response too large")
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("decode syncthing response: %w", err)
	}
	return nil
}

func configPath(kind, id string) string { return "/rest/config/" + kind + "/" + url.PathEscape(id) }

// Version and Status return daemon identity information; MyID extracts its device ID.
type VersionInfo struct {
	Version     string `json:"version"`
	LongVersion string `json:"longVersion"`
}
type SystemStatus struct {
	MyID   string `json:"myID"`
	Uptime int64  `json:"uptime"`
}

func (c *Client) Version(ctx context.Context) (VersionInfo, error) {
	var v VersionInfo
	err := c.do(ctx, "GET", "/rest/system/version", nil, nil, &v)
	return v, err
}
func (c *Client) Status(ctx context.Context) (SystemStatus, error) {
	var v SystemStatus
	err := c.do(ctx, "GET", "/rest/system/status", nil, nil, &v)
	return v, err
}
func (c *Client) MyID(ctx context.Context) (string, error) {
	v, err := c.Status(ctx)
	return v.MyID, err
}

// Config records keep unknown fields on read-modify-write. Syncthing adds settings
// over time; discarding them during a share or rename update would be destructive.
type Folder struct {
	ID      string         `json:"id"`
	Label   string         `json:"label"`
	Group   string         `json:"group"`
	Path    string         `json:"path"`
	Type    string         `json:"type"`
	Devices []FolderDevice `json:"devices"`
	raw     map[string]json.RawMessage
}
type FolderDevice struct {
	DeviceID string `json:"deviceID"`
	raw      map[string]json.RawMessage
}
type Device struct {
	DeviceID       string          `json:"deviceID"`
	Name           string          `json:"name"`
	Addresses      []string        `json:"addresses"`
	IgnoredFolders []IgnoredFolder `json:"ignoredFolders"`
	raw            map[string]json.RawMessage
}
type IgnoredFolder struct {
	ID    string    `json:"id"`
	Label string    `json:"label"`
	Time  time.Time `json:"time"`
	raw   map[string]json.RawMessage
}

func merge(raw map[string]json.RawMessage, fields map[string]any) ([]byte, error) {
	m := make(map[string]json.RawMessage, len(raw)+len(fields))
	for k, v := range raw {
		m[k] = v
	}
	for k, v := range fields {
		// A nil optional slice on a new record means "use daemon default".
		if v == nil && raw[k] == nil {
			continue
		}
		b, err := json.Marshal(v)
		if err != nil {
			return nil, err
		}
		m[k] = b
	}
	return json.Marshal(m)
}
func (f *Folder) UnmarshalJSON(b []byte) error {
	type plain Folder
	var p plain
	if err := json.Unmarshal(b, &p); err != nil {
		return err
	}
	if err := json.Unmarshal(b, &p.raw); err != nil {
		return err
	}
	*f = Folder(p)
	return nil
}
func (f Folder) MarshalJSON() ([]byte, error) {
	return merge(f.raw, map[string]any{"id": f.ID, "label": f.Label, "group": f.Group, "path": f.Path, "type": f.Type, "devices": f.Devices})
}
func (f *FolderDevice) UnmarshalJSON(b []byte) error {
	type plain FolderDevice
	var p plain
	if err := json.Unmarshal(b, &p); err != nil {
		return err
	}
	if err := json.Unmarshal(b, &p.raw); err != nil {
		return err
	}
	*f = FolderDevice(p)
	return nil
}
func (f FolderDevice) MarshalJSON() ([]byte, error) {
	return merge(f.raw, map[string]any{"deviceID": f.DeviceID})
}
func (d *Device) UnmarshalJSON(b []byte) error {
	type plain Device
	var p plain
	if err := json.Unmarshal(b, &p); err != nil {
		return err
	}
	if err := json.Unmarshal(b, &p.raw); err != nil {
		return err
	}
	*d = Device(p)
	return nil
}
func (d Device) MarshalJSON() ([]byte, error) {
	return merge(d.raw, map[string]any{"deviceID": d.DeviceID, "name": d.Name, "addresses": d.Addresses, "ignoredFolders": d.IgnoredFolders})
}
func (f *IgnoredFolder) UnmarshalJSON(b []byte) error {
	type plain IgnoredFolder
	var p plain
	if err := json.Unmarshal(b, &p); err != nil {
		return err
	}
	if err := json.Unmarshal(b, &p.raw); err != nil {
		return err
	}
	*f = IgnoredFolder(p)
	return nil
}
func (f IgnoredFolder) MarshalJSON() ([]byte, error) {
	return merge(f.raw, map[string]any{"id": f.ID, "label": f.Label, "time": f.Time})
}

func (c *Client) Folders(ctx context.Context) ([]Folder, error) {
	var result []Folder
	err := c.do(ctx, "GET", "/rest/config/folders", nil, nil, &result)
	return result, err
}
func (c *Client) Folder(ctx context.Context, id string) (Folder, error) {
	var result Folder
	err := c.do(ctx, "GET", configPath("folders", id), nil, nil, &result)
	return result, err
}
func (c *Client) CreateFolder(ctx context.Context, f Folder) error {
	return c.do(ctx, "POST", "/rest/config/folders", nil, f, nil)
}
func (c *Client) UpdateFolder(ctx context.Context, f Folder) error {
	if f.ID == "" {
		return errors.New("folder ID required")
	}
	return c.do(ctx, "PUT", configPath("folders", f.ID), nil, f, nil)
}
func (c *Client) DeleteFolder(ctx context.Context, id string) error {
	if id == "" {
		return errors.New("folder ID required")
	}
	return c.do(ctx, "DELETE", configPath("folders", id), nil, nil, nil)
}
func (c *Client) Devices(ctx context.Context) ([]Device, error) {
	var result []Device
	err := c.do(ctx, "GET", "/rest/config/devices", nil, nil, &result)
	return result, err
}
func (c *Client) Device(ctx context.Context, id string) (Device, error) {
	var result Device
	err := c.do(ctx, "GET", configPath("devices", id), nil, nil, &result)
	return result, err
}
func (c *Client) CreateDevice(ctx context.Context, d Device) error {
	return c.do(ctx, "POST", "/rest/config/devices", nil, d, nil)
}
func (c *Client) UpdateDevice(ctx context.Context, d Device) error {
	if d.DeviceID == "" {
		return errors.New("device ID required")
	}
	return c.do(ctx, "PUT", configPath("devices", d.DeviceID), nil, d, nil)
}
func (c *Client) DeleteDevice(ctx context.Context, id string) error {
	if id == "" {
		return errors.New("device ID required")
	}
	return c.do(ctx, "DELETE", configPath("devices", id), nil, nil, nil)
}

// Status and stats are returned as raw JSON so callers can filter by ownership
// before rendering any data, while retaining daemon-version-specific fields.
func (c *Client) FolderStatus(ctx context.Context, id string) (json.RawMessage, error) {
	var result json.RawMessage
	err := c.do(ctx, "GET", "/rest/db/status", url.Values{"folder": {id}}, nil, &result)
	return result, err
}
func (c *Client) FolderStats(ctx context.Context) (map[string]json.RawMessage, error) {
	var result map[string]json.RawMessage
	err := c.do(ctx, "GET", "/rest/stats/folder", nil, nil, &result)
	return result, err
}
func (c *Client) DeviceStats(ctx context.Context) (map[string]json.RawMessage, error) {
	var result map[string]json.RawMessage
	err := c.do(ctx, "GET", "/rest/stats/device", nil, nil, &result)
	return result, err
}

// Connections includes daemon-wide aggregates; callers must filter before display.
func (c *Client) Connections(ctx context.Context) (json.RawMessage, error) {
	var result json.RawMessage
	err := c.do(ctx, "GET", "/rest/system/connections", nil, nil, &result)
	return result, err
}
func (c *Client) Completion(ctx context.Context, folderID, deviceID string) (json.RawMessage, error) {
	var result json.RawMessage
	query := url.Values{"folder": {folderID}}
	if deviceID != "" {
		query.Set("device", deviceID)
	}
	err := c.do(ctx, "GET", "/rest/db/completion", query, nil, &result)
	return result, err
}

// Pending requests are global daemon data. Never return these directly to a user.
func (c *Client) PendingDevices(ctx context.Context) (map[string]json.RawMessage, error) {
	var result map[string]json.RawMessage
	err := c.do(ctx, "GET", "/rest/cluster/pending/devices", nil, nil, &result)
	return result, err
}
func (c *Client) PendingFolders(ctx context.Context) (map[string]json.RawMessage, error) {
	var result map[string]json.RawMessage
	err := c.do(ctx, "GET", "/rest/cluster/pending/folders", nil, nil, &result)
	return result, err
}
func (c *Client) DismissPendingDevice(ctx context.Context, id string) error {
	if id == "" {
		return errors.New("device ID required")
	}
	return c.do(ctx, "DELETE", "/rest/cluster/pending/devices", url.Values{"device": {id}}, nil, nil)
}
func (c *Client) DismissPendingFolder(ctx context.Context, folderID, deviceID string) error {
	if folderID == "" || deviceID == "" {
		return errors.New("folder and device ID required")
	}
	return c.do(ctx, "DELETE", "/rest/cluster/pending/folders", url.Values{"folder": {folderID}, "device": {deviceID}}, nil, nil)
}

// IgnorePendingFolder persistently ignores a peer's unsolicited offer, without
// ignoring the peer itself or any legitimately shared folder. On update failure
// the offer remains pending for retry; a failed dismissal is reported to caller.
func (c *Client) IgnorePendingFolder(ctx context.Context, folderID, deviceID, label string) error {
	if folderID == "" || deviceID == "" {
		return errors.New("folder and device ID required")
	}
	device, err := c.Device(ctx, deviceID)
	if err != nil {
		return err
	}
	for _, ignored := range device.IgnoredFolders {
		if ignored.ID == folderID {
			return c.DismissPendingFolder(ctx, folderID, deviceID)
		}
	}
	device.IgnoredFolders = append(device.IgnoredFolders, IgnoredFolder{ID: folderID, Label: label, Time: time.Now().UTC()})
	if err := c.UpdateDevice(ctx, device); err != nil {
		return err
	}
	return c.DismissPendingFolder(ctx, folderID, deviceID)
}

type Event struct {
	ID   int64           `json:"id"`
	Type string          `json:"type"`
	Time time.Time       `json:"time"`
	Data json.RawMessage `json:"data"`
}

// Events polls the daemon event stream after since; the returned data is global
// and must be filtered before any user-facing response.
func (c *Client) Events(ctx context.Context, since int64, limit int) ([]Event, error) {
	if since < 0 || limit < 1 {
		return nil, errors.New("invalid event cursor or limit")
	}
	var result []Event
	err := c.do(ctx, "GET", "/rest/events", url.Values{"since": {strconv.FormatInt(since, 10)}, "limit": {strconv.Itoa(limit)}}, nil, &result)
	return result, err
}
