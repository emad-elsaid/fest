// Package fd implements the subset of fd that fest uses: walking a directory
// tree without following symlinks and returning the absolute paths of every
// symlink, while skipping excluded directories. It replaces the external fd
// binary.
package fd

import (
	"io/fs"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

// Symlinks returns the absolute paths of all symlinks under root. Excludes are
// glob patterns matched against each entry's base name; patterns containing a
// slash are matched against the entry's path relative to root. Matching
// directories are not descended into.
//
// Unlike fd's default behaviour, hidden entries are included: fest uses this to
// find broken dotfile symlinks, which almost always live in hidden paths.
func Symlinks(root string, excludes []string) ([]string, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}

	// A symlinked root (for example a symlinked home directory) must be
	// resolved before walking, otherwise WalkDir only visits the root itself.
	// Symlinks inside the tree are still not followed.
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, err
	}

	var links []string

	err = filepath.WalkDir(resolved, func(resolvedPath string, entry fs.DirEntry, walkErr error) error {
		rel, relErr := filepath.Rel(resolved, resolvedPath)
		if relErr != nil {
			return relErr
		}

		if rel == "." {
			if walkErr != nil {
				return walkErr
			}
			return nil
		}

		// Keep paths under the caller's original root.
		entryPath := filepath.Join(root, rel)

		if walkErr != nil {
			// Skip unreadable entries inside the tree instead of aborting the
			// whole scan.
			if entry != nil && entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}

		if excluded(entry.Name(), filepath.ToSlash(rel), excludes) {
			if entry.IsDir() {
				return fs.SkipDir
			}
			return nil
		}

		if entry.Type()&fs.ModeSymlink != 0 {
			links = append(links, entryPath)
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return links, nil
}

// excluded reports whether name or rel matches any of the patterns.
func excluded(name, rel string, patterns []string) bool {
	return slices.ContainsFunc(patterns, func(pattern string) bool {
		pattern = filepath.ToSlash(pattern)

		if strings.Contains(pattern, "/") {
			if matched, err := path.Match(pattern, rel); err == nil && matched {
				return true
			}
			return strings.HasSuffix(rel, "/"+pattern)
		}

		matched, err := path.Match(pattern, name)
		return err == nil && matched
	})
}
