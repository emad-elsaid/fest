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

func TestSymlinksMissingRoot(t *testing.T) {
	_, err := Symlinks(filepath.Join(t.TempDir(), "missing"), nil)
	require.Error(t, err)
}
