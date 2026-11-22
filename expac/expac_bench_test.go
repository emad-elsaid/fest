package expac

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// buildDesc renders a pacman desc file shaped like a real database entry.
func buildDesc(name string, depends, provides int) string {
	var b strings.Builder

	fmt.Fprintf(&b, "%%NAME%%\n%s\n\n", name)
	b.WriteString("%VERSION%\n1.0.0-1\n\n")
	fmt.Fprintf(&b, "%%DESC%%\nPackage %s\n\n", name)
	b.WriteString("%ARCH%\nx86_64\n\n")
	b.WriteString("%BUILDDATE%\n1700000000\n\n")
	b.WriteString("%INSTALLDATE%\n1700000000\n\n")
	b.WriteString("%PACKAGER%\nBench <bench@example.com>\n\n")
	b.WriteString("%SIZE%\n1024\n\n")
	b.WriteString("%DEPENDS%\n")
	for i := range depends {
		fmt.Fprintf(&b, "dep-%03d>=1.0\n", i)
	}
	b.WriteString("\n%PROVIDES%\n")
	for i := range provides {
		fmt.Fprintf(&b, "virtual-%03d=1.0\n", i)
	}
	b.WriteString("\n")

	return b.String()
}

func BenchmarkParseDesc(b *testing.B) {
	data := buildDesc("bench-package", 12, 2)

	b.ReportAllocs()
	b.SetBytes(int64(len(data)))

	for b.Loop() {
		parseDesc(data)
	}
}

// BenchmarkLocal measures reading the real pacman local database.
func BenchmarkLocal(b *testing.B) {
	dbPath := ConfiguredDBPath()
	if _, err := os.Stat(filepath.Join(dbPath, "local")); err != nil {
		b.Skipf("pacman local database unavailable at %s: %v", dbPath, err)
	}

	entries, err := os.ReadDir(filepath.Join(dbPath, "local"))
	if err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	b.Logf("reading %d local database entries", len(entries))

	for b.Loop() {
		if _, err := Local(dbPath); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkLocalSynthetic isolates the per-entry cost from database size.
func BenchmarkLocalSynthetic(b *testing.B) {
	const packages = 2000

	dbPath := b.TempDir()
	localDir := filepath.Join(dbPath, "local")

	for i := range packages {
		dir := filepath.Join(localDir, fmt.Sprintf("package-%04d", i))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			b.Fatal(err)
		}
		if err := os.WriteFile(
			filepath.Join(dir, "desc"),
			[]byte(buildDesc(fmt.Sprintf("package-%04d", i), 8, 1)),
			0o644,
		); err != nil {
			b.Fatal(err)
		}
	}

	b.ReportAllocs()

	for b.Loop() {
		if _, err := Local(dbPath); err != nil {
			b.Fatal(err)
		}
	}
}
