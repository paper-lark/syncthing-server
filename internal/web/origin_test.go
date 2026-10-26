package web_test

import (
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"syncthing-server/internal/config"
	"syncthing-server/internal/web"
)

func TestFormOriginPolicyKeepsBrowserOrigin(t *testing.T) {
	handler, _, cookie := fixture(t)
	response := serve(handler, cookie, "GET", "/", nil)
	if policy := response.Header().Get("Referrer-Policy"); policy != "same-origin" {
		t.Fatalf("form posts need a non-opaque browser Origin; Referrer-Policy: %q", policy)
	}
}

func TestCreateFolderAcceptsOriginOfServingHost(t *testing.T) {
	_, db, cookie := fixture(t)
	handler := web.New(config.Config{BaseURL: "http://127.0.0.1:48080"}, db, nil, nil, nil)
	form := url.Values{"csrf": {csrf(t, handler, cookie)}}
	for _, tc := range []struct {
		name, origin string
		want         int
	}{
		{"serving host", "http://localhost:48080", 400},
		{"configured base URL", "http://127.0.0.1:48080", 400},
		{"foreign host", "http://other.example", 403},
		{"wrong scheme", "https://localhost:48080", 403},
		{"opaque origin", "null", 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "http://localhost:48080/folders", strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.Header.Set("Origin", tc.origin)
			req.AddCookie(cookie)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, req)
			if response.Code != tc.want || (tc.want == 403 && !strings.Contains(response.Body.String(), "invalid origin")) {
				t.Fatalf("folder origin %q: HTTP %d %s", tc.origin, response.Code, response.Body.String())
			}
		})
	}
}
