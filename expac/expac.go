// Package expac implements the subset of expac that fest uses: reading the
// local pacman database and reporting each installed package's name,
// dependencies, and provides. It replaces the external expac binary.
package expac

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	pacmanconf "github.com/Morganamilo/go-pacmanconf"
)

// DefaultDBPath is the default location of pacman's database on Arch Linux.
const DefaultDBPath = "/var/lib/pacman"

// pacmanConfPath is pacman's default configuration file.
const pacmanConfPath = "/etc/pacman.conf"

// ConfiguredDBPath returns the database path configured in the system's
// pacman.conf, falling back to DefaultDBPath.
func ConfiguredDBPath() string {
	return dbPath(pacmanConfPath)
}

// dbPath returns the database path configured in the given pacman.conf, falling
// back to DefaultDBPath when the file cannot be read or does not set DBPath.
func dbPath(confPath string) string {
	config, err := readConfig(confPath, 0)
	if err != nil {
		return DefaultDBPath
	}

	conf, err := pacmanconf.Parse(config)
	if err != nil || conf.DBPath == "" {
		return DefaultDBPath
	}

	return conf.DBPath
}

// maxIncludeDepth bounds recursive Include directives so include cycles cannot
// loop forever.
const maxIncludeDepth = 10

// readConfig reads a pacman.conf and expands Include directives in place, so
// options in included files (and mirrorlists in repository sections) are parsed
// in their original section context.
func readConfig(path string, depth int) (string, error) {
	if depth > maxIncludeDepth {
		return "", fmt.Errorf("too many nested includes: %s", path)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}

	var expanded strings.Builder

	for line := range strings.Lines(string(data)) {
		line = strings.TrimSuffix(line, "\n")

		key, value, ok := strings.Cut(line, "=")
		if ok && strings.EqualFold(strings.TrimSpace(key), "Include") {
			matches, err := filepath.Glob(strings.TrimSpace(value))
			if err != nil {
				return "", err
			}

			for _, match := range matches {
				included, err := readConfig(match, depth+1)
				if err != nil {
					return "", err
				}

				expanded.WriteString(included)
				expanded.WriteByte('\n')
			}

			continue
		}

		expanded.WriteString(line)
		expanded.WriteByte('\n')
	}

	return expanded.String(), nil
}

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
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	return parseDesc(string(data)), nil
}

// parseDesc parses pacman's %KEY% value block format.
func parseDesc(data string) map[string][]string {
	fields := make(map[string][]string)

	key := ""
	for line := range strings.Lines(data) {
		line = strings.TrimSuffix(line, "\n")

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

	return fields
}

// first returns the first value for key, or an empty string when absent.
func first(fields map[string][]string, key string) string {
	values := fields[key]
	if len(values) == 0 {
		return ""
	}

	return values[0]
}
