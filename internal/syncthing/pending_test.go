package syncthing_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"syncthing-server/internal/syncthing"
)

func TestIgnorePendingFolderPersistsPerPeerAndIsRetryable(t *testing.T) {
	var current = `{"deviceID":"PEER","name":"peer","unrelatedSetting":42,"ignoredFolders":[{"id":"older","label":"earlier","time":"2024-01-01T00:00:00Z","extra":"retained"}]}`
	var writes, dismissals int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET /rest/config/devices/PEER":
			io.WriteString(w, current)
		case "PUT /rest/config/devices/PEER":
			b, err := io.ReadAll(r.Body)
			if err != nil {
				t.Error(err)
			}
			current = string(b)
			writes++
			var record map[string]any
			if err := json.Unmarshal(b, &record); err != nil {
				t.Error(err)
			}
			if record["unrelatedSetting"] != float64(42) {
				t.Errorf("lost device settings: %s", b)
			}
			offers := record["ignoredFolders"].([]any)
			if len(offers) != 2 || offers[0].(map[string]any)["extra"] != "retained" || offers[1].(map[string]any)["id"] != "foreign" || offers[1].(map[string]any)["time"] == "" {
				t.Errorf("ignored folder config invalid: %s", b)
			}
		case "DELETE /rest/cluster/pending/folders":
			if r.URL.Query().Get("device") != "PEER" || r.URL.Query().Get("folder") != "foreign" {
				t.Errorf("dismissed wrong offer: %s", r.URL)
			}
			dismissals++
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	c, _ := syncthing.NewClient(server.URL, "k", nil)
	for i := 0; i < 2; i++ {
		if err := c.IgnorePendingFolder(context.Background(), "foreign", "PEER", "unsolicited"); err != nil {
			t.Fatal(err)
		}
	}
	if writes != 1 || dismissals != 2 {
		t.Errorf("writes=%d dismissals=%d", writes, dismissals)
	}
}

func TestClientDoesNotSendAPIKeyToRedirectTarget(t *testing.T) {
	var leaked bool
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked = r.Header.Get("X-API-Key") != "" }))
	defer target.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer origin.Close()
	c, _ := syncthing.NewClient(origin.URL, "highly-secret", nil)
	_, err := c.Version(context.Background())
	if err == nil || leaked || strings.Contains(err.Error(), "highly-secret") {
		t.Fatalf("redirect should fail closed without leaking credential: %v leaked=%t", err, leaked)
	}
}
