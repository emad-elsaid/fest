package fd

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// buildTree creates a directory tree where every 20th file is a symlink.
func buildTree(b *testing.B, dirs, filesPerDir int) string {
	b.Helper()

	root := b.TempDir()

	for d := range dirs {
		dir := filepath.Join(root, fmt.Sprintf("dir-%03d", d), "nested")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			b.Fatal(err)
		}

		for f := range filesPerDir {
			file := filepath.Join(dir, fmt.Sprintf("file-%03d", f))
			if err := os.WriteFile(file, nil, 0o644); err != nil {
				b.Fatal(err)
			}

			if f%20 == 0 {
				link := file + ".link"
				if err := os.Symlink(file, link); err != nil {
					b.Fatal(err)
				}
			}
		}
	}

	return root
}

func BenchmarkSymlinks(b *testing.B) {
	root := buildTree(b, 200, 50)

	b.ReportAllocs()
	b.ResetTimer()

	for b.Loop() {
		links, err := Symlinks(root, []string{".git", "node_modules"})
		if err != nil {
			b.Fatal(err)
		}
		if len(links) == 0 {
			b.Fatal("expected symlinks")
		}
	}
}

func BenchmarkExcluded(b *testing.B) {
	patterns := []string{".git", "node_modules", "*.log", "target", ".cache"}
	e := newExcluder(patterns)

	b.ReportAllocs()

	for b.Loop() {
		e.match("cache", "projects/app/.cache")
	}
}

func BenchmarkNewExcluder(b *testing.B) {
	patterns := []string{
		".git", ".svn", ".hg", "node_modules", ".cache", ".npm", ".cargo",
		".rustup", ".go", ".mozilla", ".steam", ".wine", "vendor",
		"__pycache__", ".local/state", ".local/share", ".var", ".thunderbird",
		".dropbox", ".vscode", ".cursor", ".gem", ".bun", ".pnpm-store",
		".yarn", ".gradle", ".m2", "go/pkg",
	}

	b.ReportAllocs()

	for b.Loop() {
		newExcluder(patterns)
	}
}
