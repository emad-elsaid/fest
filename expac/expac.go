// Package expac implements the subset of expac that fest uses: reading the
// local pacman database and reporting each installed package's name,
// dependencies, and provides. It replaces the external expac binary.
package expac

import (
	"bufio"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// DefaultDBPath is the default location of pacman's database on Arch Linux.
const DefaultDBPath = "/var/lib/pacman"

// Package holds the installed package metadata that fest's dependency graph
// needs. Depends and Provides keep their pacman version constraints, if any.
type Package struct {
	Name     string
	Depends  []string
	Provides []string
}

// Local returns metadata for every package in the local pacman database under
// dbPath. A missing database is reported as an empty slice so callers on
// non-pacman systems get a usable, if empty, result. Entries with an
// unreadable desc file are skipped, mirroring libalpm's tolerance of a
// partially corrupt local database.
func Local(dbPath string) ([]Package, error) {
	entries, err := os.ReadDir(filepath.Join(dbPath, "local"))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}

	packages := make([]Package, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		fields, err := readDesc(filepath.Join(dbPath, "local", entry.Name(), "desc"))
		if err != nil {
			continue
		}

		name := first(fields, "NAME")
		if name == "" {
			continue
		}

		packages = append(packages, Package{
			Name:     name,
			Depends:  fields["DEPENDS"],
			Provides: fields["PROVIDES"],
		})
	}

	return packages, nil
}

// readDesc parses pacman's %KEY% value block format.
func readDesc(path string) (map[string][]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	return parseDesc(file)
}

// parseDesc parses pacman's %KEY% value block format.
func parseDesc(reader io.Reader) (map[string][]string, error) {
	fields := make(map[string][]string)
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)

	key := ""
	for scanner.Scan() {
		line := scanner.Text()

		if len(line) > 2 && line[0] == '%' && line[len(line)-1] == '%' {
			key = line[1 : len(line)-1]
			continue
		}

		if line == "" {
			key = ""
			continue
		}

		if key != "" {
			fields[key] = append(fields[key], line)
		}
	}

	return fields, scanner.Err()
}

// first returns the first value for key, or an empty string when absent.
func first(fields map[string][]string, key string) string {
	values := fields[key]
	if len(values) == 0 {
		return ""
	}

	return values[0]
}
