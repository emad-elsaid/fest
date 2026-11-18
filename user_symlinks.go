package fest

import (
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/emad-elsaid/fest/fd"
	"github.com/emad-elsaid/types"
)

const ResourceBrokenSymlinks ResourceName = "broken symlinks"

var ignoreFiles = []string{
	"SingletonCookie",
	"SingletonLock",
	"SingletonSocket",
}

var ignoredDirs = []string{
	".git",
	".svn",
	".hg",
	"node_modules",
	".cache",
	".npm",
	".cargo",
	".rustup",
	".go",
	".mozilla",
	".steam",
	".wine",
	"vendor",
	"__pycache__",
	".local/state",
	".var",
	".thunderbird",
	".dropbox",
	".vscode",
	".cursor",
}

var systemDirs = []string{
	"/etc",
	"/usr/local",
	"/opt",
	"/usr/share",
}

// Cache for ListInstalled to avoid expensive operations being called twice
var symlinkCache struct {
	sync.Mutex
	result []string
	cached bool
}

type symlinks struct{}

func (u symlinks) ResourceName() string                          { return string(ResourceBrokenSymlinks) }
func (u symlinks) Wanted() []string                              { return []string{} }
func (u symlinks) Match(want, have string) bool                  { return want == have }
func (u symlinks) GetDependencies() (map[string][]string, error) { return nil, nil }
func (u symlinks) SaveAsGo([]string) error                       { return nil }

// findBrokenSymlinks walks searchPath looking for symlinks and keeps the ones
// whose target cannot be resolved. The walk is read-only and the scanned
// directories are world-readable, so no elevated privileges are required.
func (u symlinks) findBrokenSymlinks(searchPath string) ([]string, error) {
	links, err := fd.Symlinks(searchPath, ignoredDirs)
	if err != nil {
		return nil, err
	}

	return brokenLinks(links), nil
}

// brokenLinks keeps the non-ignored links whose target cannot be resolved.
func brokenLinks(links []string) []string {
	return slices.DeleteFunc(append([]string{}, links...), func(link string) bool {
		return isIgnoredLink(link) || !isBrokenLink(link)
	})
}

// isIgnoredLink reports whether the link names one of the files to ignore.
func isIgnoredLink(link string) bool {
	return slices.Contains(ignoreFiles, filepath.Base(link))
}

// isBrokenLink reports whether the link's target cannot be resolved, for any
// reason (ENOENT, ELOOP, ENOTDIR, EACCES, ...).
func isBrokenLink(link string) bool {
	_, err := os.Stat(link)
	return err != nil
}

func (u symlinks) ListInstalled() ([]string, error) {
	// Return cached result if available
	symlinkCache.Lock()
	if symlinkCache.cached {
		result := symlinkCache.result
		symlinkCache.Unlock()
		return result, nil
	}
	symlinkCache.Unlock()

	// Get home directory
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}

	// Collect all directories to scan
	dirsToScan := []string{home}
	for _, sysDir := range systemDirs {
		if _, err := os.Stat(sysDir); os.IsNotExist(err) {
			continue // Skip directories that don't exist
		}
		dirsToScan = append(dirsToScan, sysDir)
	}

	// Scan all directories in parallel
	var wg sync.WaitGroup
	var mu sync.Mutex
	var allSymlinks []string

	for _, dir := range dirsToScan {
		wg.Go(func() {
			symlinks, err := u.findBrokenSymlinks(dir)
			if err != nil {
				slog.Debug("Failed to check "+dir, "error", err)
				return
			}
			mu.Lock()
			allSymlinks = append(allSymlinks, symlinks...)
			mu.Unlock()
		})
	}

	wg.Wait()

	// Cache the result
	symlinkCache.Lock()
	symlinkCache.result = allSymlinks
	symlinkCache.cached = true
	symlinkCache.Unlock()

	return allSymlinks, nil
}

func (u symlinks) ListExplicit() ([]string, error) {
	return u.ListInstalled()
}

func (u symlinks) Install([]string) error      { return nil }
func (u symlinks) MarkExplicit([]string) error { return nil }
func (u symlinks) Update() error               { return nil }

func (u symlinks) isSystemPath(path string) bool {
	for _, sysDir := range systemDirs {
		if strings.HasPrefix(path, sysDir+"/") || path == sysDir {
			return true
		}
	}
	return false
}

func (u symlinks) Uninstall(symlinks []string) error {
	// Clear cache since we're modifying symlinks
	symlinkCache.Lock()
	symlinkCache.cached = false
	symlinkCache.result = nil
	symlinkCache.Unlock()

	errs := []error{}

	for _, link := range symlinks {
		slog.Debug("Removing " + link)

		if u.isSystemPath(link) {
			// Use sudo for system paths
			if err := types.Sudo("rm", link).Interactive().Error(); err != nil {
				errs = append(errs, err)
			}
		} else {
			// Regular removal for user paths
			if err := os.Remove(link); err != nil {
				errs = append(errs, err)
			}
		}
	}

	return errors.Join(errs...)
}
