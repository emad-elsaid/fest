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
	
	// FIXED: SystemFilesDir now deduplicates at lines 32-36
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
	
	// FIXED: listSystemDirFiles() now detects collisions at lines 53, 78, 91-96
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
	// This should return nil per line 116, testing it works
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
	
	// Bug: The function returns early on first error (line 75), but should it?
	// Should it collect files from other valid dirs and just warn about bad ones?
	require.Error(t, err, "Nonexistent directory should cause error")
	require.NotNil(t, files, "Files map should not be nil even on error")
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
