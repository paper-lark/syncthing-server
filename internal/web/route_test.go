package web_test

import (
	"net/http"
	"testing"
)

func TestUnknownBrowserRouteIsNotDashboard(t *testing.T) {
	handler, _, cookie := fixture(t)
	response := serve(handler, cookie, http.MethodGet, "/not-a-route", nil)
	if response.Code != http.StatusNotFound {
		t.Fatalf("unknown route returned %d instead of 404", response.Code)
	}
}
