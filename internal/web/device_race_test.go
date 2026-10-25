package web_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"syncthing-server/internal/config"
	"syncthing-server/internal/syncthing"
	"syncthing-server/internal/web"
)

func TestSimultaneousPeerClaimDoesNotBlockLosingUser(t *testing.T) {
	_, db, aliceCookie := fixture(t)
	ctx := context.Background()
	alice, err := db.FindUser(ctx, "https://issuer.example", "alice")
	if err != nil {
		t.Fatal(err)
	}
	bob, err := db.UpsertUser(ctx, "https://issuer.example", "bob", "Bob", "", true)
	if err != nil {
		t.Fatal(err)
	}
	bobCookie := &http.Cookie{Name: "session", Value: "bob-test-session"}
	if err := db.CreateSession(ctx, bobCookie.Value, bob.ID, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	id := "P56IOI7-MZJNU2Y-IQGDREY-DM2MGTI-MGL3BXN-PQ6W5BM-TBBZ4TJ-XZWICQ2"
	postArrived := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	var mu sync.Mutex
	present := false
	writes := 0
	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/rest/system/status":
			json.NewEncoder(w).Encode(map[string]string{"myID": "SERVER"})
		case r.URL.Path == "/rest/config/devices/"+id && r.Method == http.MethodGet:
			mu.Lock()
			exists := present
			mu.Unlock()
			if !exists {
				http.NotFound(w, r)
				return
			}
			json.NewEncoder(w).Encode(syncthing.Device{DeviceID: id, Name: "Peer"})
		case r.URL.Path == "/rest/config/devices" && r.Method == http.MethodPost:
			mu.Lock()
			writes++
			present = true
			mu.Unlock()
			once.Do(func() { close(postArrived); <-release })
		default:
			http.NotFound(w, r)
		}
	}))
	defer daemon.Close()
	client, err := syncthing.NewClient(daemon.URL, "key", daemon.Client())
	if err != nil {
		t.Fatal(err)
	}
	handler := web.New(config.Config{BaseURL: "https://example.test"}, db, client, nil, nil)
	bobCSRF := csrf(t, handler, bobCookie)
	aliceCSRF := csrf(t, handler, aliceCookie)
	submit := func(cookie *http.Cookie, token string) *httptest.ResponseRecorder {
		return serve(handler, cookie, http.MethodPost, "/devices", url.Values{"csrf": {token}, "id": {id}, "name": {"Peer"}})
	}
	bobDone := make(chan *httptest.ResponseRecorder, 1)
	go func() { bobDone <- submit(bobCookie, bobCSRF) }()
	select {
	case <-postArrived:
	case <-time.After(5 * time.Second):
		t.Fatal("first peer add did not reach daemon")
	}
	aliceDone := make(chan *httptest.ResponseRecorder, 1)
	go func() { aliceDone <- submit(aliceCookie, aliceCSRF) }()
	close(release)
	if response := <-bobDone; response.Code != http.StatusSeeOther {
		t.Fatalf("first add failed: %d %s", response.Code, response.Body.String())
	}
	if response := <-aliceDone; response.Code != http.StatusConflict {
		t.Fatalf("racing add should conflict: %d %s", response.Code, response.Body.String())
	}
	mu.Lock()
	count := writes
	mu.Unlock()
	if count != 1 {
		t.Fatalf("racing request overwrote daemon %d times", count)
	}
	blocked, _, err := db.WriteStatus(ctx, alice.ID)
	if err != nil || blocked {
		t.Fatalf("losing user falsely blocked: %v %v", blocked, err)
	}
}
