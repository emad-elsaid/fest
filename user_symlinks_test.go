package fest

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestFilterBrokenSymlinks_IgnoreFilesMatchesPartialNames tests that ignore
// rules match exact file names, not suffixes of them.
func TestFilterBrokenSymlinks_IgnoreFilesMatchesPartialNames(t *testing.T) {
	tmpDir := t.TempDir()

	// Create broken symlinks with names similar to ignored files
	// "MySingletonCookie" ends with "SingletonCookie" which is in ignoreFiles
	brokenPrefixedSymlink := filepath.Join(tmpDir, "MySingletonCookie")
	require.NoError(t, os.Symlink("/nonexistent/target1", brokenPrefixedSymlink))

	brokenNormalSymlink := filepath.Join(tmpDir, "normal_link")
	require.NoError(t, os.Symlink("/nonexistent/target2", brokenNormalSymlink))

	broken := brokenLinks([]string{brokenPrefixedSymlink, brokenNormalSymlink})

	// Ignore rules must match the exact file name, not a suffix of it.
	require.Len(t, broken, 2, "Both broken symlinks should be included")
	require.Contains(t, broken, brokenPrefixedSymlink,
		"MySingletonCookie should not be ignored just because it ends with 'SingletonCookie'")
	require.Contains(t, broken, brokenNormalSymlink)
}

// A symlink whose target cannot be resolved is broken no matter which errno
// resolution fails with. brokenLinks must report symlink loops (ELOOP) and
// symlinks that traverse a non-directory component (ENOTDIR), while leaving
// healthy links alone.
func TestBrokenLinksReportsNonENOENTFailures(t *testing.T) {
	dir := t.TempDir()

	// Self-referential symlink: os.Stat returns ELOOP, not ENOENT.
	loop := filepath.Join(dir, "loop")
	require.NoError(t, os.Symlink(loop, loop))

	// Symlink through a regular file: os.Stat returns ENOTDIR, not ENOENT.
	file := filepath.Join(dir, "file")
	require.NoError(t, os.WriteFile(file, []byte("x"), 0o644))
	throughFile := filepath.Join(dir, "through_file")
	require.NoError(t, os.Symlink(filepath.Join(file, "child"), throughFile))

	// A healthy symlink must not be reported.
	target := filepath.Join(dir, "target")
	require.NoError(t, os.WriteFile(target, []byte("x"), 0o644))
	healthy := filepath.Join(dir, "healthy")
	require.NoError(t, os.Symlink(target, healthy))

	stdin := []string{loop, throughFile, healthy}

	broken := brokenLinks(stdin)
	require.Contains(t, broken, loop, "a symlink loop is broken and must be reported")
	require.Contains(t, broken, throughFile, "a symlink through a non-directory is broken and must be reported")
	require.NotContains(t, broken, healthy, "a healthy symlink must not be reported")
}
