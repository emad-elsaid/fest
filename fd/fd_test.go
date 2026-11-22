package fd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSymlinks(t *testing.T) {
	root := t.TempDir()

	link := func(name, target string) string {
		t.Helper()
		path := filepath.Join(root, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.Symlink(target, path))
		return path
	}

	wanted := []string{
		link("top", "/nonexistent"),
		link("keep/nested/link", "/nonexistent"),
		link(".hidden/link", "/nonexistent"),
		link(".local/other/link", "/nonexistent"),
	}

	link("node_modules/excluded", "/nonexistent")
	link(".git/excluded", "/nonexistent")
	link(".local/state/excluded", "/nonexistent")

	require.NoError(t, os.WriteFile(filepath.Join(root, "regular"), []byte("x"), 0o644))

	links, err := Symlinks(root, []string{"node_modules", ".git", ".local/state"})
	require.NoError(t, err)
	require.ElementsMatch(t, wanted, links)

	for _, path := range links {
		require.True(t, filepath.IsAbs(path), "%s should be absolute", path)
	}
}

func TestSymlinksSymlinkedRoot(t *testing.T) {
	realRoot := t.TempDir()
	require.NoError(t, os.Symlink("/nonexistent", filepath.Join(realRoot, "link")))

	linkRoot := filepath.Join(t.TempDir(), "home")
	require.NoError(t, os.Symlink(realRoot, linkRoot))

	links, err := Symlinks(linkRoot, nil)
	require.NoError(t, err)
	require.Equal(t, []string{filepath.Join(linkRoot, "link")}, links,
		"paths must stay under the caller's root")
}

func TestSymlinksMissingRoot(t *testing.T) {
	_, err := Symlinks(filepath.Join(t.TempDir(), "missing"), nil)
	require.Error(t, err)
}

// TestSymlinksNonDirectoryRoot verifies that a file root yields no links.
func TestSymlinksNonDirectoryRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(root, nil, 0o644))

	links, err := Symlinks(root, nil)
	require.NoError(t, err)
	require.Empty(t, links)
}

// TestSymlinksGlobAndNestedExcludes covers glob and root-relative excludes.
func TestSymlinksGlobAndNestedExcludes(t *testing.T) {
	root := t.TempDir()

	link := func(name string) string {
		t.Helper()
		path := filepath.Join(root, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.Symlink("/nonexistent", path))
		return path
	}

	wanted := []string{
		link("kept"),
		link("logs/kept.txt"),
	}

	link("debug.log")
	link("logs/trace.log")
	link("build/output/excluded")
	link("nested/build/output/excluded")
	link("nested/build/output/kept")

	links, err := Symlinks(root, []string{"*.log", "build/output"})
	require.NoError(t, err)
	require.ElementsMatch(t, wanted, links)
}
