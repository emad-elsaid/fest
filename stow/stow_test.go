package stow

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/stretchr/testify/require"
)

func setupStowTest(t *testing.T) (stowDir, targetDir string) {
	t.Helper()
	root := t.TempDir()
	stowDir = filepath.Join(root, "dotfiles")
	targetDir = filepath.Join(root, "home")
	require.NoError(t, os.MkdirAll(stowDir, 0755))
	require.NoError(t, os.MkdirAll(targetDir, 0755))
	return stowDir, targetDir
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0644))
}

func TestStow_BasicFileAndIdempotency(t *testing.T) {
	stowDir, targetDir := setupStowTest(t)
	writeFile(t, filepath.Join(stowDir, "user", ".bashrc"), "export FOO=1\n")

	var buf bytes.Buffer
	opts := Options{
		StowDir:   stowDir,
		TargetDir: targetDir,
		Verbose:   true,
		Writer:    &buf,
	}

	plan, err := Stow("user", opts)
	require.NoError(t, err)
	require.Len(t, plan.Actions, 1)
	require.Equal(t, ActionLink, plan.Actions[0].Kind)

	linkPath := filepath.Join(targetDir, ".bashrc")
	info, err := os.Lstat(linkPath)
	require.NoError(t, err)
	require.NotZero(t, info.Mode()&os.ModeSymlink)

	rawTarget, err := os.Readlink(linkPath)
	require.NoError(t, err)
	require.False(t, filepath.IsAbs(rawTarget), "symlink should be relative, got %q", rawTarget)

	data, err := os.ReadFile(linkPath)
	require.NoError(t, err)
	require.Equal(t, "export FOO=1\n", string(data))

	// Second run is a no-op
	plan2, err := Stow("user", opts)
	require.NoError(t, err)
	require.Empty(t, plan2.Actions)
}

func TestStow_TreeFoldingAndUnfolding(t *testing.T) {
	stowDir, targetDir := setupStowTest(t)
	writeFile(t, filepath.Join(stowDir, "pkg1", ".config", "app1", "conf"), "app1")
	writeFile(t, filepath.Join(stowDir, "pkg2", ".config", "app2", "conf"), "app2")

	// Stowing pkg1 folds .config into a single symlink.
	_, err := Stow("pkg1", Options{StowDir: stowDir, TargetDir: targetDir})
	require.NoError(t, err)

	cfgPath := filepath.Join(targetDir, ".config")
	info, err := os.Lstat(cfgPath)
	require.NoError(t, err)
	require.NotZero(t, info.Mode()&os.ModeSymlink, ".config should be folded as a symlink")

	// Stowing pkg2 unfolds .config into a real directory and links both app1 and app2.
	_, err = Stow("pkg2", Options{StowDir: stowDir, TargetDir: targetDir})
	require.NoError(t, err)

	info, err = os.Lstat(cfgPath)
	require.NoError(t, err)
	require.True(t, info.IsDir(), ".config should be unfolded into a real directory")
	require.Zero(t, info.Mode()&os.ModeSymlink)

	d1, err := os.ReadFile(filepath.Join(cfgPath, "app1", "conf"))
	require.NoError(t, err)
	require.Equal(t, "app1", string(d1))

	d2, err := os.ReadFile(filepath.Join(cfgPath, "app2", "conf"))
	require.NoError(t, err)
	require.Equal(t, "app2", string(d2))
}

func TestStow_SimulateDoesNotModifyFilesystem(t *testing.T) {
	stowDir, targetDir := setupStowTest(t)
	writeFile(t, filepath.Join(stowDir, "user", ".vimrc"), "set number\n")

	plan, err := Stow("user", Options{
		StowDir:   stowDir,
		TargetDir: targetDir,
		Simulate:  true,
	})
	require.NoError(t, err)
	require.Len(t, plan.Actions, 1)

	_, err = os.Lstat(filepath.Join(targetDir, ".vimrc"))
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestStow_Conflicts(t *testing.T) {
	stowDir, targetDir := setupStowTest(t)
	writeFile(t, filepath.Join(stowDir, "user", ".bashrc"), "stow bashrc")
	writeFile(t, filepath.Join(targetDir, ".bashrc"), "existing bashrc")

	other := filepath.Join(filepath.Dir(stowDir), "other")
	writeFile(t, other, "other")
	writeFile(t, filepath.Join(stowDir, "user", ".profile"), "stow profile")
	require.NoError(t, os.Symlink(other, filepath.Join(targetDir, ".profile")))

	plan, err := Stow("user", Options{StowDir: stowDir, TargetDir: targetDir})
	require.Error(t, err)
	var conflictErr *ConflictError
	require.ErrorAs(t, err, &conflictErr)
	require.Len(t, conflictErr.Conflicts, 2)
	require.Len(t, plan.Conflicts, 2)
}

func TestStow_AdoptAndOverride(t *testing.T) {
	stowDir, targetDir := setupStowTest(t)
	writeFile(t, filepath.Join(stowDir, "user", ".bashrc"), "old stow")
	writeFile(t, filepath.Join(targetDir, ".bashrc"), "edited in home")

	_, err := Stow("user", Options{
		StowDir:   stowDir,
		TargetDir: targetDir,
		Adopt:     true,
	})
	require.NoError(t, err)

	// Stow file now has the adopted content, and target is a symlink to it.
	adopted, err := os.ReadFile(filepath.Join(stowDir, "user", ".bashrc"))
	require.NoError(t, err)
	require.Equal(t, "edited in home", string(adopted))

	info, err := os.Lstat(filepath.Join(targetDir, ".bashrc"))
	require.NoError(t, err)
	require.NotZero(t, info.Mode()&os.ModeSymlink)
}

// --override replaces links owned by another stow package, but never links the
// stow directory does not own.
func TestStow_OverrideRequiresOwnership(t *testing.T) {
	stowDir, targetDir := setupStowTest(t)
	writeFile(t, filepath.Join(stowDir, "pkg1", ".zshrc"), "pkg1")
	writeFile(t, filepath.Join(stowDir, "pkg2", ".zshrc"), "pkg2")

	override := []*regexp.Regexp{regexp.MustCompile(".*")}

	_, err := Stow("pkg1", Options{StowDir: stowDir, TargetDir: targetDir})
	require.NoError(t, err)

	_, err = Stow("pkg2", Options{StowDir: stowDir, TargetDir: targetDir, Override: override})
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(targetDir, ".zshrc"))
	require.NoError(t, err)
	require.Equal(t, "pkg2", string(data))

	// A foreign symlink is not stow-owned, so override must not apply.
	foreign := filepath.Join(filepath.Dir(stowDir), "foreign")
	writeFile(t, foreign, "foreign")
	writeFile(t, filepath.Join(stowDir, "pkg2", ".foreign"), "pkg2 foreign")
	require.NoError(t, os.Symlink(foreign, filepath.Join(targetDir, ".foreign")))

	_, err = Stow("pkg2", Options{StowDir: stowDir, TargetDir: targetDir, Override: override})
	var conflictErr *ConflictError
	require.ErrorAs(t, err, &conflictErr)
	require.Contains(t, conflictErr.Error(), "existing target is not owned by stow: .foreign")

	link, err := os.Readlink(filepath.Join(targetDir, ".foreign"))
	require.NoError(t, err)
	require.Equal(t, foreign, link)
}

func TestStow_SkipAndIgnores(t *testing.T) {
	stowDir, targetDir := setupStowTest(t)
	writeFile(t, filepath.Join(stowDir, "user", ".bashrc"), "stow bashrc")
	writeFile(t, filepath.Join(stowDir, "user", ".git", "config"), "git")
	writeFile(t, filepath.Join(targetDir, ".bashrc"), "keep me")

	_, err := Stow("user", Options{
		StowDir:   stowDir,
		TargetDir: targetDir,
		Skip:      map[string]bool{".bashrc": true},
	})
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(targetDir, ".bashrc"))
	require.NoError(t, err)
	require.Equal(t, "keep me", string(data))

	_, err = os.Lstat(filepath.Join(targetDir, ".git"))
	require.True(t, errors.Is(err, os.ErrNotExist))
}

func TestUnstow(t *testing.T) {
	stowDir, targetDir := setupStowTest(t)
	writeFile(t, filepath.Join(stowDir, "user", ".config", "nvim", "init.lua"), "vim")

	_, err := Stow("user", Options{StowDir: stowDir, TargetDir: targetDir})
	require.NoError(t, err)

	_, err = Unstow("user", Options{StowDir: stowDir, TargetDir: targetDir})
	require.NoError(t, err)

	_, err = os.Lstat(filepath.Join(targetDir, ".config"))
	require.True(t, errors.Is(err, os.ErrNotExist))
}

// Unstowing an unfolded tree removes the links and prunes the now-empty
// directories it created, leaving unrelated target content untouched.
func TestUnstow_KeepsUnrelatedFiles(t *testing.T) {
	stowDir, targetDir := setupStowTest(t)
	writeFile(t, filepath.Join(stowDir, "pkg1", ".config", "app1", "conf"), "app1")
	writeFile(t, filepath.Join(stowDir, "pkg2", ".config", "app2", "conf"), "app2")
	writeFile(t, filepath.Join(targetDir, ".config", "unrelated"), "keep")

	_, err := Stow("pkg1", Options{StowDir: stowDir, TargetDir: targetDir})
	require.NoError(t, err)
	_, err = Stow("pkg2", Options{StowDir: stowDir, TargetDir: targetDir})
	require.NoError(t, err)

	_, err = Unstow("pkg1", Options{StowDir: stowDir, TargetDir: targetDir})
	require.NoError(t, err)

	_, err = os.Lstat(filepath.Join(targetDir, ".config", "app1"))
	require.True(t, errors.Is(err, os.ErrNotExist))

	// pkg2 and the unrelated file survive.
	_, err = os.Stat(filepath.Join(targetDir, ".config", "app2", "conf"))
	require.NoError(t, err)
	_, err = os.Stat(filepath.Join(targetDir, ".config", "unrelated"))
	require.NoError(t, err)

	_, err = Unstow("pkg2", Options{StowDir: stowDir, TargetDir: targetDir})
	require.NoError(t, err)

	// .config stays because it still contains "unrelated".
	info, err := os.Stat(filepath.Join(targetDir, ".config"))
	require.NoError(t, err)
	require.True(t, info.IsDir())
}

// Stowing a nested package unfolds a directory symlink created by a previous
// package, recursively.
func TestStow_NestedUnfolding(t *testing.T) {
	stowDir, targetDir := setupStowTest(t)
	writeFile(t, filepath.Join(stowDir, "pkg1", ".config", "app1", "conf"), "app1")
	writeFile(t, filepath.Join(stowDir, "pkg2", ".config", "app1", "extra"), "extra")

	_, err := Stow("pkg1", Options{StowDir: stowDir, TargetDir: targetDir})
	require.NoError(t, err)
	_, err = Stow("pkg2", Options{StowDir: stowDir, TargetDir: targetDir})
	require.NoError(t, err)

	app1 := filepath.Join(targetDir, ".config", "app1")
	info, err := os.Lstat(app1)
	require.NoError(t, err)
	require.True(t, info.IsDir(), "app1 should be unfolded into a real directory")

	for name, want := range map[string]string{"conf": "app1", "extra": "extra"} {
		data, err := os.ReadFile(filepath.Join(app1, name))
		require.NoError(t, err)
		require.Equal(t, want, string(data))
	}
}

func TestStow_MissingPackage(t *testing.T) {
	stowDir, targetDir := setupStowTest(t)
	_, err := Stow("nope", Options{StowDir: stowDir, TargetDir: targetDir})
	require.ErrorContains(t, err, "not accessible")
}

func TestStow_VerboseWriter(t *testing.T) {
	stowDir, targetDir := setupStowTest(t)
	writeFile(t, filepath.Join(stowDir, "user", ".bashrc"), "x")

	var buf bytes.Buffer
	_, err := Stow("user", Options{
		StowDir:   stowDir,
		TargetDir: targetDir,
		Verbose:   true,
		Writer:    &buf,
	})
	require.NoError(t, err)
	require.Contains(t, buf.String(), "LINK:")
}

func TestUnstow_Simulate(t *testing.T) {
	stowDir, targetDir := setupStowTest(t)
	writeFile(t, filepath.Join(stowDir, "user", ".bashrc"), "x")

	_, err := Stow("user", Options{StowDir: stowDir, TargetDir: targetDir})
	require.NoError(t, err)

	plan, err := Unstow("user", Options{StowDir: stowDir, TargetDir: targetDir, Simulate: true})
	require.NoError(t, err)
	require.Len(t, plan.Actions, 1)
	require.Equal(t, ActionRemove, plan.Actions[0].Kind)

	// The link is still there because the run was simulated.
	_, err = os.Lstat(filepath.Join(targetDir, ".bashrc"))
	require.NoError(t, err)
}

func TestConflictErrorMessage(t *testing.T) {
	err := &ConflictError{
		Package: "user",
		Conflicts: []Conflict{
			{RelPath: ".bashrc", Reason: "existing target is not owned by stow: .bashrc"},
		},
	}
	msg := err.Error()
	require.Contains(t, msg, "stowing user would cause conflicts")
	require.Contains(t, msg, "existing target is not owned by stow: .bashrc")
	require.Contains(t, msg, "All operations aborted")
}

func TestStow_ConflictReasonOtherPackage(t *testing.T) {
	t.Run("file over file", func(t *testing.T) {
		stowDir, targetDir := setupStowTest(t)
		writeFile(t, filepath.Join(stowDir, "pkg1", "x"), "one")
		writeFile(t, filepath.Join(stowDir, "pkg2", "x"), "two")

		_, err := Stow("pkg1", Options{StowDir: stowDir, TargetDir: targetDir})
		require.NoError(t, err)

		_, err = Stow("pkg2", Options{StowDir: stowDir, TargetDir: targetDir})
		var conflictErr *ConflictError
		require.ErrorAs(t, err, &conflictErr)
		require.Contains(t, conflictErr.Error(), "existing target is stowed to a different package: x =>")
	})

	t.Run("directory over file", func(t *testing.T) {
		stowDir, targetDir := setupStowTest(t)
		writeFile(t, filepath.Join(stowDir, "pkg1", "x"), "one")
		writeFile(t, filepath.Join(stowDir, "pkg2", "x", "y"), "two")

		_, err := Stow("pkg1", Options{StowDir: stowDir, TargetDir: targetDir})
		require.NoError(t, err)

		_, err = Stow("pkg2", Options{StowDir: stowDir, TargetDir: targetDir})
		var conflictErr *ConflictError
		require.ErrorAs(t, err, &conflictErr)
		require.NotContains(t, conflictErr.Error(), "not owned by stow")
		require.Contains(t, conflictErr.Error(), "existing target is stowed to a different package: x =>")
	})
}

// Relative StowDir and TargetDir must behave like absolute ones: ownership
// checks and link comparisons require normalised paths.
func TestStow_RelativePaths(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "dotfiles", "pkg1", ".config", "app1", "conf"), "app1")
	writeFile(t, filepath.Join(root, "dotfiles", "pkg2", ".config", "app2", "conf"), "app2")
	home := filepath.Join(root, "home")
	require.NoError(t, os.MkdirAll(home, 0755))

	originalWd, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(originalWd) })
	require.NoError(t, os.Chdir(root))

	// Relative stow dir with an absolute target (previously misdetected
	// ownership and reported a spurious conflict).
	_, err := Stow("pkg1", Options{StowDir: "dotfiles", TargetDir: home})
	require.NoError(t, err)
	_, err = Stow("pkg2", Options{StowDir: "dotfiles", TargetDir: home})
	require.NoError(t, err)

	info, err := os.Lstat(filepath.Join(home, ".config"))
	require.NoError(t, err)
	require.True(t, info.IsDir(), ".config should be unfolded")

	// Relative stow dir with the default target (the stow dir's parent).
	_, err = Stow("pkg2", Options{StowDir: filepath.Join(root, "dotfiles")})
	require.NoError(t, err)
}

// The default target directory is the parent of the stow directory, not the
// stow directory itself.
func TestStow_DefaultTargetDir(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "dotfiles", "pkg", "file"), "x")

	originalWd, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(originalWd) })
	require.NoError(t, os.Chdir(root))

	_, err := Stow("pkg", Options{StowDir: "dotfiles"})
	require.NoError(t, err)

	info, err := os.Lstat(filepath.Join(root, "file"))
	require.NoError(t, err)
	require.NotZero(t, info.Mode()&os.ModeSymlink)
}

func TestUnstow_Refolds(t *testing.T) {
	stowDir, targetDir := setupStowTest(t)
	writeFile(t, filepath.Join(stowDir, "pkg1", ".config", "app1", "conf"), "app1")
	writeFile(t, filepath.Join(stowDir, "pkg2", ".config", "app2", "conf"), "app2")

	_, err := Stow("pkg1", Options{StowDir: stowDir, TargetDir: targetDir})
	require.NoError(t, err)
	_, err = Stow("pkg2", Options{StowDir: stowDir, TargetDir: targetDir})
	require.NoError(t, err)

	_, err = Unstow("pkg1", Options{StowDir: stowDir, TargetDir: targetDir})
	require.NoError(t, err)

	cfg := filepath.Join(targetDir, ".config")
	info, err := os.Lstat(cfg)
	require.NoError(t, err)
	require.NotZero(t, info.Mode()&os.ModeSymlink, ".config should fold back into a symlink")

	data, err := os.ReadFile(filepath.Join(cfg, "app2", "conf"))
	require.NoError(t, err)
	require.Equal(t, "app2", string(data))
}

// copyAndRemove preserves content and permissions when rename is not possible.
func TestCopyAndRemove(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	dst := filepath.Join(dir, "dst")
	require.NoError(t, os.WriteFile(src, []byte("payload"), 0640))
	require.NoError(t, os.Chmod(src, 0640))

	require.NoError(t, copyAndRemove(src, dst))

	data, err := os.ReadFile(dst)
	require.NoError(t, err)
	require.Equal(t, "payload", string(data))

	info, err := os.Stat(dst)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0640), info.Mode().Perm())

	_, err = os.Stat(src)
	require.True(t, errors.Is(err, os.ErrNotExist))
}

// The built-in ignore list matches GNU Stow: VCS metadata, editor backups and
// autosave files, README/LICENSE/COPYING at the package root, etc.
func TestStow_DefaultIgnorePatterns(t *testing.T) {
	stowDir, targetDir := setupStowTest(t)
	pkg := filepath.Join(stowDir, "user")
	for _, name := range []string{
		"README.md", "LICENSE", "COPYING", ".bashrc",
		".bashrc~", "#emacs#", ".#lock", ".cvsignore",
		".git/config", ".config/app/conf", ".config/app/conf~",
	} {
		writeFile(t, filepath.Join(pkg, name), "x")
	}
	// Pre-create the nested target dirs so the port traverses instead of folding.
	require.NoError(t, os.MkdirAll(filepath.Join(targetDir, ".config", "app"), 0755))

	_, err := Stow("user", Options{StowDir: stowDir, TargetDir: targetDir})
	require.NoError(t, err)

	for _, name := range []string{
		"README.md", "LICENSE", "COPYING", ".bashrc~", "#emacs#", ".#lock",
		".cvsignore", ".git", ".config/app/conf~",
	} {
		_, err := os.Lstat(filepath.Join(targetDir, name))
		require.True(t, errors.Is(err, os.ErrNotExist), "%s should be ignored", name)
	}

	for _, name := range []string{".bashrc", ".config/app/conf"} {
		_, err := os.Lstat(filepath.Join(targetDir, name))
		require.NoError(t, err, "%s should be stowed", name)
	}
}

// A package-local ignore file replaces the built-in list, as in GNU Stow.
func TestStow_LocalIgnoreFile(t *testing.T) {
	stowDir, targetDir := setupStowTest(t)
	pkg := filepath.Join(stowDir, "user")
	writeFile(t, filepath.Join(pkg, ".stow-local-ignore"), "secret   # inline comment\n# a comment\n")
	writeFile(t, filepath.Join(pkg, "secret"), "x")
	writeFile(t, filepath.Join(pkg, "README.md"), "x")

	_, err := Stow("user", Options{StowDir: stowDir, TargetDir: targetDir})
	require.NoError(t, err)

	_, err = os.Lstat(filepath.Join(targetDir, "secret"))
	require.True(t, errors.Is(err, os.ErrNotExist))

	_, err = os.Lstat(filepath.Join(targetDir, ".stow-local-ignore"))
	require.True(t, errors.Is(err, os.ErrNotExist))

	// README is in the built-in list, but the local file replaces it.
	_, err = os.Lstat(filepath.Join(targetDir, "README.md"))
	require.NoError(t, err)
}

// A link owned by the stow directory whose destination disappeared is stale;
// GNU Stow replaces it instead of reporting a conflict.
func TestStow_DanglingOwnedLinkReplaced(t *testing.T) {
	stowDir, targetDir := setupStowTest(t)
	writeFile(t, filepath.Join(stowDir, "user", ".config2"), "new")

	require.NoError(t, os.Symlink("../dotfiles/user/.oldname", filepath.Join(targetDir, ".config2")))

	_, err := Stow("user", Options{StowDir: stowDir, TargetDir: targetDir})
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(targetDir, ".config2"))
	require.NoError(t, err)
	require.Equal(t, "new", string(data))
}

// Directories symlinked inside a package are traversed, matching GNU Stow's
// -d (follow symlinks) semantics.
func TestStow_DirectorySymlinkFollowed(t *testing.T) {
	stowDir, targetDir := setupStowTest(t)
	writeFile(t, filepath.Join(stowDir, "shared", "nvim", "init.lua"), "lua")
	require.NoError(t, os.MkdirAll(filepath.Join(stowDir, "user", ".config"), 0755))
	require.NoError(t, os.Symlink("../../shared/nvim", filepath.Join(stowDir, "user", ".config", "nvim")))
	require.NoError(t, os.MkdirAll(filepath.Join(targetDir, ".config", "nvim"), 0755))

	_, err := Stow("user", Options{StowDir: stowDir, TargetDir: targetDir})
	require.NoError(t, err)

	info, err := os.Lstat(filepath.Join(targetDir, ".config", "nvim"))
	require.NoError(t, err)
	require.True(t, info.IsDir())

	data, err := os.ReadFile(filepath.Join(targetDir, ".config", "nvim", "init.lua"))
	require.NoError(t, err)
	require.Equal(t, "lua", string(data))
}

// Absolute symlinks in a package are host-specific and rejected by GNU Stow.
func TestStow_AbsoluteSymlinkConflict(t *testing.T) {
	stowDir, targetDir := setupStowTest(t)
	writeFile(t, filepath.Join(stowDir, "user", ".bashrc"), "x")
	abs := filepath.Join(filepath.Dir(stowDir), "absolute")
	writeFile(t, abs, "x")
	require.NoError(t, os.Symlink(abs, filepath.Join(stowDir, "user", ".link")))

	_, err := Stow("user", Options{StowDir: stowDir, TargetDir: targetDir})
	var conflictErr *ConflictError
	require.ErrorAs(t, err, &conflictErr)
	require.Contains(t, conflictErr.Error(), "absolute symlink")

	_, err = os.Lstat(filepath.Join(targetDir, ".link"))
	require.True(t, errors.Is(err, os.ErrNotExist))
}

func TestStow_SkipsProtectedTargets(t *testing.T) {
	t.Run("stow directory", func(t *testing.T) {
		root := t.TempDir()
		stowDir := filepath.Join(root, "dotfiles")
		writeFile(t, filepath.Join(stowDir, "user", "dotfiles", "notes"), "x")

		_, err := Stow("user", Options{StowDir: stowDir, TargetDir: root})
		require.NoError(t, err)

		_, err = os.Lstat(filepath.Join(stowDir, "dotfiles"))
		require.True(t, errors.Is(err, os.ErrNotExist), "must not write inside the stow directory")
	})

	t.Run("nonstow marker", func(t *testing.T) {
		stowDir, targetDir := setupStowTest(t)
		writeFile(t, filepath.Join(stowDir, "user", "protected", "notes"), "x")
		writeFile(t, filepath.Join(targetDir, "protected", ".nonstow"), "")

		_, err := Stow("user", Options{StowDir: stowDir, TargetDir: targetDir})
		require.NoError(t, err)

		_, err = os.Lstat(filepath.Join(targetDir, "protected", "notes"))
		require.True(t, errors.Is(err, os.ErrNotExist))
	})
}

func TestStow_PackageNameValidation(t *testing.T) {
	stowDir, targetDir := setupStowTest(t)
	for _, pkg := range []string{"", "..", "../outside"} {
		_, err := Stow(pkg, Options{StowDir: stowDir, TargetDir: targetDir})
		require.Error(t, err, "package %q should be rejected", pkg)
	}
}

// Unstow never removes directories it did not create, even when they end up
// empty. GNU Stow leaves pre-existing directories alone.
func TestUnstow_KeepsPreExistingDirectories(t *testing.T) {
	stowDir, targetDir := setupStowTest(t)
	require.NoError(t, os.MkdirAll(filepath.Join(stowDir, "user", "keep"), 0755))
	require.NoError(t, os.MkdirAll(filepath.Join(targetDir, "keep"), 0755))

	_, err := Stow("user", Options{StowDir: stowDir, TargetDir: targetDir})
	require.NoError(t, err)
	_, err = Unstow("user", Options{StowDir: stowDir, TargetDir: targetDir})
	require.NoError(t, err)

	info, err := os.Stat(filepath.Join(targetDir, "keep"))
	require.NoError(t, err)
	require.True(t, info.IsDir())
}

func TestActionString(t *testing.T) {
	tests := []struct {
		action Action
		want   string
	}{
		{Action{Kind: ActionLink, Target: "/t", Source: "../s"}, "LINK: /t => ../s"},
		{Action{Kind: ActionRemove, Target: "/t"}, "UNLINK: /t"},
		{Action{Kind: ActionAdopt, Target: "/t", Source: "/s"}, "ADOPT: /t -> /s"},
		{Action{Kind: ActionUnfold, Target: "/t"}, "UNFOLD: /t"},
		{Action{Kind: "CUSTOM", Target: "/t"}, "CUSTOM: /t"},
	}
	for _, tc := range tests {
		require.Equal(t, tc.want, tc.action.String())
	}
}

// PlanStow returns the same plan as Stow without touching the filesystem.
func TestPlanStow(t *testing.T) {
	stowDir, targetDir := setupStowTest(t)
	writeFile(t, filepath.Join(stowDir, "user", ".bashrc"), "x")

	plan, err := PlanStow("user", Options{StowDir: stowDir, TargetDir: targetDir})
	require.NoError(t, err)
	require.Len(t, plan.Actions, 1)
	require.Empty(t, plan.Conflicts)

	_, err = os.Lstat(filepath.Join(targetDir, ".bashrc"))
	require.True(t, errors.Is(err, os.ErrNotExist))
}

func TestStow_ConflictMessages(t *testing.T) {
	t.Run("directory over file", func(t *testing.T) {
		stowDir, targetDir := setupStowTest(t)
		writeFile(t, filepath.Join(stowDir, "user", "dir", "child"), "x")
		writeFile(t, filepath.Join(targetDir, "dir"), "x")

		_, err := Stow("user", Options{StowDir: stowDir, TargetDir: targetDir})
		var conflictErr *ConflictError
		require.ErrorAs(t, err, &conflictErr)
		require.Contains(t, conflictErr.Error(), "cannot stow user/dir over existing target dir")
	})

	t.Run("file over directory", func(t *testing.T) {
		stowDir, targetDir := setupStowTest(t)
		writeFile(t, filepath.Join(stowDir, "user", "bin"), "x")
		require.NoError(t, os.MkdirAll(filepath.Join(targetDir, "bin"), 0755))

		_, err := Stow("user", Options{StowDir: stowDir, TargetDir: targetDir})
		var conflictErr *ConflictError
		require.ErrorAs(t, err, &conflictErr)
		require.Contains(t, conflictErr.Error(), "cannot stow non-directory user/bin over existing directory bin")
	})
}

// refold only folds when every entry is a link into one package directory.
func TestRefoldGuards(t *testing.T) {
	stowDir, targetDir := setupStowTest(t)
	writeFile(t, filepath.Join(stowDir, "user", "app"), "app")
	writeFile(t, filepath.Join(stowDir, "other", "app"), "other")

	s, err := newStower("user", Options{StowDir: stowDir, TargetDir: targetDir})
	require.NoError(t, err)

	setup := func(name string) string {
		dir := filepath.Join(targetDir, name)
		require.NoError(t, os.MkdirAll(dir, 0755))
		return dir
	}

	t.Run("real file present", func(t *testing.T) {
		dir := setup(".config")
		writeFile(t, filepath.Join(dir, "real"), "x")
		require.NoError(t, os.Symlink(filepath.Join(stowDir, "user", "app"), filepath.Join(dir, "app")))

		require.NoError(t, s.refold(dir))
		info, err := os.Lstat(dir)
		require.NoError(t, err)
		require.True(t, info.IsDir(), "must not fold over real files")
	})

	t.Run("links from two packages", func(t *testing.T) {
		dir := setup(".config2")
		require.NoError(t, os.Symlink(filepath.Join(stowDir, "user", "app"), filepath.Join(dir, "a")))
		require.NoError(t, os.Symlink(filepath.Join(stowDir, "other", "app"), filepath.Join(dir, "b")))

		require.NoError(t, s.refold(dir))
		info, err := os.Lstat(dir)
		require.NoError(t, err)
		require.True(t, info.IsDir(), "must not fold links from different packages")
	})

	t.Run("link outside stow dir", func(t *testing.T) {
		dir := setup(".config3")
		writeFile(t, filepath.Join(targetDir, "outside"), "x")
		require.NoError(t, os.Symlink(filepath.Join(targetDir, "outside"), filepath.Join(dir, "a")))

		require.NoError(t, s.refold(dir))
		info, err := os.Lstat(dir)
		require.NoError(t, err)
		require.True(t, info.IsDir(), "must not fold foreign links")
	})
}

func TestReadlinkResolved(t *testing.T) {
	dir := t.TempDir()
	realFile := filepath.Join(dir, "real")
	writeFile(t, realFile, "x")
	link := filepath.Join(dir, "link")
	require.NoError(t, os.Symlink("real", link))

	v := &vfs{nodes: make(map[string]vnode)}

	got, err := v.readlinkResolved(link)
	require.NoError(t, err)
	require.Equal(t, realFile, got)

	planned := filepath.Join(dir, "planned")
	v.set(planned, vnode{kind: nodeLink, src: "/source"})
	got, err = v.readlinkResolved(planned)
	require.NoError(t, err)
	require.Equal(t, "/source", got)

	plannedDir := filepath.Join(dir, "pdir")
	v.set(plannedDir, vnode{kind: nodeDir})
	_, err = v.readlinkResolved(filepath.Join(plannedDir, "child"))
	require.Error(t, err)
	_, err = v.readlinkResolved(plannedDir)
	require.Error(t, err)

	_, err = v.readlinkResolved(realFile)
	require.Error(t, err, "a regular file is not a symlink")
}

func TestExecuteActionsErrors(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "missing")

	require.Error(t, executeActions([]Action{{Kind: ActionRemove, Target: missing}}))
	require.Error(t, executeActions([]Action{{Kind: ActionUnfold, Target: missing}}))
	require.Error(t, executeActions([]Action{{Kind: ActionAdopt, Target: missing, Source: filepath.Join(dir, "src")}}))

	taken := filepath.Join(dir, "taken")
	writeFile(t, taken, "x")
	err := executeActions([]Action{{Kind: ActionLink, Target: taken, Source: "elsewhere"}})
	require.Error(t, err)
}

func TestMoveHelpersErrors(t *testing.T) {
	dir := t.TempDir()
	require.Error(t, moveFile(filepath.Join(dir, "missing"), filepath.Join(dir, "dst")))
	require.Error(t, copyAndRemove(filepath.Join(dir, "missing"), filepath.Join(dir, "dst")))

	src := filepath.Join(dir, "src")
	writeFile(t, src, "x")
	require.Error(t, copyAndRemove(src, filepath.Join(dir, "no-such-dir", "dst")))
}

// Invalid ignore patterns are reported instead of being silently ignored.
func TestIgnorePatternError(t *testing.T) {
	stowDir, targetDir := setupStowTest(t)
	writeFile(t, filepath.Join(stowDir, "user", ".stow-local-ignore"), "(\n")

	_, err := PlanStow("user", Options{StowDir: stowDir, TargetDir: targetDir})
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid stow ignore pattern")
}

func TestVFSLstat(t *testing.T) {
	dir := t.TempDir()
	realFile := filepath.Join(dir, "real")
	writeFile(t, realFile, "x")

	v := &vfs{nodes: make(map[string]vnode)}
	v.set(filepath.Join(dir, "pdir"), vnode{kind: nodeDir})
	v.set(filepath.Join(dir, "plink"), vnode{kind: nodeLink, src: dir})

	got := v.lstat(realFile)
	require.True(t, got.exists)
	require.False(t, got.isDir)
	require.False(t, got.isSymlink)

	require.False(t, v.lstat(filepath.Join(dir, "missing")).exists)

	got = v.lstat(filepath.Join(dir, "pdir"))
	require.True(t, got.exists)
	require.True(t, got.isDir)

	got = v.lstat(filepath.Join(dir, "plink"))
	require.True(t, got.exists)
	require.True(t, got.isSymlink)

	require.False(t, v.lstat(filepath.Join(dir, "pdir", "child")).exists)

	got = v.lstat(filepath.Join(dir, "plink", "real"))
	require.True(t, got.exists)
	require.False(t, got.isDir)
}

func TestLinkDestination(t *testing.T) {
	stowDir, targetDir := setupStowTest(t)
	writeFile(t, filepath.Join(stowDir, "user", "file"), "x")
	s, err := newStower("user", Options{StowDir: stowDir, TargetDir: targetDir})
	require.NoError(t, err)

	planned := filepath.Join(targetDir, "planned")
	s.fs.set(planned, vnode{kind: nodeLink, src: filepath.Join(stowDir, "user", "file")})
	require.Equal(t, "../dotfiles/user/file", s.linkDestination(planned))

	realLink := filepath.Join(targetDir, "real")
	require.NoError(t, os.Symlink("../dotfiles/user/file", realLink))
	require.Equal(t, "../dotfiles/user/file", s.linkDestination(realLink))

	regular := filepath.Join(targetDir, "regular")
	writeFile(t, regular, "x")
	require.Empty(t, s.linkDestination(regular))
}

func TestIsOwnedByStowAndPointsTo(t *testing.T) {
	stowDir, targetDir := setupStowTest(t)
	writeFile(t, filepath.Join(stowDir, "user", "file"), "x")
	s, err := newStower("user", Options{StowDir: stowDir, TargetDir: targetDir})
	require.NoError(t, err)

	own := filepath.Join(targetDir, "own")
	require.NoError(t, os.Symlink(filepath.Join(stowDir, "user", "file"), own))
	require.True(t, s.isOwnedByStow(own))
	require.True(t, s.pointsTo(own, filepath.Join(stowDir, "user", "file")))
	require.False(t, s.pointsTo(own, filepath.Join(stowDir, "user", "other")))

	outside := filepath.Join(targetDir, "outside")
	writeFile(t, outside, "x")
	foreign := filepath.Join(targetDir, "foreign")
	require.NoError(t, os.Symlink(outside, foreign))
	require.False(t, s.isOwnedByStow(foreign))

	regular := filepath.Join(targetDir, "regular")
	writeFile(t, regular, "x")
	require.False(t, s.isOwnedByStow(regular))
	require.False(t, s.pointsTo(regular, filepath.Join(stowDir, "user", "file")))
}

func TestResolveLinkFromDiskError(t *testing.T) {
	dir := t.TempDir()
	regular := filepath.Join(dir, "regular")
	writeFile(t, regular, "x")

	_, err := resolveLinkFromDisk(regular)
	require.Error(t, err)
}

// Missing packages are reported by every entry point.
func TestMissingPackage(t *testing.T) {
	stowDir, targetDir := setupStowTest(t)
	opts := Options{StowDir: stowDir, TargetDir: targetDir}

	_, err := PlanStow("nope", opts)
	require.Error(t, err)
	_, err = Stow("nope", opts)
	require.Error(t, err)
	_, err = Unstow("nope", opts)
	require.Error(t, err)
}

func TestExecuteActionsAdoptMkdirError(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "blocker")
	writeFile(t, blocker, "x")
	target := filepath.Join(dir, "target")
	writeFile(t, target, "y")

	err := executeActions([]Action{{
		Kind:   ActionAdopt,
		Target: target,
		Source: filepath.Join(blocker, "child", "file"),
	}})
	require.Error(t, err)
}

func TestCopyAndRemoveDirectorySource(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "dir")
	require.NoError(t, os.MkdirAll(src, 0755))

	require.Error(t, copyAndRemove(src, filepath.Join(dir, "dst")))
}

func TestHomeDirFallback(t *testing.T) {
	t.Setenv("HOME", "")
	_, err := homeDir()
	require.Error(t, err, "without $HOME there is no home directory")
}

func TestNormalizeOptionsVerboseWriter(t *testing.T) {
	opts := normalizeOptions(Options{Verbose: true})
	require.Equal(t, os.Stdout, opts.Writer)

	opts = normalizeOptions(Options{})
	require.Nil(t, opts.Writer)
}

func TestPathInsideRelative(t *testing.T) {
	require.False(t, pathInside("relative", "/absolute"))
	require.True(t, pathInside("/a/b", "/a/b"))
	require.True(t, pathInside("/a/b", "/a/b/c"))
	require.False(t, pathInside("/a/b", "/a/c"))
}

func TestLstatDanglingPlannedLink(t *testing.T) {
	dir := t.TempDir()
	v := &vfs{nodes: make(map[string]vnode)}
	link := filepath.Join(dir, "link")
	v.set(link, vnode{kind: nodeLink, src: filepath.Join(dir, "missing")})

	require.False(t, v.lstat(filepath.Join(link, "child")).exists)
}

// A failed execution is reported to the caller.
func TestStowExecuteError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory permissions")
	}

	stowDir, targetDir := setupStowTest(t)
	writeFile(t, filepath.Join(stowDir, "user", ".ro", "file"), "x")

	ro := filepath.Join(targetDir, ".ro")
	require.NoError(t, os.MkdirAll(ro, 0755))
	require.NoError(t, os.Chmod(ro, 0500))
	t.Cleanup(func() { _ = os.Chmod(ro, 0755) })

	_, err := Stow("user", Options{StowDir: stowDir, TargetDir: targetDir})
	require.Error(t, err)
}
