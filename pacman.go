package fest

import "github.com/emad-elsaid/types"

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/emad-elsaid/fest/yay"
	"github.com/samber/lo"
)

// ResourcePackages is the resource name for pacman packages.
const ResourcePackages ResourceName = "packages"

var packages []string

// Package declares one or more pacman packages to be installed.
// Packages can be from official repositories or AUR.
//
// Example:
//
//	fest.Package("vim", "git", "docker")
func Package(pkgs ...string) { addUnique(&packages, pkgs...) }

// PackageGroup expands pacman package groups into individual packages.
// This queries pacman for all packages in the given groups and adds them.
//
// Example:
//
//	fest.PackageGroup("base-devel")
func PackageGroup(groupNames ...string) {
	for _, groupName := range groupNames {
		stdout, err := types.Cmd("pacman", "-Sg", groupName).StdoutErr()
		if err != nil {
			slog.Warn("failed to query pacman groups", "error", err, "group", groupName)
			continue // Continue with next group instead of returning early
		}

		for line := range strings.SplitSeq(strings.TrimSpace(stdout), "\n") {
			fields := strings.Fields(line)
			if len(fields) != 2 {
				continue
			}
			Package(fields[1])
		}
	}
}

type pacman struct {
	yayClient *yay.Client
}

func (p pacman) Wanted() []string { return packages }

func (p pacman) ResourceName() string         { return string(ResourcePackages) }
func (p pacman) Match(want, have string) bool { return want == have }

func (p pacman) ListInstalled() ([]string, error) {
	stdout, err := types.Cmd("pacman", "-Qq").StdoutErr()
	if err != nil {
		return nil, err
	}
	return strings.Fields(stdout), nil
}

func (p pacman) ListExplicit() ([]string, error) {
	stdout, err := types.Cmd("pacman", "-Qeq").StdoutErr()
	if err != nil {
		return nil, err
	}
	return strings.Fields(stdout), nil
}

func (p pacman) Install(pkgs []string) error {
	// Initialize yay client if not already done
	if p.yayClient == nil {
		client, err := yay.NewClient("needed", "noconfirm")
		if err != nil {
			return fmt.Errorf("failed to initialize yay: %w", err)
		}
		defer client.Close()
		p.yayClient = client
	}

	ctx := context.Background()
	return p.yayClient.Install(ctx, pkgs)
}

func (p pacman) Uninstall(pkgs []string) error {
	return types.Sudo("pacman", append([]string{"-Rns"}, pkgs...)...).Interactive().Error()
}

func (p pacman) MarkExplicit(pkgs []string) error {
	return types.Sudo("pacman", append([]string{"-D", "--asexplicit"}, pkgs...)...).Interactive().Error()
}

func (p pacman) Update() error {
	// Pacman packages are never version-locked in the declaration, so a full
	// system upgrade refreshes the databases and updates every installed package.
	slog.Info("Updating system packages")
	return types.Sudo("pacman", "-Syu").Interactive().Error()
}

func (p pacman) GetDependencies() (map[string][]string, error) {
	stdout, err := types.Cmd("expac", "-Q", "%n|%D|%S").StdoutErr()
	if err != nil {
		return nil, fmt.Errorf("failed to run expac: %w", err)
	}
	deps := make(map[string][]string)
	provides := make(map[string][]string)
	stripVersion := func(s string) string {
		return strings.FieldsFunc(s, func(r rune) bool { return r == '>' || r == '<' || r == '=' })[0]
	}
	scanner := bufio.NewScanner(strings.NewReader(stdout))
	for scanner.Scan() {
		parts := strings.Split(scanner.Text(), "|")
		if len(parts) != 3 {
			continue
		}
		pkg := strings.TrimSpace(parts[0])
		if parts[1] != "-" && parts[1] != "" {
			for dep := range strings.FieldsSeq(parts[1]) {
				deps[pkg] = append(deps[pkg], stripVersion(dep))
			}
		}
		provides[pkg] = append(provides[pkg], pkg)
		if parts[2] != "-" && parts[2] != "" {
			for prov := range strings.FieldsSeq(parts[2]) {
				provides[stripVersion(prov)] = append(provides[stripVersion(prov)], pkg)
			}
		}
	}
	// Resolve virtual packages to real packages
	resolved := make(map[string][]string)
	for pkg, pkgDeps := range deps {
		// Use map to deduplicate dependencies
		uniqueDeps := make(map[string]bool)
		for _, dep := range pkgDeps {
			for _, realPkg := range provides[dep] {
				uniqueDeps[realPkg] = true
			}
		}
		// Convert map back to slice
		for dep := range uniqueDeps {
			resolved[pkg] = append(resolved[pkg], dep)
		}
	}
	return resolved, scanner.Err()
}

func (p pacman) SaveAsGo(wanted []string) error {
	installed, err := p.ListExplicit()
	if err != nil {
		return err
	}

	diff := lo.Without(installed, wanted...)
	if len(diff) == 0 {
		logSuccess("No new packages to save")
		return nil
	}

	deps, err := p.GetDependencies()
	if err != nil {
		return err
	}
	keep := getKeepPackages(wanted, deps)

	var toSave []string
	for _, pkg := range diff {
		if !keep[pkg] {
			toSave = append(toSave, pkg)
		}
	}

	if len(toSave) == 0 {
		logSuccess("No new packages to save")
		return nil
	}

	if err := saveAsGoFile("packages.go", "Package", toSave); err != nil {
		return err
	}
	logSuccess("packages saved", "file", "packages.go", "count", len(toSave))
	return nil
}
