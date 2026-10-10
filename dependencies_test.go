package fest

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestIsInstalled_EmptyName tests behavior when dependency has empty Name
func TestIsInstalled_EmptyName(t *testing.T) {
	dep := dependency{
		Name:     "", // Empty name
		Required: true,
	}

	// Bug: isInstalled doesn't validate empty names
	// Expected: should return false or panic with clear error
	// Actual: executes "which ''" which may behave unpredictably
	result := isInstalled(dep)

	// This test expects the function to handle empty names gracefully
	// Current implementation will execute "which ''" which is incorrect
	require.False(t, result, "isInstalled should return false for empty dependency name")
}

// TestIsInstalled_EmptyCheckCmd tests behavior with empty CheckCmd elements
func TestIsInstalled_EmptyCheckCmd(t *testing.T) {
	tests := []struct {
		name     string
		dep      dependency
		expected bool
	}{
		{
			name: "empty command in CheckCmd",
			dep: dependency{
				Name:     "test",
				CheckCmd: []string{"", "arg"},
			},
			expected: false,
		},
		{
			name: "CheckCmd with only empty string",
			dep: dependency{
				Name:     "test",
				CheckCmd: []string{""},
			},
			expected: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Bug: isInstalled doesn't validate CheckCmd[0] is non-empty
			// Executing empty command leads to unpredictable behavior
			result := isInstalled(tc.dep)
			require.Equal(t, tc.expected, result,
				"isInstalled should handle empty CheckCmd elements gracefully")
		})
	}
}

// TestDependencyPkg_EmptyFields tests pkg() with empty Name and PackageName
func TestDependencyPkg_EmptyFields(t *testing.T) {
	tests := []struct {
		name        string
		dep         dependency
		expectEmpty bool
	}{
		{
			name: "both Name and PackageName empty",
			dep: dependency{
				Name:        "",
				PackageName: "",
			},
			expectEmpty: true,
		},
		{
			name: "Name empty, PackageName set",
			dep: dependency{
				Name:        "",
				PackageName: "valid-package",
			},
			expectEmpty: false,
		},
		{
			name: "Name set, PackageName empty",
			dep: dependency{
				Name:        "valid-name",
				PackageName: "",
			},
			expectEmpty: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := tc.dep.pkg()
			if tc.expectEmpty {
				// Bug: pkg() can return empty string, which will cause issues
				// when used in installDependencies()
				require.Empty(t, result, "pkg() returns empty when both fields are empty")
			} else {
				require.NotEmpty(t, result, "pkg() should return non-empty string")
			}
		})
	}
}

// TestDepNames_NilSlice tests depNames with nil input
func TestDepNames_NilSlice(t *testing.T) {
	// Edge case: nil slice
	result := depNames(nil)

	// Bug: depNames doesn't handle nil input gracefully
	// make([]string, len(nil)) creates a slice of length 0, but returns nil slice behavior
	// Expected: should return empty slice or handle nil explicitly
	require.NotNil(t, result, "depNames should return non-nil slice for nil input")
	require.Empty(t, result, "depNames should return empty slice for nil input")
}

// TestDepNames_EmptySlice tests depNames with empty slice
func TestDepNames_EmptySlice(t *testing.T) {
	result := depNames([]dependency{})

	require.NotNil(t, result, "depNames should return non-nil slice")
	require.Empty(t, result, "depNames should return empty slice for empty input")
}

// TestFilterMissing_NilSlice tests filterMissing with nil input
func TestFilterMissing_NilSlice(t *testing.T) {
	result := filterMissing(nil)

	// Similar edge case: nil slice iteration
	require.NotNil(t, result, "filterMissing should return non-nil slice")
	require.Empty(t, result, "filterMissing should return empty slice for nil input")
}

// TestInstallDependencies_EmptyPackageNames demonstrates the bug where
// empty package names are passed to pacman/yay without validation
func TestInstallDependencies_EmptyPackageNames(t *testing.T) {
	deps := []dependency{
		{
			Name:        "", // Empty name
			PackageName: "", // Empty package name
			Required:    true,
		},
	}

	// Bug: installDependencies doesn't validate that dep.pkg() returns non-empty strings
	// This will attempt to run "pacman -Si ''" which may behave unpredictably
	//
	// Expected behavior: Should return error for invalid dependency BEFORE attempting install
	// Actual behavior: Proceeds with empty package name, yay reports "Query arg too small"
	//                  but function returns nil (success) instead of error
	//
	// This test demonstrates the bug: function should validate inputs and error early,
	// but instead it attempts installation and returns success despite failure
	err := installDependencies(deps)

	// BUG DEMONSTRATED: The function returns nil despite trying to install invalid package
	// This is a silent failure - no error returned even though nothing was installed
	require.Error(t, err, "installDependencies should validate and error on empty package names before attempting install")
}

// TestCheckDependencies_AllOptionalMissing tests when only optional deps are missing
func TestCheckDependencies_AllOptionalMissing(t *testing.T) {
	// Save original dependencies
	originalDeps := dependencies

	// Mock dependencies with only optional ones
	dependencies = []dependency{
		{Name: "nonexistent-optional-tool-xyz123", Required: false},
	}

	// Restore after test
	defer func() { dependencies = originalDeps }()

	// Should not error when only optional dependencies are missing
	err := checkDependencies()

	require.NoError(t, err, "checkDependencies should not error when only optional deps missing")
}

// DependenciesBugWriteExecutable creates an executable file so it can be
// discovered through PATH lookup.
func DependenciesBugWriteExecutable(t *testing.T, dir, name string) {
	t.Helper()
	path := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755))
}

// TestDependenciesBugIsInstalledRequiresExternalWhich documents that
// isInstalled detects a dependency by shelling out to the
// external `which` binary instead of using os/exec.LookPath.
//
// Contract: `dependency.Name` is the command name to look for, and isInstalled
// must report it as installed whenever that command is resolvable through PATH.
//
// The current implementation runs `types.Cmd("which", dep.Name)`, so on any
// system that lacks the standalone `which` program (it is not part of Arch's
// `base` group, only `base-devel`, and it is absent from minimal/container
// images) every dependency is reported missing even when the tool is present.
// checkDependencies then tries to reinstall already-installed tools and aborts
// with "failed to install required dependencies".
func TestDependenciesBugIsInstalledRequiresExternalWhich(t *testing.T) {
	dir := t.TempDir()
	DependenciesBugWriteExecutable(t, dir, "DependenciesBugPresentTool")

	// Point PATH at the temp dir only: the tool below is resolvable, but the
	// external `which` helper is not.
	oldPath := os.Getenv("PATH")
	require.NoError(t, os.Setenv("PATH", dir))
	defer func() { _ = os.Setenv("PATH", oldPath) }()

	dep := dependency{Name: "DependenciesBugPresentTool", Required: true}
	require.True(t, isInstalled(dep),
		"a command that is present in PATH must be reported as installed, even when the external `which` helper is unavailable")
}
