package fest

import "github.com/emad-elsaid/types"

import (
	"bufio"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

const ResourceGoPackages ResourceName = "Go packages"

var (
	wantedGoPackages []string
	goListCache      listCache
)

type goPackages struct{}

func (g goPackages) Wanted() []string { return wantedGoPackages }

func (g goPackages) ResourceName() string { return string(ResourceGoPackages) }

func (g goPackages) Match(want, have string) bool {
	return matchWithVersion(want, have, splitVer)
}

func (g goPackages) getGoBin() (string, error) {
	if gobin := os.Getenv("GOBIN"); gobin != "" {
		return gobin, nil
	}
	gopath := os.Getenv("GOPATH")
	if gopath == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		gopath = filepath.Join(home, "go")
	}
	return filepath.Join(gopath, "bin"), nil
}

func (g goPackages) getBinaryModule(binPath string) (string, error) {
	stdout, err := types.Cmd("go", "version", "-m", binPath).StdoutErr()
	if err != nil {
		return "", err
	}

	var path, version string
	scanner := bufio.NewScanner(strings.NewReader(stdout))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		parts := strings.Fields(line)
		if len(parts) < 2 {
			continue
		}

		switch parts[0] {
		case "path":
			path = parts[1]
		case "mod":
			if len(parts) >= 3 {
				version = parts[2]
			}
		}
	}

	if path == "" {
		return "", fmt.Errorf("no valid module path found")
	}

	if version == "(devel)" || version == "" {
		version = "latest"
	}

	return path + "@" + version, nil
}

func (g goPackages) ListInstalled() ([]string, error) {
	return goListCache.getOrSet(func() ([]string, error) {
		if _, err := types.Cmd("go", "version").StdoutErr(); err != nil {
			slog.Debug("go is not installed or not available")
			return []string{}, nil
		}

		goBin, err := g.getGoBin()
		if err != nil {
			return nil, err
		}

		entries, err := os.ReadDir(goBin)
		if os.IsNotExist(err) {
			return []string{}, nil
		}
		if err != nil {
			return nil, err
		}

		var installed []string
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			binPath := filepath.Join(goBin, entry.Name())
			modulePath, err := g.getBinaryModule(binPath)
			if err != nil {
				continue
			}
			installed = append(installed, modulePath)
		}
		return installed, nil
	})
}

func (g goPackages) ListExplicit() ([]string, error) {
	return g.ListInstalled()
}

func (g goPackages) Install(pkgs []string) error {
	defer goListCache.invalidate()

	if _, err := types.Cmd("go", "version").StdoutErr(); err != nil {
		slog.Warn("go is not installed, skipping Go package installation")
		return nil
	}

	for _, pkg := range pkgs {
		installPkg := pkg
		if !strings.Contains(pkg, "@") {
			installPkg = pkg + "@latest"
		}
		slog.Info("Installing Go package", "package", installPkg)
		if err := types.Cmd("go", "install", installPkg).Interactive().Error(); err != nil {
			return err
		}
	}
	return nil
}

func (g goPackages) Uninstall(pkgs []string) error {
	defer goListCache.invalidate()

	if _, err := types.Cmd("go", "version").StdoutErr(); err != nil {
		return nil
	}

	goBin, err := g.getGoBin()
	if err != nil {
		return err
	}

	entries, err := os.ReadDir(goBin)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}

	// Build a map of module paths to binary names
	moduleToBinary := make(map[string]string)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		binPath := filepath.Join(goBin, entry.Name())
		modulePath, err := g.getBinaryModule(binPath)
		if err != nil {
			continue
		}
		moduleToBinary[modulePath] = entry.Name()
	}

	// Uninstall each package by finding its binary
	for _, pkg := range pkgs {
		// Check if this exact module path matches a binary
		if binaryName, found := moduleToBinary[pkg]; found {
			binPath := filepath.Join(goBin, binaryName)
			slog.Info("Removing Go binary", "binary", binPath, "module", pkg)
			if err := os.Remove(binPath); err != nil && !os.IsNotExist(err) {
				return err
			}
			continue
		}

		// If not found, try matching using our Match function (version flexibility)
		for modulePath, binaryName := range moduleToBinary {
			if g.Match(pkg, modulePath) {
				binPath := filepath.Join(goBin, binaryName)
				slog.Info("Removing Go binary", "binary", binPath, "module", modulePath)
				if err := os.Remove(binPath); err != nil && !os.IsNotExist(err) {
					return err
				}
			}
		}
	}
	return nil
}

func (g goPackages) MarkExplicit([]string) error                   { return nil }
func (g goPackages) GetDependencies() (map[string][]string, error) { return nil, nil }

func (g goPackages) Update() error {
	defer goListCache.invalidate()

	if _, err := types.Cmd("go", "version").StdoutErr(); err != nil {
		slog.Warn("go is not installed, skipping Go package update")
		return nil
	}

	var toUpdate []string
	for _, pkg := range g.Wanted() {
		if _, ver := splitVer(pkg); isVersionLocked(ver) {
			slog.Debug("skipping version-locked Go package", "package", pkg)
			continue
		}
		toUpdate = append(toUpdate, pkg)
	}
	if len(toUpdate) == 0 {
		slog.Info("No Go packages to update")
		return nil
	}

	slog.Info("Updating Go packages", "count", len(toUpdate))
	for _, pkg := range toUpdate {
		installPkg := pkg
		if !strings.Contains(pkg, "@") {
			installPkg = pkg + "@latest"
		}
		slog.Info("Updating Go package", "package", installPkg)
		if err := types.Cmd("go", "install", installPkg).Interactive().Error(); err != nil {
			return err
		}
	}
	return nil
}

func (g goPackages) SaveAsGo(wanted []string) error {
	installed, err := g.ListExplicit()
	if err != nil {
		return err
	}

	diff := subtract(g, installed, wanted)
	if len(diff) == 0 {
		logSuccess("No new go packages to save")
		return nil
	}

	if err := saveAsGoFile("go_packages.go", "GoPackage", diff); err != nil {
		return err
	}
	logSuccess("go packages saved", "file", "go_packages.go", "count", len(diff))
	return nil
}

// GoPackage declares Go packages to install using `go install`.
// Packages are installed to GOBIN or $GOPATH/bin.
// Supports version pinning using @version syntax.
//
// Example:
//
//	fest.GoPackage(
//	    "github.com/golangci/golangci-lint/cmd/golangci-lint@latest",
//	    "golang.org/x/tools/cmd/goimports@v0.15.0",
//	)
func GoPackage(pkgs ...string) {
	normalized := make([]string, 0, len(pkgs))
	for _, pkg := range pkgs {
		if strings.HasSuffix(pkg, "@(devel)") {
			pkg = strings.TrimSuffix(pkg, "@(devel)") + "@latest"
		}
		normalized = append(normalized, pkg)
	}
	addUnique(&wantedGoPackages, normalized...)
}
