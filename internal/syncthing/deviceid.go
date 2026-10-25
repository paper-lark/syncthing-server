package syncthing

import "strings"

const deviceAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567"

// ValidDeviceID checks Syncthing's canonical eight-group ID and its four
// Luhn32 check characters. Shape alone does not catch mistyped peer IDs.
func ValidDeviceID(id string) bool {
	if len(id) != 63 {
		return false
	}
	for i, ch := range id {
		if (i+1)%8 == 0 {
			if ch != '-' {
				return false
			}
		} else if !strings.ContainsRune(deviceAlphabet, ch) {
			return false
		}
	}
	plain := strings.ReplaceAll(id, "-", "")
	for start := 0; start < len(plain); start += 14 {
		factor, sum := 1, 0
		for i := start; i < start+13; i++ {
			value := strings.IndexByte(deviceAlphabet, plain[i])
			product := factor * value
			sum += product/32 + product%32
			factor = 3 - factor
		}
		if plain[start+13] != deviceAlphabet[(32-sum%32)%32] {
			return false
		}
	}
	return true
}
