package fest

import "testing"

func TestIsVersionLocked(t *testing.T) {
	tests := []struct {
		name     string
		version  string
		expected bool
	}{
		{"empty version is not locked", "", false},
		{"whitespace version is not locked", "   ", false},
		{"latest is not locked", "latest", false},
		{"latest with whitespace is not locked", " latest ", false},
		{"exact version is locked", "1.0.0", true},
		{"exact version with whitespace is locked", " 1.0.0 ", true},
		{"pessimistic constraint is locked", "~>1.0", true},
		{"minimum constraint is locked", ">=1.0.0", true},
		{"equality constraint is locked", "=1.0.0", true},
		{"version with platform suffix is locked", "1.16.0 x86_64-linux", true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isVersionLocked(tc.version); got != tc.expected {
				t.Errorf("isVersionLocked(%q) = %v, want %v", tc.version, got, tc.expected)
			}
		})
	}
}
