package web_test

import (
	"net/http"
	"strings"
	"testing"
)

func TestHealthEndpointIsAvailableWithoutSession(t *testing.T) {
	handler, _, _ := fixture(t)
	response := serve(handler, nil, http.MethodGet, "/healthz", nil)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "ok") {
		t.Fatalf("health unavailable: %d %s", response.Code, response.Body.String())
	}
}
