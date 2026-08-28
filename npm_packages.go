package fest

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/emad-elsaid/types"
)

const ResourceNpmPackages ResourceName = "npm packages"

var (
	wantedNpmPackages []string
	npmListCache      listCache
)

// NpmPackage declares global npm packages to install.
// Supports version pinning using @version syntax.
// Handles scoped packages correctly (e.g., @vue/cli).
//
// Example:
//
//	fest.NpmPackage(
//	    "typescript",
//	    "@vue/cli",
//	    "eslint@8.50.0",  // Pin to specific version
//	)
func NpmPackage(pkgs ...string) {
	// Validate and filter out empty/whitespace-only packages
	valid := make([]string, 0, len(pkgs))
	for _, pkg := range pkgs {
		if strings.TrimSpace(pkg) != "" {
			valid = append(valid, pkg)
		}
	}
	addUnique(&wantedNpmPackages, valid...)
}

type npmPackages struct{}

func (n npmPackages) Wanted() []string     { return wantedNpmPackages }
func (n npmPackages) ResourceName() string { return string(ResourceNpmPackages) }

func (n npmPackages) Match(want, have string) bool {
	wantName, wantVer := splitNpmVer(want)
	haveName, haveVer := splitNpmVer(have)
	if wantName != haveName {
		return false
	}
	// Don't trim whitespace from versions - they indicate malformed input
	return wantVer == "" || wantVer == "latest" || haveVer == "" || haveVer == "latest" || wantVer == haveVer
}

type npmListOutput struct {
	Dependencies map[string]struct {
		Version string `json:"version"`
	} `json:"dependencies"`
}

func (n npmPackages) ListInstalled() ([]string, error) {
	return npmListCache.getOrSet(func() ([]string, error) {
		stdout, err := types.Cmd("npm", "list", "-g", "--depth=0", "--json").StdoutErr()
		if err != nil {
			// npm list returns non-zero if there are issues, but still outputs JSON.
			// Empty output with an error means npm is missing or unavailable, which
			// we treat as an empty install rather than a hard failure.
			if stdout == "" {
				slog.Debug("npm is not installed or not available")
				return []string{}, nil
			}
		}

		var output npmListOutput
		if err := json.Unmarshal([]byte(stdout), &output); err != nil {
			return nil, fmt.Errorf("failed to parse npm list output: %w", err)
		}

		var packages []string
		for name, info := range output.Dependencies {
			packages = append(packages, name+"@"+info.Version)
		}
		return packages, nil
	})
}

func (n npmPackages) ListExplicit() ([]string, error) {
	return n.ListInstalled()
}

func (n npmPackages) Install(pkgs []string) error {
	defer npmListCache.invalidate()

	if _, err := types.Cmd("npm", "--version").StdoutErr(); err != nil {
		slog.Warn("npm is not installed, skipping npm package installation")
		return nil
	}

	for _, pkg := range pkgs {
		// Validate package name is not empty/whitespace
		if strings.TrimSpace(pkg) == "" {
			return fmt.Errorf("invalid npm package: empty or whitespace-only name")
		}

		installPkg := pkg
		if _, ver := splitNpmVer(pkg); ver == "" {
			installPkg = pkg + "@latest"
		}

		slog.Info("Installing npm package", "package", installPkg)
		if err := types.Cmd("npm", "install", "-g", installPkg).Interactive().Error(); err != nil {
			return err
		}
	}
	return nil
}

func (n npmPackages) Uninstall(pkgs []string) error {
	defer npmListCache.invalidate()

	if _, err := types.Cmd("npm", "--version").StdoutErr(); err != nil {
		return nil
	}

	for _, pkg := range pkgs {
		// Validate package name is not empty/whitespace
		if strings.TrimSpace(pkg) == "" {
			return fmt.Errorf("invalid npm package: empty or whitespace-only name")
		}

		pkgName, _ := splitNpmVer(pkg)

		slog.Info("Uninstalling npm package", "package", pkgName)
		if err := types.Cmd("npm", "uninstall", "-g", pkgName).Interactive().Error(); err != nil {
			return err
		}
	}
	return nil
}

func (n npmPackages) MarkExplicit([]string) error                   { return nil }
func (n npmPackages) GetDependencies() (map[string][]string, error) { return nil, nil }

func (n npmPackages) SaveAsGo(wanted []string) error {
	installed, err := n.ListExplicit()
	if err != nil {
		return err
	}

	diff := subtract(n, installed, wanted)
	if len(diff) == 0 {
		logSuccess("No new npm packages to save")
		return nil
	}

	if err := saveAsGoFile("npm_packages.go", "NpmPackage", diff); err != nil {
		return err
	}
	logSuccess("npm packages saved", "file", "npm_packages.go", "count", len(diff))
	return nil
}
