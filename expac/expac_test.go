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

	// A directory without a desc file must be skipped.
	require.NoError(t, os.MkdirAll(filepath.Join(dbPath, "local", "broken-1.0-1"), 0o755))

	packages, err := Local(dbPath)
	require.NoError(t, err)

	require.ElementsMatch(t, []Package{
		{Name: "foo", Depends: []string{"bar>=1.0", "baz"}, Provides: []string{"virtual=1.0"}},
		{Name: "bar"},
	}, packages)
}

func TestLocalMissingDatabase(t *testing.T) {
	packages, err := Local(filepath.Join(t.TempDir(), "missing"))
	require.NoError(t, err)
	require.Empty(t, packages)
}

func writeDesc(t *testing.T, dbPath, entry, contents string) {
	t.Helper()

	dir := filepath.Join(dbPath, "local", entry)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "desc"), []byte(contents), 0o644))
}
