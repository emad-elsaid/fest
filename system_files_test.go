package fest

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestSystemFilesDir_DuplicateDirectoriesDeduped tests that SystemFilesDir now deduplicates
func TestSystemFilesDir_DuplicateDirectoriesDeduped(t *testing.T) {
	// Reset global state
	original := systemFilesDirs
	defer func() { systemFilesDirs = original }()

	systemFilesDirs = []string{"system"}

	// FIXED: SystemFilesDir now deduplicates
	//   for _, existing := range systemFilesDirs {
	//       if existing == dir {
	//           return
	//       }
	//   }

	// Add same directory twice
	SystemFilesDir("system")
	SystemFilesDir("system")
	SystemFilesDir("other")
	SystemFilesDir("system") // Try again

	// Should have unique entries only
	require.Equal(t, 2, len(systemFilesDirs),
		"Should deduplicate: %v", systemFilesDirs)
	require.Contains(t, systemFilesDirs, "system")
	require.Contains(t, systemFilesDirs, "other")
}

// TestListSystemDirFiles_OverwriteCollisionWarning tests that collisions are now warned about
func TestListSystemDirFiles_OverwriteCollisionWarning(t *testing.T) {
	original := systemFilesDirs
	defer func() { systemFilesDirs = original }()

	// Create two directories with files targeting same destination
	tmpDir := t.TempDir()
	dir1 := filepath.Join(tmpDir, "dir1")
	dir2 := filepath.Join(tmpDir, "dir2")

	os.MkdirAll(filepath.Join(dir1, "etc"), 0755)
	os.MkdirAll(filepath.Join(dir2, "etc"), 0755)

	os.WriteFile(filepath.Join(dir1, "etc", "config"), []byte("version1"), 0644)
	os.WriteFile(filepath.Join(dir2, "etc", "config"), []byte("version2"), 0644)

	systemFilesDirs = []string{dir1, dir2}

	files, err := listSystemDirFiles()
	require.NoError(t, err)

	// FIXED: listSystemDirFiles() now detects collisions
	//   targetToSources := make(map[string][]string)
	//   targetToSources[targetPath] = append(targetToSources[targetPath], path)
	//   if len(sources) > 1 {
	//       slog.Warn("Multiple source files target same destination (last one wins)", ...)
	//   }

	// Count distinct target paths
	targetPaths := make(map[string][]string)
	for srcPath, targetPath := range files {
		targetPaths[targetPath] = append(targetPaths[targetPath], srcPath)
	}

	// The collision is tracked, and both sources are retained
	require.Contains(t, targetPaths, "/etc/config", "Target path should exist")
	require.ElementsMatch(t, []string{
		filepath.Join(dir1, "etc", "config"),
		filepath.Join(dir2, "etc", "config"),
	}, targetPaths["/etc/config"], "Should track both sources that target /etc/config")
}

// TestHashBytes_HasPrefix tests that hashBytes includes "sha256:" prefix
// for hash algorithm identification and forward compatibility
func TestHashBytes_HasPrefix(t *testing.T) {
	data := []byte("test content")
	hash := hashBytes(data)

	// Hash should include "sha256:" prefix for algorithm identification
	require.True(t, strings.HasPrefix(hash, "sha256:"),
		"Hash should have 'sha256:' prefix for algorithm identification")

	// Verify hex part is valid
	hexPart := strings.TrimPrefix(hash, "sha256:")
	_, err := hex.DecodeString(hexPart)
	require.NoError(t, err, "Hex part should be valid hex string")
	require.Equal(t, 64, len(hexPart), "SHA256 hex should be 64 characters")
}

// TestBackupFile_PathConstruction tests the backup path construction logic
func TestBackupFile_PathConstruction(t *testing.T) {
	targetPath := "/etc/hosts"
	content := []byte("127.0.0.1 localhost")

	relBackupPath, err := backupFile(targetPath, content)
	require.NoError(t, err)

	// Verify path is constructed correctly
	expected := filepath.Join("backups", "etc", "hosts.backup")
	require.Equal(t, expected, relBackupPath,
		"Backup path should be backups/etc/hosts.backup, got: %s", relBackupPath)

	// Cleanup
	os.RemoveAll(filepath.Join(stateDir(), "backups", "etc"))
}

// TestDeleteBackup_EmptyPathHandling tests edge case handling
func TestDeleteBackup_EmptyPathHandling(t *testing.T) {
	// Empty paths should return nil
	err := deleteBackup("")
	require.NoError(t, err, "Empty path should be handled gracefully")
}

// TestListSystemDirFiles_EmptyDirHandling tests that empty directory strings
// are skipped properly
func TestListSystemDirFiles_EmptyDirHandling(t *testing.T) {
	original := systemFilesDirs
	defer func() { systemFilesDirs = original }()

	systemFilesDirs = []string{"", "", ""}

	files, err := listSystemDirFiles()
	require.NoError(t, err)
	require.Empty(t, files, "Empty directories should be skipped")
}

// TestListSystemDirFiles_NonexistentDir tests error handling for missing directories
func TestListSystemDirFiles_NonexistentDir(t *testing.T) {
	original := systemFilesDirs
	defer func() { systemFilesDirs = original }()

	systemFilesDirs = []string{"/this/path/definitely/does/not/exist/12345"}

	files, err := listSystemDirFiles()

	// Bug: The function returns early on first error, but should it?
	// Should it collect files from other valid dirs and just warn about bad ones?
	require.Error(t, err, "Nonexistent directory should cause error")
	require.NotNil(t, files, "Files map should not be nil even on error")
}

// TestListInstalled_IncludesOrphansInState tests that a file whose source was
// deleted from the system dirs is still reported as installed (from state), so
// apply offers to remove it instead of leaving it behind forever.
func TestListInstalled_IncludesOrphansInState(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	target := filepath.Join(home, "etc", "sddm.conf.d", "retro.conf")
	require.NoError(t, os.MkdirAll(filepath.Dir(target), 0755))
	require.NoError(t, os.WriteFile(target, []byte("old"), 0644))

	require.NoError(t, saveState(&systemFilesState{Version: 1, Files: map[string]fileState{
		target: {InstalledHash: hashBytes([]byte("old"))},
	}}))

	original := systemFilesDirs
	defer func() { systemFilesDirs = original }()
	emptySource := filepath.Join(home, "system")
	require.NoError(t, os.MkdirAll(emptySource, 0755))
	systemFilesDirs = []string{emptySource}

	installed, err := systemFiles{}.ListInstalled()
	require.NoError(t, err)
	require.Contains(t, installed, target, "orphaned tracked file should be reported installed")
}

// TestReadFileWithSudo_RegularFile tests normal file reading without sudo
func TestReadFileWithSudo_RegularFile(t *testing.T) {
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.txt")
	expectedContent := []byte("test content")

	err := os.WriteFile(testFile, expectedContent, 0644)
	require.NoError(t, err)

	content, err := readFileWithSudo(testFile)
	require.NoError(t, err)
	require.Equal(t, expectedContent, content)
}

// TestReadFileWithSudo_NonexistentFile tests error handling for missing files
func TestReadFileWithSudo_NonexistentFile(t *testing.T) {
	content, err := readFileWithSudo("/tmp/this-file-does-not-exist-12345.txt")
	require.Error(t, err)
	require.Nil(t, content)
}

// systemFilesBugFakeSudo installs a `sudo` shim at the front of PATH so the
// Install/Uninstall code paths (which shell out to sudo) can execute without
// real root. The shim no-ops sudo's auth probes and otherwise runs the command.
func systemFilesBugFakeSudo(t *testing.T) {
	t.Helper()
	bin := t.TempDir()
	script := "#!/bin/sh\ncase \"$1\" in -n|-v) exit 0 ;; esac\nexec \"$@\"\n"
	require.NoError(t, os.WriteFile(filepath.Join(bin, "sudo"), []byte(script), 0755))
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// TestSystemFilesBugListInstalledWhitespaceContent documents that ListInstalled
// must compare file contents exactly. A target that differs from the source
// only by surrounding whitespace (e.g. a missing trailing newline) is NOT the
// declared file and must be reported as not-installed so apply re-deploys it.
//
// Bug: ListInstalled compared strings.TrimSpace(src) == strings.TrimSpace(dst),
// so whitespace-only differences were silently treated as up to date.
func TestSystemFilesBugListInstalledWhitespaceContent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	const relDir = "fest_bug_sysfiles_ws"
	rel := filepath.Join("tmp", relDir, "app.conf")
	src := filepath.Join(home, "system")
	require.NoError(t, os.MkdirAll(filepath.Join(src, filepath.Dir(rel)), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(src, rel), []byte("content\n"), 0644))

	target := "/" + rel
	require.NoError(t, os.MkdirAll(filepath.Dir(target), 0755))
	// Different content: missing the trailing newline the source declares.
	require.NoError(t, os.WriteFile(target, []byte("content"), 0644))
	t.Cleanup(func() { os.RemoveAll("/tmp/" + relDir) })

	orig := systemFilesDirs
	defer func() { systemFilesDirs = orig }()
	systemFilesDirs = []string{src}

	installed, err := systemFiles{}.ListInstalled()
	require.NoError(t, err)
	require.NotContains(t, installed, target,
		"target whose content differs from source must not be reported as installed")
}

// TestSystemFilesBugInstallPartialStateNotPersisted documents that when Install
// deploys several files and one of them fails, the state of the files that were
// already successfully deployed must still be persisted. Today Install only
// calls saveState at the very end, so an early return discards the tracking of
// files that were already written to disk, leaving them unrestorable/orphaned.
//
// Bug: Install returned from inside the loop on error without persisting state
// accumulated for previously deployed files.
func TestSystemFilesBugInstallPartialStateNotPersisted(t *testing.T) {
	systemFilesBugFakeSudo(t)

	home := t.TempDir()
	t.Setenv("HOME", home)

	const relDir = "fest_bug_sysfiles_partial"
	rel := filepath.Join("tmp", relDir, "ok.conf")
	src := filepath.Join(home, "system")
	require.NoError(t, os.MkdirAll(filepath.Join(src, filepath.Dir(rel)), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(src, rel), []byte("hello"), 0644))

	okTarget := "/" + rel
	t.Cleanup(func() { os.RemoveAll("/tmp/" + relDir) })

	orig := systemFilesDirs
	defer func() { systemFilesDirs = orig }()
	systemFilesDirs = []string{src}

	// First op deploys fine; second op has no source and aborts the batch.
	err := systemFiles{}.Install([]string{okTarget, "/definitely/not/a/managed/target"})
	require.Error(t, err)

	_, statErr := os.Stat(okTarget)
	require.NoError(t, statErr, "first file should have been deployed")

	state, loadErr := loadState()
	require.NoError(t, loadErr)
	_, tracked := state.Files[okTarget]
	require.True(t, tracked,
		"successfully deployed file must be tracked even when a later op fails")
}
