package fest

import (
	"testing"

	"github.com/stretchr/testify/require"
)

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

// TestCommonBugAddUniqueDuplicateItemsWithinOneCall documents that addUnique
// must not append the same item twice when it is passed more than once in a
// single call (e.g. fest.Package("vim", "vim")).
func TestCommonBugAddUniqueDuplicateItemsWithinOneCall(t *testing.T) {
	var s []string
	addUnique(&s, "a", "a")
	require.Equal(t, []string{"a"}, s)
}

func TestCommonBugAddUniqueDuplicateItemsWithExisting(t *testing.T) {
	s := []string{"b"}
	addUnique(&s, "a", "b", "a")
	require.Equal(t, []string{"b", "a"}, s)
}
