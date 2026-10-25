package syncthing_test

import (
	"syncthing-server/internal/syncthing"
	"testing"
)

func TestCanonicalDeviceIDChecksums(t *testing.T) {
	for _, tc := range []struct {
		id    string
		valid bool
	}{
		{"P56IOI7-MZJNU2Y-IQGDREY-DM2MGTI-MGL3BXN-PQ6W5BM-TBBZ4TJ-XZWICQ2", true},
		{"P56IOI7-MZJNU2Y-IQGDREY-DM2MGTI-MGL3BXN-PQ6W5BM-TBBZ4TJ-XZWICQ3", false},
		{"P56IOI7MZJNU2YIQGDREYDM2MGTIMGL3BXNPQ6W5BMTBBZ4TJXZWICQ2", false},
		{"ABC2EFG-ABC2EFG-ABC2EFG-ABC2EFG-ABC2EFG-ABC2EFG-ABC2EFG-ABC2EFG", false},
	} {
		if got := syncthing.ValidDeviceID(tc.id); got != tc.valid {
			t.Errorf("device ID %q valid=%v want %v", tc.id, got, tc.valid)
		}
	}
}
