package stow

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// snapshot records the type of every path under dir. Symlinks are recorded with
// their resolved destination relative to dir, so trees can be compared.
func snapshot(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := make(map[string]string)
	require.NoError(t, filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if path == dir {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			resolved, err := filepath.EvalSymlinks(path)
			if err != nil {
				out[rel] = "broken-link"
				return nil
			}
			relResolved, err := filepath.Rel(dir, resolved)
			if err != nil {
				return err
			}
			out[rel] = "link->" + filepath.ToSlash(relResolved)
			return nil
		}
		if info.IsDir() {
			out[rel] = "dir"
			return nil
		}
		out[rel] = "file"
		return nil
	}))
	return out
}

func keys(m map[string]string) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// TestParityWithGNUStow compares the resulting trees of this package and the
// real GNU Stow binary for the same inputs. It is skipped when stow is missing.
func TestParityWithGNUStow(t *testing.T) {
	gnuStow, err := exec.LookPath("stow")
	if err != nil {
		t.Skip("GNU stow is not installed; skipping parity test")
	}

	layout := map[string]string{
		"pkg1/.bashrc":                  "bashrc",
		"pkg1/.config/app1/conf":        "app1",
		"pkg1/.local/share/app1/data":   "data1",
		"pkg2/.config/app2/conf":        "app2",
		"pkg2/.local/share/app2/data":   "data2",
		"pkg2/.config/app1/shared.conf": "shared",
	}
	order := []string{"pkg1", "pkg2"}

	loop := func(t *testing.T, stowDir string, lay map[string]string, seedTarget func(home string), steps []string) {
		t.Helper()
		root := t.TempDir()
		for path, content := range lay {
			full := filepath.Join(stowDir, path)
			require.NoError(t, os.MkdirAll(filepath.Dir(full), 0755))
			require.NoError(t, os.WriteFile(full, []byte(content), 0644))
		}

		oursHome := filepath.Join(root, "ours")
		gnuHome := filepath.Join(root, "gnu")
		require.NoError(t, os.MkdirAll(oursHome, 0755))
		require.NoError(t, os.MkdirAll(gnuHome, 0755))
		if seedTarget != nil {
			seedTarget(oursHome)
			seedTarget(gnuHome)
		}

		for _, step := range steps {
			unstow := strings.HasPrefix(step, "-D ")
			pkg := strings.TrimPrefix(step, "-D ")
			opts := Options{StowDir: stowDir, TargetDir: oursHome}
			if unstow {
				_, err := Unstow(pkg, opts)
				require.NoError(t, err)
			} else {
				_, err := Stow(pkg, opts)
				require.NoError(t, err)
			}

			args := []string{"-d", stowDir, "-t", gnuHome}
			if unstow {
				args = append(args, "-D")
			}
			out, err := exec.Command(gnuStow, append(args, pkg)...).CombinedOutput()
			require.NoError(t, err, "GNU stow failed: %s", out)
		}

		ours := snapshot(t, oursHome)
		gnu := snapshot(t, gnuHome)
		require.Equal(t, keys(gnu), keys(ours))
		for _, k := range keys(gnu) {
			require.Equal(t, gnu[k], ours[k], "mismatch at %s", k)
		}
	}

	t.Run("fold-and-unfold", func(t *testing.T) {
		root := t.TempDir()
		loop(t, filepath.Join(root, "dotfiles"), layout, nil, order)
	})

	t.Run("pre-existing-directories", func(t *testing.T) {
		root := t.TempDir()
		loop(t, filepath.Join(root, "dotfiles"), layout, func(home string) {
			require.NoError(t, os.MkdirAll(filepath.Join(home, ".config"), 0755))
			require.NoError(t, os.WriteFile(filepath.Join(home, ".config", "unrelated"), []byte("keep"), 0644))
			require.NoError(t, os.MkdirAll(filepath.Join(home, ".local", "share"), 0755))
		}, order)
	})

	t.Run("unstow-refolds", func(t *testing.T) {
		root := t.TempDir()
		loop(t, filepath.Join(root, "dotfiles"), layout, nil, []string{"pkg1", "pkg2", "-D pkg1"})
	})

	t.Run("default-ignores", func(t *testing.T) {
		root := t.TempDir()
		ignoreLayout := map[string]string{
			"pkg1/README.md":        "readme",
			"pkg1/LICENSE":          "license",
			"pkg1/COPYING":          "copying",
			"pkg1/.bashrc":          "bashrc",
			"pkg1/.bashrc~":         "backup",
			"pkg1/#emacs#":          "autosave",
			"pkg1/.#lock":           "lock",
			"pkg1/.cvsignore":       "cvs",
			"pkg1/.git/config":      "git",
			"pkg1/.config/app/conf": "conf",
			"pkg1/.config/app/bak~": "bak",
		}
		loop(t, filepath.Join(root, "dotfiles"), ignoreLayout, func(home string) {
			require.NoError(t, os.MkdirAll(filepath.Join(home, ".config", "app"), 0755))
		}, []string{"pkg1"})
	})

	t.Run("local-ignore-file", func(t *testing.T) {
		root := t.TempDir()
		localLayout := map[string]string{
			"pkg1/.stow-local-ignore": "secret\n",
			"pkg1/secret":             "secret",
			"pkg1/README.md":          "readme",
			"pkg1/.bashrc":            "bashrc",
		}
		loop(t, filepath.Join(root, "dotfiles"), localLayout, nil, []string{"pkg1"})
	})

	t.Run("directory-symlink-in-package", func(t *testing.T) {
		root := t.TempDir()
		stowDir := filepath.Join(root, "dotfiles")
		require.NoError(t, os.MkdirAll(filepath.Join(stowDir, "shared", "nvim"), 0755))
		require.NoError(t, os.WriteFile(filepath.Join(stowDir, "shared", "nvim", "init.lua"), []byte("lua"), 0644))
		require.NoError(t, os.MkdirAll(filepath.Join(stowDir, "pkg", ".config"), 0755))
		require.NoError(t, os.Symlink("../../shared/nvim", filepath.Join(stowDir, "pkg", ".config", "nvim")))

		oursHome := filepath.Join(root, "ours")
		gnuHome := filepath.Join(root, "gnu")
		for _, home := range []string{oursHome, gnuHome} {
			require.NoError(t, os.MkdirAll(filepath.Join(home, ".config", "nvim"), 0755))
		}

		_, err := Stow("pkg", Options{StowDir: stowDir, TargetDir: oursHome})
		require.NoError(t, err)

		out, err := exec.Command(gnuStow, "-d", stowDir, "-t", gnuHome, "pkg").CombinedOutput()
		require.NoError(t, err, "GNU stow failed: %s", out)

		require.Equal(t, snapshot(t, gnuHome), snapshot(t, oursHome))
	})

	t.Run("unstow-keeps-pre-existing-empty-dir", func(t *testing.T) {
		root := t.TempDir()
		stowDir := filepath.Join(root, "dotfiles")
		require.NoError(t, os.MkdirAll(filepath.Join(stowDir, "pkg", "empty"), 0755))

		oursHome := filepath.Join(root, "ours")
		gnuHome := filepath.Join(root, "gnu")
		for _, home := range []string{oursHome, gnuHome} {
			require.NoError(t, os.MkdirAll(filepath.Join(home, "empty"), 0755))
		}

		_, err := Stow("pkg", Options{StowDir: stowDir, TargetDir: oursHome})
		require.NoError(t, err)
		out, err := exec.Command(gnuStow, "-d", stowDir, "-t", gnuHome, "pkg").CombinedOutput()
		require.NoError(t, err, "GNU stow failed: %s", out)

		_, err = Unstow("pkg", Options{StowDir: stowDir, TargetDir: oursHome})
		require.NoError(t, err)
		out, err = exec.Command(gnuStow, "-D", "-d", stowDir, "-t", gnuHome, "pkg").CombinedOutput()
		require.NoError(t, err, "GNU unstow failed: %s", out)

		require.Equal(t, snapshot(t, gnuHome), snapshot(t, oursHome))
	})

	t.Run("adopt", func(t *testing.T) {
		root := t.TempDir()

		// Adoption mutates the stow tree, so give each implementation its own
		// directory pair. Both pairs mirror the same layout so the resulting
		// target trees are directly comparable.
		setup := func(name string) (stowDir, homeDir string) {
			stowDir = filepath.Join(root, name, "dotfiles")
			homeDir = filepath.Join(root, name, "home")
			require.NoError(t, os.MkdirAll(filepath.Join(stowDir, "pkg"), 0755))
			require.NoError(t, os.WriteFile(filepath.Join(stowDir, "pkg", ".bashrc"), []byte("stow"), 0644))
			require.NoError(t, os.MkdirAll(homeDir, 0755))
			require.NoError(t, os.WriteFile(filepath.Join(homeDir, ".bashrc"), []byte("home"), 0644))
			return stowDir, homeDir
		}

		oursStow, oursHome := setup("ours")
		gnuStowDir, gnuHome := setup("gnu")

		_, err := Stow("pkg", Options{StowDir: oursStow, TargetDir: oursHome, Adopt: true})
		require.NoError(t, err)

		out, err := exec.Command(gnuStow, "--adopt", "-d", gnuStowDir, "-t", gnuHome, "pkg").CombinedOutput()
		require.NoError(t, err, "GNU stow failed: %s", out)

		require.Equal(t, snapshot(t, gnuHome), snapshot(t, oursHome))

		oursData, err := os.ReadFile(filepath.Join(oursStow, "pkg", ".bashrc"))
		require.NoError(t, err)
		gnuData, err := os.ReadFile(filepath.Join(gnuStowDir, "pkg", ".bashrc"))
		require.NoError(t, err)
		require.Equal(t, string(gnuData), string(oursData))
	})
}
