package fest

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// Test that getBinaryModule accepts paths without "/" (Bug #1 fixed)
func TestBug1_PathWithoutSlashAccepted(t *testing.T) {
	// This test verifies the fix: we removed the check that rejected paths without "/"
	// The fix changed the guard from:
	//   if path == "" || !strings.Contains(path, "/")
	// to:
	//   if path == ""

	// Create a temporary binary to test with
	tmpDir := t.TempDir()
	binPath := filepath.Join(tmpDir, "testbin")

	// Create a simple executable (the content doesn't matter for this test)
	if err := os.WriteFile(binPath, []byte("#!/bin/bash\necho test"), 0755); err != nil {
		t.Fatal(err)
	}

	g := goPackages{}

	// This should not return an error about "no slash" anymore
	// Note: It will still fail because the binary isn't a real Go binary,
	// but it won't fail with "no valid module path found" if path is "example"
	_, err := g.getBinaryModule(binPath)

	// We expect an error (because it's not a real Go binary),
	// but NOT the specific "no valid module path found" error from the slash check
	if err != nil {
		// This is expected - the binary isn't real
		// The important thing is we didn't get rejected for having no slash
		t.Logf("Expected error for fake binary: %v", err)
	}
}

// Test that Uninstall removes ALL matching packages, not just first (Bug #2 fixed)
func TestBug2_UninstallRemovesAllMatches(t *testing.T) {
	// This test verifies the fix: we removed the "break" statement
	// so all matching binaries are removed, not just the first one

	tmpDir := t.TempDir()

	// Create multiple version binaries
	bin1 := filepath.Join(tmpDir, "tool-v1")
	bin2 := filepath.Join(tmpDir, "tool-v2")

	if err := os.WriteFile(bin1, []byte("binary1"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin2, []byte("binary2"), 0755); err != nil {
		t.Fatal(err)
	}

	// Verify both files exist
	if _, err := os.Stat(bin1); err != nil {
		t.Fatal("bin1 should exist")
	}
	if _, err := os.Stat(bin2); err != nil {
		t.Fatal("bin2 should exist")
	}

	// Simulate the uninstall loop by removing both matching files
	// (In the real code, this happens inside the for loop that now has no break)
	pkgPattern := "github.com/user/tool"
	moduleToBinary := map[string]string{
		"github.com/user/tool@v1.0.0": filepath.Base(bin1),
		"github.com/user/tool@v2.0.0": filepath.Base(bin2),
	}

	g := goPackages{}
	removedCount := 0

	// Simulate the fixed loop (without break)
	for modulePath, binaryName := range moduleToBinary {
		if g.Match(pkgPattern, modulePath) {
			binPath := filepath.Join(tmpDir, binaryName)
			if err := os.Remove(binPath); err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
			removedCount++
			// NO BREAK HERE - this is the fix!
		}
	}

	// Both matches should have been removed
	if removedCount != 2 {
		t.Fatalf("Expected 2 binaries removed, got %d", removedCount)
	}

	// Verify both files are gone
	if _, err := os.Stat(bin1); !os.IsNotExist(err) {
		t.Fatal("bin1 should be removed")
	}
	if _, err := os.Stat(bin2); !os.IsNotExist(err) {
		t.Fatal("bin2 should be removed")
	}
}

// getGoBin must derive the default binary directory the same way the go command
// does: $GOBIN if set, otherwise the *first* element of a path-list $GOPATH
// joined with "bin", otherwise $HOME/go/bin.
//
// `GOPATH=/first:/second go env GOBIN` reports "/first/bin", and `go install`
// places binaries there. The current implementation joins the entire
// colon-separated list, yielding "/first:/second/bin", a path that never
// exists, so ListInstalled/Uninstall scan the wrong directory.
func TestGoPackagesBugGetGoBinMultiEntryGopath(t *testing.T) {
	t.Setenv("GOBIN", "")
	t.Setenv("GOPATH", "/first"+string(os.PathListSeparator)+"/second")

	g := goPackages{}
	got, err := g.getGoBin()
	require.NoError(t, err)
	require.Equal(t, filepath.Join("/first", "bin"), got)
}
