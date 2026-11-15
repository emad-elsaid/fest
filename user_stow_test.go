package fest

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// getDirs must preserve a symlinked working directory instead of resolving it.
func TestUserStow_GetDirs_SymlinkWorkingDir(t *testing.T) {
	tmpDir := t.TempDir()
	symlinkPath := filepath.Join(tmpDir, "dotfiles-link")
	actualPath := filepath.Join(tmpDir, "dotfiles-actual")

	require.NoError(t, os.Mkdir(actualPath, 0755))
	require.NoError(t, os.Symlink(actualPath, symlinkPath))

	originalWd, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(originalWd) })

	originalPWD := os.Getenv("PWD")
	t.Cleanup(func() { _ = os.Setenv("PWD", originalPWD) })
	originalHome := os.Getenv("HOME")
	t.Cleanup(func() { _ = os.Setenv("HOME", originalHome) })

	require.NoError(t, os.Chdir(symlinkPath))
	require.NoError(t, os.Setenv("PWD", symlinkPath))
	require.NoError(t, os.Setenv("HOME", actualPath))

	u := newUserStow()
	dotfilesDir, targetDir, err := u.getDirs()
	require.NoError(t, err)
	require.Equal(t, symlinkPath, dotfilesDir, "getDirs should preserve symlink path")
	require.Equal(t, actualPath, targetDir)
}

// withDotfilesEnv points PWD and HOME at a temporary dotfiles tree and returns
// the dotfiles directory and home directory.
func withDotfilesEnv(t *testing.T) (dotfilesDir, homeDir string) {
	t.Helper()
	root := t.TempDir()
	dotfilesDir = filepath.Join(root, "dotfiles")
	homeDir = filepath.Join(root, "home")
	require.NoError(t, os.MkdirAll(filepath.Join(dotfilesDir, "user"), 0755))
	require.NoError(t, os.MkdirAll(homeDir, 0755))

	t.Setenv("PWD", dotfilesDir)
	t.Setenv("HOME", homeDir)
	return dotfilesDir, homeDir
}

func TestUserStow_ApplySymlinksDotfiles(t *testing.T) {
	dotfilesDir, homeDir := withDotfilesEnv(t)

	require.NoError(t, os.WriteFile(
		filepath.Join(dotfilesDir, "user", ".bashrc"),
		[]byte("export FOO=1\n"), 0644,
	))

	require.NoError(t, newUserStow().Apply())

	linkPath := filepath.Join(homeDir, ".bashrc")
	info, err := os.Lstat(linkPath)
	require.NoError(t, err)
	require.NotZero(t, info.Mode()&os.ModeSymlink)

	content, err := os.ReadFile(linkPath)
	require.NoError(t, err)
	require.Equal(t, "export FOO=1\n", string(content))

	// Applying twice is idempotent.
	require.NoError(t, newUserStow().Apply())
}

func TestUserStow_ApplyKeepsExistingTargetWhenNoOverrideNeeded(t *testing.T) {
	dotfilesDir, homeDir := withDotfilesEnv(t)

	require.NoError(t, os.MkdirAll(filepath.Join(dotfilesDir, "user", ".config", "nvim"), 0755))
	require.NoError(t, os.WriteFile(
		filepath.Join(dotfilesDir, "user", ".config", "nvim", "init.lua"),
		[]byte("set number\n"), 0644,
	))

	require.NoError(t, newUserStow().Apply())

	content, err := os.ReadFile(filepath.Join(homeDir, ".config", "nvim", "init.lua"))
	require.NoError(t, err)
	require.Equal(t, "set number\n", string(content))
}

func TestUserStow_SaveAdoptsFiles(t *testing.T) {
	dotfilesDir, homeDir := withDotfilesEnv(t)

	require.NoError(t, os.WriteFile(
		filepath.Join(dotfilesDir, "user", ".bashrc"),
		[]byte("stowed version\n"), 0644,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(homeDir, ".bashrc"),
		[]byte("edited in home\n"), 0644,
	))

	require.NoError(t, newUserStow().Save())

	// The home edit is adopted into the dotfile tree...
	adopted, err := os.ReadFile(filepath.Join(dotfilesDir, "user", ".bashrc"))
	require.NoError(t, err)
	require.Equal(t, "edited in home\n", string(adopted))

	// ...and the home path becomes a symlink to it.
	info, err := os.Lstat(filepath.Join(homeDir, ".bashrc"))
	require.NoError(t, err)
	require.NotZero(t, info.Mode()&os.ModeSymlink)

	// Saving again is a no-op.
	require.NoError(t, newUserStow().Save())
}

func TestUserStow_DiffDoesNotChangeHome(t *testing.T) {
	dotfilesDir, homeDir := withDotfilesEnv(t)

	require.NoError(t, os.WriteFile(
		filepath.Join(dotfilesDir, "user", ".vimrc"),
		[]byte("set relativenumber\n"), 0644,
	))

	require.NoError(t, newUserStow().Diff())

	_, err := os.Lstat(filepath.Join(homeDir, ".vimrc"))
	require.ErrorIs(t, err, os.ErrNotExist)
}

// showDiff must not attempt to diff directories; it should report and return.
func TestUserStow_ShowDiff_Directories(t *testing.T) {
	tmpDir := t.TempDir()
	stowPath := filepath.Join(tmpDir, "stow-dir")
	targetPath := filepath.Join(tmpDir, "target-dir")

	require.NoError(t, os.MkdirAll(stowPath, 0755))
	require.NoError(t, os.MkdirAll(targetPath, 0755))

	require.NoError(t, newUserStow().showDiff(targetPath, stowPath))
}

// showDiff on a target that does not exist in the stow tree yet is not an error.
func TestUserStow_ShowDiff_MissingStowFile(t *testing.T) {
	tmpDir := t.TempDir()
	stowPath := filepath.Join(tmpDir, "missing")
	targetPath := filepath.Join(tmpDir, "target")
	require.NoError(t, os.WriteFile(targetPath, []byte("x"), 0644))

	require.NoError(t, newUserStow().showDiff(targetPath, stowPath))
}

// options must keep Diff and Apply in sync, and enable adoption only for Save.
func TestUserStow_Options(t *testing.T) {
	u := newUserStow()

	apply := u.options("/dotfiles", "/home", false)
	require.Equal(t, "/dotfiles", apply.StowDir)
	require.Equal(t, "/home", apply.TargetDir)
	require.False(t, apply.Adopt)
	require.NotEmpty(t, apply.Override, "Apply replaces links owned by other packages")

	save := u.options("/dotfiles", "/home", true)
	require.True(t, save.Adopt)
}

// chooseStub records the offered items and returns a fixed choice.
func chooseStub(offered *[]string, choice string) func(string, []string) (string, error) {
	return func(_ string, items []string) (string, error) {
		*offered = append([]string(nil), items...)
		return choice, nil
	}
}

func TestUserStow_ApplyOverwriteForeignSymlink(t *testing.T) {
	dotfilesDir, homeDir := withDotfilesEnv(t)
	require.NoError(t, os.WriteFile(
		filepath.Join(dotfilesDir, "user", ".zshrc"), []byte("stow\n"), 0644))

	foreign := filepath.Join(filepath.Dir(dotfilesDir), "foreign")
	require.NoError(t, os.WriteFile(foreign, []byte("foreign\n"), 0644))
	require.NoError(t, os.Symlink(foreign, filepath.Join(homeDir, ".zshrc")))

	u := newUserStow()
	var offered []string
	u.choose = chooseStub(&offered, "Overwrite")

	require.NoError(t, u.Apply())
	require.Contains(t, offered, "Overwrite")

	content, err := os.ReadFile(filepath.Join(homeDir, ".zshrc"))
	require.NoError(t, err)
	require.Equal(t, "stow\n", string(content))
}

func TestUserStow_ApplyKeepsForeignSymlink(t *testing.T) {
	dotfilesDir, homeDir := withDotfilesEnv(t)
	require.NoError(t, os.WriteFile(
		filepath.Join(dotfilesDir, "user", ".zshrc"), []byte("stow\n"), 0644))

	foreign := filepath.Join(filepath.Dir(dotfilesDir), "foreign")
	require.NoError(t, os.WriteFile(foreign, []byte("foreign\n"), 0644))
	require.NoError(t, os.Symlink(foreign, filepath.Join(homeDir, ".zshrc")))

	u := newUserStow()
	var offered []string
	u.choose = chooseStub(&offered, "Keep target")

	require.NoError(t, u.Apply())

	link, err := os.Readlink(filepath.Join(homeDir, ".zshrc"))
	require.NoError(t, err)
	require.Equal(t, foreign, link)
}

// Overwriting is not offered when the conflict target is a directory, since
// that would require deleting a whole tree.
func TestUserStow_ApplyDoesNotOfferOverwriteForDirectories(t *testing.T) {
	dotfilesDir, homeDir := withDotfilesEnv(t)
	require.NoError(t, os.WriteFile(
		filepath.Join(dotfilesDir, "user", "bin"), []byte("not a dir\n"), 0644))
	require.NoError(t, os.MkdirAll(filepath.Join(homeDir, "bin"), 0755))
	require.NoError(t, os.WriteFile(
		filepath.Join(homeDir, "bin", "tool"), []byte("tool\n"), 0644))

	u := newUserStow()
	var offered []string
	u.choose = chooseStub(&offered, "Keep target")

	require.NoError(t, u.Apply())
	require.Equal(t, []string{"Show diff", "Keep target"}, offered)

	content, err := os.ReadFile(filepath.Join(homeDir, "bin", "tool"))
	require.NoError(t, err)
	require.Equal(t, "tool\n", string(content))
}
