package syncthing_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"syncthing-server/internal/syncthing"
)

func TestClientVersionStatusAndMyID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-API-Key") != "secret" {
			t.Errorf("missing API key")
			w.WriteHeader(403)
			return
		}
		switch r.URL.Path {
		case "/gui/rest/system/version":
			io.WriteString(w, `{"version":"v1.29.0","longVersion":"new"}`)
		case "/gui/rest/system/status":
			io.WriteString(w, `{"myID":"SERVER","uptime":12}`)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	client, err := syncthing.NewClient(server.URL+"/gui/", "secret", nil)
	if err != nil {
		t.Fatal(err)
	}
	version, err := client.Version(context.Background())
	if err != nil || version.Version != "v1.29.0" {
		t.Fatalf("Version: %+v %v", version, err)
	}
	status, err := client.Status(context.Background())
	if err != nil || status.MyID != "SERVER" {
		t.Fatalf("Status: %+v %v", status, err)
	}
	id, err := client.MyID(context.Background())
	if err != nil || id != "SERVER" {
		t.Fatalf("MyID: %q %v", id, err)
	}
}

func TestFoldersAndDevicesPreserveUnknownConfiguration(t *testing.T) {
	requests := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.EscapedPath())
		switch {
		case r.Method == "GET" && r.URL.Path == "/rest/config/folders":
			io.WriteString(w, `[{"id":"f","label":"old","group":"Family","path":"/data/f","type":"sendreceive","rescanIntervalS":19,"devices":[{"deviceID":"PEER","introducedBy":"SERVER"}]}]`)
		case r.Method == "GET" && r.URL.Path == "/rest/config/devices":
			io.WriteString(w, `[{"deviceID":"PEER","name":"old","compression":"metadata","ignoredFolders":[{"id":"earlier","label":"original","time":"2024-01-01T00:00:00Z"}]}]`)
		case r.Method == "PUT" && r.URL.Path == "/rest/config/folders/f":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body["rescanIntervalS"] != float64(19) || body["label"] != "new" || body["group"] != "Family" {
				t.Errorf("folder fields lost: %v", body)
			}
			nested := body["devices"].([]any)[0].(map[string]any)
			if nested["introducedBy"] != "SERVER" {
				t.Errorf("nested field lost: %v", body)
			}
		case r.Method == "PUT" && r.URL.Path == "/rest/config/devices/PEER":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body["compression"] != "metadata" || body["name"] != "new" || len(body["ignoredFolders"].([]any)) != 1 {
				t.Errorf("device fields lost: %v", body)
			}
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	client, _ := syncthing.NewClient(server.URL, "secret", nil)
	folders, err := client.Folders(context.Background())
	if err != nil || len(folders) != 1 {
		t.Fatalf("Folders: %v %v", folders, err)
	}
	folders[0].Label = "new"
	if err := client.UpdateFolder(context.Background(), folders[0]); err != nil {
		t.Fatal(err)
	}
	devices, err := client.Devices(context.Background())
	if err != nil || len(devices) != 1 {
		t.Fatalf("Devices: %v %v", devices, err)
	}
	devices[0].Name = "new"
	if err := client.UpdateDevice(context.Background(), devices[0]); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(requests, []string{"GET /rest/config/folders", "PUT /rest/config/folders/f", "GET /rest/config/devices", "PUT /rest/config/devices/PEER"}) {
		t.Fatal(requests)
	}
}

func TestConfigWritesAndEscapedIDs(t *testing.T) {
	var calls []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.EscapedPath())
		if r.Method != "DELETE" {
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
		}
	}))
	defer server.Close()
	c, _ := syncthing.NewClient(server.URL, "k", nil)
	ctx := context.Background()
	if err := c.CreateFolder(ctx, syncthing.Folder{ID: "f/1", Path: "/safe", Type: "sendreceive"}); err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteFolder(ctx, "f/1"); err != nil {
		t.Fatal(err)
	}
	if err := c.CreateDevice(ctx, syncthing.Device{DeviceID: "A/B", Name: "peer"}); err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteDevice(ctx, "A/B"); err != nil {
		t.Fatal(err)
	}
	expected := []string{"POST /rest/config/folders", "DELETE /rest/config/folders/f%2F1", "POST /rest/config/devices", "DELETE /rest/config/devices/A%2FB"}
	if !reflect.DeepEqual(calls, expected) {
		t.Fatalf("got %v want %v", calls, expected)
	}
}

func TestPendingRequestsAndEvents(t *testing.T) {
	var calls []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.RequestURI())
		switch r.URL.Path {
		case "/rest/cluster/pending/devices":
			if r.Method == "GET" {
				io.WriteString(w, `{"DEV":{"name":"offered"}}`)
			}
		case "/rest/cluster/pending/folders":
			if r.Method == "GET" {
				io.WriteString(w, `{"f":{"offeredBy":{"DEV":{"label":"offered"}}}}`)
			}
		case "/rest/events":
			io.WriteString(w, `[{"id":42,"type":"PendingDevicesChanged","data":{"device":"DEV"}}]`)
		default:
			t.Errorf("unexpected %s", r.URL)
		}
	}))
	defer server.Close()
	c, _ := syncthing.NewClient(server.URL, "k", nil)
	ctx := context.Background()
	devices, err := c.PendingDevices(ctx)
	if err != nil || len(devices) != 1 {
		t.Fatalf("pending devices: %v %v", devices, err)
	}
	folders, err := c.PendingFolders(ctx)
	if err != nil || len(folders) != 1 {
		t.Fatalf("pending folders: %v %v", folders, err)
	}
	if err := c.DismissPendingDevice(ctx, "A/B"); err != nil {
		t.Fatal(err)
	}
	if err := c.DismissPendingFolder(ctx, "f/1", "A/B"); err != nil {
		t.Fatal(err)
	}
	events, err := c.Events(ctx, 41, 3)
	if err != nil || len(events) != 1 || events[0].ID != 42 {
		t.Fatalf("events: %v %v", events, err)
	}
	want := []string{"GET /rest/cluster/pending/devices", "GET /rest/cluster/pending/folders", "DELETE /rest/cluster/pending/devices?device=A%2FB", "DELETE /rest/cluster/pending/folders?device=A%2FB&folder=f%2F1", "GET /rest/events?limit=3&since=41"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("got %v want %v", calls, want)
	}
}

func TestHTTPFailuresAndCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/rest/system/version" {
			w.Header().Set("Content-Type", "text/plain")
			w.WriteHeader(http.StatusUnauthorized)
			io.WriteString(w, strings.Repeat("token-leak", 10000))
			return
		}
		<-r.Context().Done()
	}))
	defer server.Close()
	c, _ := syncthing.NewClient(server.URL, "super-secret", server.Client())
	_, err := c.Version(context.Background())
	var apiErr *syncthing.HTTPError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != 401 || strings.Contains(err.Error(), "super-secret") || len(err.Error()) > 1024 {
		t.Fatalf("unsafe error: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	_, err = c.Status(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("context not propagated: %v", err)
	}
}

func TestStatusAndStatsEndpoints(t *testing.T) {
	paths := map[string]string{
		"/rest/db/status?folder=f%2F1":                 `{"state":"idle"}`,
		"/rest/db/completion?device=PEER&folder=f%2F1": `{"completion":100}`,
		"/rest/stats/folder":                           `{"f/1":{"lastScan":"today"}}`,
		"/rest/stats/device":                           `{"PEER":{"lastSeen":"today"}}`,
		"/rest/system/connections":                     `{"connections":{"PEER":{"connected":true}},"total":{"inBytesTotal":999}}`,
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, ok := paths[r.URL.RequestURI()]
		if !ok {
			t.Errorf("unexpected %s", r.URL)
			w.WriteHeader(404)
			return
		}
		io.WriteString(w, data)
	}))
	defer server.Close()
	c, _ := syncthing.NewClient(server.URL, "k", nil)
	ctx := context.Background()
	if value, err := c.FolderStatus(ctx, "f/1"); err != nil || !strings.Contains(string(value), `"idle"`) {
		t.Fatalf("folder status: %s %v", value, err)
	}
	if value, err := c.Completion(ctx, "f/1", "PEER"); err != nil || !strings.Contains(string(value), `100`) {
		t.Fatalf("completion: %s %v", value, err)
	}
	if value, err := c.FolderStats(ctx); err != nil || len(value) != 1 {
		t.Fatalf("folder stats: %v %v", value, err)
	}
	if value, err := c.DeviceStats(ctx); err != nil || len(value) != 1 {
		t.Fatalf("device stats: %v %v", value, err)
	}
	if value, err := c.Connections(ctx); err != nil || !strings.Contains(string(value), `"PEER"`) {
		t.Fatalf("connections: %s %v", value, err)
	}
}

func TestClientRejectsInvalidURLAndCredentials(t *testing.T) {
	for _, tc := range []struct{ url, key string }{{"", "key"}, {"file:///tmp/socket", "key"}, {"http://host", ""}, {"http://user:password@host", "key"}, {"https://host?key=secret", "key"}} {
		if _, err := syncthing.NewClient(tc.url, tc.key, nil); err == nil {
			t.Errorf("accepted URL %q key %q", tc.url, tc.key)
		}
	}
}
