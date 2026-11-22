package expac

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLocal(t *testing.T) {
	dbPath := t.TempDir()

	writeDesc(t, dbPath, "foo-1.0-1", `%NAME%
foo
%VERSION%
1.0-1
%DEPENDS%
bar>=1.0
baz
%PROVIDES%
virtual=1.0
`)
	writeDesc(t, dbPath, "bar-2.0-1", `%NAME%
bar
%VERSION%
2.0-1`)

	// A file where a package directory is expected must be ignored.
	require.NoError(t, os.WriteFile(filepath.Join(dbPath, "local", "stray"), []byte("x"), 0o644))

	packages, err := Local(dbPath)
	require.NoError(t, err)

	require.ElementsMatch(t, []Package{
		{Name: "foo", Depends: []string{"bar>=1.0", "baz"}, Provides: []string{"virtual=1.0"}, Explicit: true},
		{Name: "bar", Explicit: true},
	}, packages)
}

// TestLocalMarksDependencies verifies that a REASON block marks a dependency.
func TestLocalMarksDependencies(t *testing.T) {
	dbPath := t.TempDir()

	writeDesc(t, dbPath, "foo-1.0-1", "%NAME%\nfoo\n")
	writeDesc(t, dbPath, "bar-2.0-1", "%NAME%\nbar\n%REASON%\n1\n")

	packages, err := Local(dbPath)
	require.NoError(t, err)

	require.ElementsMatch(t, []Package{
		{Name: "foo", Explicit: true},
		{Name: "bar", Explicit: false},
	}, packages)
}

func TestLocalRejectsUnreadableEntry(t *testing.T) {
	dbPath := t.TempDir()
	writeDesc(t, dbPath, "foo-1.0-1", "%NAME%\nfoo\n")

	// A package directory without a desc file would silently drop its
	// dependencies, so it must fail the whole read.
	require.NoError(t, os.MkdirAll(filepath.Join(dbPath, "local", "broken-1.0-1"), 0o755))

	_, err := Local(dbPath)
	require.Error(t, err)
}

func TestLocalMissingDatabase(t *testing.T) {
	_, err := Local(filepath.Join(t.TempDir(), "missing"))
	require.ErrorIs(t, err, ErrNoDatabase)
}

func TestDBPath(t *testing.T) {
	confPath := filepath.Join(t.TempDir(), "pacman.conf")

	require.NoError(t, os.WriteFile(confPath,
		[]byte("[options]\nDBPath = /custom/pacman\n"), 0o644))
	require.Equal(t, "/custom/pacman", dbPath(confPath))

	require.NoError(t, os.WriteFile(confPath, []byte("[options]\n"), 0o644))
	require.Equal(t, DefaultDBPath, dbPath(confPath))

	require.Equal(t, DefaultDBPath, dbPath(filepath.Join(t.TempDir(), "missing")))
}

func TestDBPathFromInclude(t *testing.T) {
	dir := t.TempDir()

	included := filepath.Join(dir, "dbpath.conf")
	require.NoError(t, os.WriteFile(included, []byte("DBPath = /included/db\n"), 0o644))

	confPath := filepath.Join(dir, "pacman.conf")
	require.NoError(t, os.WriteFile(confPath,
		[]byte("[options]\nInclude = "+included+"\n"), 0o644))

	require.Equal(t, "/included/db", dbPath(confPath))
}

func TestDBPathFromIncludeGlob(t *testing.T) {
	dir := t.TempDir()
	confDir := filepath.Join(dir, "conf.d")
	require.NoError(t, os.MkdirAll(confDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(confDir, "10-base.conf"),
		[]byte("DBPath = /first\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(confDir, "20-override.conf"),
		[]byte("DBPath = /second\n"), 0o644))

	confPath := filepath.Join(dir, "pacman.conf")
	require.NoError(t, os.WriteFile(confPath,
		[]byte("[options]\nInclude = "+filepath.Join(confDir, "*.conf")+"\n"), 0o644))

	require.Equal(t, "/second", dbPath(confPath), "the last glob match wins, like pacman")
}

func writeDesc(t *testing.T, dbPath, entry, contents string) {
	t.Helper()

	dir := filepath.Join(dbPath, "local", entry)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "desc"), []byte(contents), 0o644))
}
