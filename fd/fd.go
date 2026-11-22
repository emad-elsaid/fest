// Package fd implements the subset of fd that fest uses: walking a directory
// tree without following symlinks and returning the absolute paths of every
// symlink, while skipping excluded directories. It replaces the external fd
// binary.
package fd

import (
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

// Symlinks returns the absolute paths of all symlinks under root. Excludes
// match entry base names or, when they contain a slash, paths relative to root.
// Matching directories are skipped. The walk is concurrent, so the result is
// not sorted.
func Symlinks(root string, excludes []string) ([]string, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}

	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, err
	}

	info, err := os.Stat(resolved)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, nil
	}

	w := &walker{
		root:           root,
		rootIsResolved: root == resolved,
		excludes:       newExcluder(excludes),
		sem:            make(chan struct{}, max(2, 2*runtime.GOMAXPROCS(0))),
	}
	w.needRel = w.excludes.needsRel()
	if !w.rootIsResolved {
		w.resolvedPrefix = resolved
		if !strings.HasSuffix(w.resolvedPrefix, string(filepath.Separator)) {
			w.resolvedPrefix += string(filepath.Separator)
		}
	}

	entries, err := readDir(resolved)
	if err != nil {
		return nil, err
	}

	w.walkEntries(resolved, "", entries)
	w.wg.Wait()

	return w.links, nil
}

// walker walks one tree and collects its symlinks.
type walker struct {
	root           string
	resolvedPrefix string
	rootIsResolved bool
	needRel        bool

	excludes *excluder

	sem   chan struct{}
	wg    sync.WaitGroup
	mu    sync.Mutex
	links []string
}

// readDir reads a directory without sorting its entries.
func readDir(dir string) ([]fs.DirEntry, error) {
	file, err := os.Open(dir)
	if err != nil {
		return nil, err
	}

	entries, err := file.ReadDir(-1)
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}

	return entries, err
}

// walkDir visits the entries of dir, skipping unreadable subdirectories.
func (w *walker) walkDir(dir, rel string) {
	entries, err := readDir(dir)
	if err != nil {
		return
	}

	w.walkEntries(dir, rel, entries)
}

// walkEntries visits entries, walking subdirectories concurrently when possible.
func (w *walker) walkEntries(dir, rel string, entries []fs.DirEntry) {
	for _, entry := range entries {
		name := entry.Name()
		childRel := w.childRel(rel, name)

		if w.excludes.match(name, childRel) {
			continue
		}

		switch {
		case entry.Type()&fs.ModeSymlink != 0:
			w.addLink(dir, name)
		case entry.IsDir():
			child := filepath.Join(dir, name)
			if w.trySpawn(child, childRel) {
				continue
			}
			w.walkDir(child, childRel)
		}
	}
}

// childRel returns name relative to the walk root when exclusion rules need it.
func (w *walker) childRel(rel, name string) string {
	if !w.needRel {
		return ""
	}
	if rel == "" {
		return name
	}
	return rel + "/" + name
}

// trySpawn walks dir in a new goroutine when a worker slot is available.
func (w *walker) trySpawn(dir, rel string) bool {
	select {
	case w.sem <- struct{}{}:
		w.wg.Go(func() {
			defer func() { <-w.sem }()
			w.walkDir(dir, rel)
		})
		return true
	default:
		return false
	}
}

// addLink records the symlink dir/name under the caller's original root.
func (w *walker) addLink(dir, name string) {
	link := filepath.Join(dir, name)
	if !w.rootIsResolved {
		rel := strings.TrimPrefix(link, w.resolvedPrefix)
		link = filepath.Join(w.root, rel)
	}

	w.mu.Lock()
	w.links = append(w.links, link)
	w.mu.Unlock()
}

// excluder holds compiled exclusion patterns.
type excluder struct {
	names     map[string]struct{}
	nameGlobs []string
	paths     []pathPattern
}

type pathPattern struct {
	pattern string
	suffix  string
	glob    bool
}

// newExcluder compiles patterns into base-name and root-relative matchers.
func newExcluder(patterns []string) *excluder {
	e := &excluder{}

	for _, pattern := range patterns {
		pattern = filepath.ToSlash(pattern)

		if strings.Contains(pattern, "/") {
			e.paths = append(e.paths, pathPattern{
				pattern: pattern,
				suffix:  "/" + pattern,
				glob:    hasMeta(pattern),
			})
			continue
		}

		if hasMeta(pattern) {
			e.nameGlobs = append(e.nameGlobs, pattern)
			continue
		}

		if e.names == nil {
			e.names = make(map[string]struct{})
		}
		e.names[pattern] = struct{}{}
	}

	return e
}

// hasMeta reports whether pattern contains glob metacharacters.
func hasMeta(pattern string) bool {
	return strings.ContainsAny(pattern, `*?[\`)
}

// needsRel reports whether matching requires the path relative to the root.
func (e *excluder) needsRel() bool { return len(e.paths) > 0 }

// match reports whether name or rel is excluded.
func (e *excluder) match(name, rel string) bool {
	if e.names != nil {
		if _, ok := e.names[name]; ok {
			return true
		}
	}

	for _, pattern := range e.nameGlobs {
		if matched, err := path.Match(pattern, name); err == nil && matched {
			return true
		}
	}

	for _, p := range e.paths {
		if !p.glob {
			if rel == p.pattern || strings.HasSuffix(rel, p.suffix) {
				return true
			}
			continue
		}

		if matched, err := path.Match(p.pattern, rel); err == nil && matched {
			return true
		}
		if strings.HasSuffix(rel, p.suffix) {
			return true
		}
	}

	return false
}
