package main

import "testing"

func TestCheckSyncthingVersion(t *testing.T) {
	for _, tc := range []struct {
		version string
		major   int
		ok      bool
	}{
		{"v2.1.3", 2, true},
		{"v1.30.0", 2, false},
		{"v2.1.3-rc.1", 2, true},
		{"development", 2, false},
		{"", 2, false},
	} {
		err := checkSyncthingVersion(tc.version, tc.major)
		if (err == nil) != tc.ok {
			t.Errorf("version %q major %d: err=%v want ok=%v", tc.version, tc.major, err, tc.ok)
		}
	}
}
