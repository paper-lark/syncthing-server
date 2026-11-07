package web

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestFormatBytes(t *testing.T) {
	for _, tc := range []struct {
		bytes int64
		want  string
	}{
		{0, "0 B"},
		{4, "4 B"},
		{999, "999 B"},
		{1000, "1.0 KB"},
		{4244146, "4.2 MB"},
		{1000000000, "1.0 GB"},
	} {
		if got := formatBytes(tc.bytes); got != tc.want {
			t.Errorf("formatBytes(%d) = %q, want %q", tc.bytes, got, tc.want)
		}
	}
}

func TestPeerCompletionWholePercent(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  string
	}{
		{`{"completion":100}`, "100% synced"},
		{`{"completion":65.2}`, "65% synced"},
		{`{"completion":99.9}`, "99% synced"},
		{`{"completion":-1}`, "Unavailable"},
	} {
		if got := peerCompletion(json.RawMessage(tc.value)); got != tc.want {
			t.Errorf("peerCompletion(%s) = %q, want %q", tc.value, got, tc.want)
		}
	}
}

func TestRelativeTime(t *testing.T) {
	now := time.Date(2026, time.November, 7, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		elapsed time.Duration
		want    string
	}{
		{-time.Minute, "just now"},
		{59 * time.Second, "just now"},
		{time.Minute, "1 minute ago"},
		{2*time.Minute + 59*time.Second, "2 minutes ago"},
		{time.Hour, "1 hour ago"},
		{3 * time.Hour, "3 hours ago"},
		{24 * time.Hour, "1 day ago"},
		{49 * time.Hour, "2 days ago"},
	} {
		if got := relativeTime(now.Add(-tc.elapsed), now); got != tc.want {
			t.Errorf("relativeTime(%s) = %q, want %q", tc.elapsed, got, tc.want)
		}
	}
}

func TestLastScanAndLastSeenRelative(t *testing.T) {
	now := time.Now().UTC()
	stamp := now.Add(-2*time.Hour - 10*time.Second).Format(time.RFC3339)
	for _, tc := range []struct {
		name  string
		input string
		parse func(json.RawMessage) (string, string, string)
	}{
		{"last scan", `{"lastScan":"` + stamp + `"}`, folderLastScan},
		{"last seen", `{"lastSeen":"` + stamp + `"}`, deviceLastSeen},
	} {
		t.Run(tc.name, func(t *testing.T) {
			label, datetime, title := tc.parse(json.RawMessage(tc.input))
			if label != "2 hours ago" || datetime != stamp || !strings.Contains(title, now.Add(-2*time.Hour-10*time.Second).Format("02 Jan 2006")) {
				t.Errorf("got %q, %q, %q", label, datetime, title)
			}
		})
	}
}
