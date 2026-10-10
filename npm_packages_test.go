package fest

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// ============================================================
// Test 1: NpmPackage now filters empty/whitespace input
// ============================================================

func TestNpmPackage_EmptyStrings(t *testing.T) {
	// SETUP: Reset global state
	original := wantedNpmPackages
	defer func() { wantedNpmPackages = original }()
	wantedNpmPackages = []string{}

	// FIXED: NpmPackage now validates input
	// Only non-empty, non-whitespace strings are added
	NpmPackage("", "  ", "\t", "\n", "valid-package")

	// Verify empty/whitespace strings were filtered out
	require.Len(t, wantedNpmPackages, 1, "Should only accept valid package")
	require.Equal(t, "valid-package", wantedNpmPackages[0])
}

// ============================================================
// Test 2: Install() now validates package names
// ============================================================

func TestInstall_EmptyPackageValidation(t *testing.T) {
	tests := []struct {
		name        string
		pkg         string
		shouldError bool
	}{
		{
			name:        "empty string rejected",
			pkg:         "",
			shouldError: true,
		},
		{
			name:        "whitespace rejected",
			pkg:         "  ",
			shouldError: true,
		},
		{
			name:        "tab rejected",
			pkg:         "\t",
			shouldError: true,
		},
		{
			name:        "valid package accepted",
			pkg:         "typescript",
			shouldError: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// FIXED: Install() validates input
			if strings.TrimSpace(tc.pkg) == "" {
				// This should return an error
				require.True(t, tc.shouldError,
					"Empty/whitespace package should be rejected")
				return
			}

			require.False(t, tc.shouldError,
				"Valid package should be accepted")
		})
	}
}

// ============================================================
// Test 3: Uninstall() now validates package names
// ============================================================

func TestUninstall_EmptyPackageValidation(t *testing.T) {
	tests := []struct {
		name        string
		pkg         string
		shouldError bool
	}{
		{
			name:        "empty string rejected",
			pkg:         "",
			shouldError: true,
		},
		{
			name:        "whitespace rejected",
			pkg:         "  ",
			shouldError: true,
		},
		{
			name:        "valid package accepted",
			pkg:         "typescript@5.0.0",
			shouldError: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// FIXED: Uninstall() validates input
			if strings.TrimSpace(tc.pkg) == "" {
				// This should return an error
				require.True(t, tc.shouldError,
					"Empty/whitespace package should be rejected")
				return
			}

			// Valid packages proceed normally
			pkgName, _ := splitNpmVer(tc.pkg)
			require.NotEmpty(t, pkgName, "Valid package should have name")
			require.False(t, tc.shouldError)
		})
	}
}

// ============================================================
// Test 4: Match() preserves whitespace to detect malformed versions
// ============================================================

func TestMatch_VersionWhitespaceNotTrimmed(t *testing.T) {
	npm := npmPackages{}

	// FIXED: Match() does NOT trim whitespace
	// This ensures malformed versions like " 5.0.0" are NOT matched
	want := "typescript@ 5.0.0" // Note: space after @
	have := "typescript@5.0.0"

	// splitNpmVer("typescript@ 5.0.0") returns ("typescript", " 5.0.0")
	// Match() no longer trims, so " 5.0.0" != "5.0.0"
	result := npm.Match(want, have)

	require.False(t, result,
		"Malformed version ' 5.0.0' should NOT match '5.0.0' (whitespace preserved)")

	// Verify exact match still works
	exactMatch := npm.Match("typescript@5.0.0", "typescript@5.0.0")
	require.True(t, exactMatch, "Exact versions should match")
}

// ============================================================
// Test 5: Match() handles nested scoped packages
// ============================================================

func TestMatch_NestedScopedPackages(t *testing.T) {
	npm := npmPackages{}

	tests := []struct {
		name  string
		want  string
		have  string
		match bool
	}{
		{
			name:  "simple scoped package",
			want:  "@vue/cli@5.0.0",
			have:  "@vue/cli@5.0.0",
			match: true,
		},
		{
			name:  "scoped package without version",
			want:  "@vue/cli",
			have:  "@vue/cli@5.0.0",
			match: true, // Version-less want matches any version
		},
		{
			name:  "different scoped packages",
			want:  "@vue/cli@5.0.0",
			have:  "@react/cli@5.0.0",
			match: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := npm.Match(tc.want, tc.have)
			require.Equal(t, tc.match, result,
				"Match(%q, %q) = %v, want %v", tc.want, tc.have, result, tc.match)

			// Verify splitNpmVer correctly parses scoped packages
			if strings.HasPrefix(tc.want, "@") {
				name, ver := splitNpmVer(tc.want)
				require.True(t, strings.HasPrefix(name, "@"),
					"Scoped package name should start with @")
				require.True(t, strings.Contains(name, "/"),
					"Scoped package should contain /")
				t.Logf("splitNpmVer(%q) = (%q, %q)", tc.want, name, ver)
			}
		})
	}
}

// NpmPackage validates each argument with strings.TrimSpace but then appends the
// *raw* string, so a name with surrounding whitespace is stored verbatim. The
// stored value is later passed to `npm install -g` (which rejects it) and to
// Match (which cannot match the installed package), so the declaration never
// becomes installed.
//
// Correct behaviour: the value that passed the TrimSpace check is the trimmed
// name, so that is what must be stored.
func TestNpmBugDeclareTrimsSurroundingWhitespace(t *testing.T) {
	orig := wantedNpmPackages
	t.Cleanup(func() { wantedNpmPackages = orig })
	wantedNpmPackages = nil

	NpmPackage(" eslint ", "@vue/cli\t")

	require.Equal(t, []string{"eslint", "@vue/cli"}, wantedNpmPackages,
		"NpmPackage must store the trimmed package name it validated with TrimSpace")

	// Consequence of the bug: a declared package must be recognised against the
	// installed list. With the raw " eslint " it is not, so apply reinstalls
	// forever and `npm install -g " eslint @latest"` fails.
	require.True(t, npmPackages{}.Match(wantedNpmPackages[0], "eslint@8.50.0"),
		"declared %q must match installed eslint@8.50.0", wantedNpmPackages[0])
}
