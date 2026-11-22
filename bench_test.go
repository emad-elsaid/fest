package fest

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

// BenchmarkGetDependencies measures reading the pacman database and building
// the dependency graph.
func BenchmarkGetDependencies(b *testing.B) {
	pm := &pacman{}

	if _, err := os.Stat(filepath.Join(pacmanDBPath(), "local")); err != nil {
		b.Skipf("pacman database unavailable: %v", err)
	}

	b.ReportAllocs()

	for b.Loop() {
		pm.invalidateLocal()

		if _, err := pm.GetDependencies(); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkDiffManagers times each manager's diff work on the real system.
// Run it with FEST_BENCH_REAL_SYSTEM=1.
func BenchmarkDiffManagers(b *testing.B) {
	if os.Getenv("FEST_BENCH_REAL_SYSTEM") == "" {
		b.Skip("set FEST_BENCH_REAL_SYSTEM=1 to query the real system")
	}

	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	b.Cleanup(func() { slog.SetDefault(previous) })

	for _, manager := range allManagers() {
		b.Run(manager.ResourceName(), func(b *testing.B) {
			b.ReportAllocs()

			for b.Loop() {
				resetManagerCaches(manager)

				if err := diffPackages(manager, manager.Wanted()); err != nil {
					b.Skip(err)
				}
			}
		})
	}
}

// resetManagerCaches clears the caches a manager uses.
func resetManagerCaches(manager packageManager) {
	if p, ok := manager.(*pacman); ok {
		p.invalidateLocal()
	}
	if s, ok := manager.(*systemdManager); ok {
		s.invalidate()
	}

	goListCache.invalidate()
	npmListCache.invalidate()
	rubyListCache.invalidate()

	symlinkCache.Lock()
	symlinkCache.cached = false
	symlinkCache.result = nil
	symlinkCache.Unlock()
}

// BenchmarkSymlinksListInstalled measures the broken-symlink scan.
// Run it with FEST_BENCH_REAL_SYSTEM=1.
func BenchmarkSymlinksListInstalled(b *testing.B) {
	if os.Getenv("FEST_BENCH_REAL_SYSTEM") == "" {
		b.Skip("set FEST_BENCH_REAL_SYSTEM=1 to walk the real filesystem")
	}

	manager := symlinks{}

	b.ReportAllocs()

	for b.Loop() {
		symlinkCache.Lock()
		symlinkCache.cached = false
		symlinkCache.result = nil
		symlinkCache.Unlock()

		if _, err := manager.ListInstalled(); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkBrokenSymlinksByDir isolates the scan cost per searched directory.
func BenchmarkBrokenSymlinksByDir(b *testing.B) {
	if os.Getenv("FEST_BENCH_REAL_SYSTEM") == "" {
		b.Skip("set FEST_BENCH_REAL_SYSTEM=1 to walk the real filesystem")
	}

	home, err := os.UserHomeDir()
	if err != nil {
		b.Fatal(err)
	}

	dirs := append([]string{home}, systemDirs...)
	manager := symlinks{}

	for _, dir := range dirs {
		b.Run(dir, func(b *testing.B) {
			if _, err := os.Stat(dir); err != nil {
				b.Skipf("unavailable: %v", err)
			}

			b.ReportAllocs()

			for b.Loop() {
				if _, err := manager.findBrokenSymlinks(dir); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
