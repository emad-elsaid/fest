package fest

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"

	"github.com/emad-elsaid/fest/yay"
	"github.com/emad-elsaid/types"
)

// dependency represents a system tool dependency required for the framework to function.
type dependency struct {
	Name        string   // Command name to check for
	PackageName string   // Package that provides it (defaults to Name if empty)
	Required    bool     // If true, fail if missing; if false, warn only
	CheckCmd    []string // Custom command to check if installed (default: which <name>)
}

// pkg returns the package name, defaulting to Name if PackageName is empty.
func (d dependency) pkg() string {
	if d.PackageName != "" {
		return d.PackageName
	}
	return d.Name
}

// dependencies lists all system dependencies required by this framework.
var dependencies = []dependency{
	// Core package management (critical)
	{Name: "pacman", Required: true},
	{Name: "expac", PackageName: "expac", Required: true},

	// Build tools (critical for this program)
	{Name: "go", PackageName: "go", Required: true},

	// Dotfile management (critical)
	{Name: "stow", PackageName: "stow", Required: true},

	// Systemd tools (usually part of base system)
	{Name: "systemctl", Required: true},
	{Name: "timedatectl", Required: true},
	{Name: "localectl", Required: true},

	// System configuration tools
	{Name: "locale-gen", Required: true},
	{Name: "sed", Required: true},
	{Name: "sudo", Required: true},

	// File manipulation tools (usually in base system)
	{Name: "diff", PackageName: "diffutils", Required: true},
	{Name: "tee", PackageName: "coreutils", Required: true},
	{Name: "cat", PackageName: "coreutils", Required: true},
	{Name: "chmod", PackageName: "coreutils", Required: true},
	{Name: "mkdir", PackageName: "coreutils", Required: true},
	{Name: "fd", PackageName: "fd", Required: true},

	// Language runtimes (optional features)
	{Name: "ruby", PackageName: "ruby", Required: false},
	{Name: "gem", PackageName: "rubygems", Required: false},
	{Name: "flatpak", PackageName: "flatpak", Required: false},
}

// checkDependencies verifies all required dependencies are installed
// and attempts to install missing ones where possible.
// Returns an error if required dependencies cannot be installed.
func checkDependencies() error {
	var missing, optionalMissing []dependency

	for _, dep := range dependencies {
		if !isInstalled(dep) {
			if dep.Required {
				missing = append(missing, dep)
			} else {
				optionalMissing = append(optionalMissing, dep)
			}
		}
	}

	if len(optionalMissing) > 0 {
		slog.Warn("Optional dependencies not found (some features may be unavailable)",
			"missing", strings.Join(depNames(optionalMissing), ", "))
	}

	if len(missing) == 0 {
		logSuccess("All required dependencies are installed")
		return nil
	}

	slog.Info("Missing required dependencies", "count", len(missing))

	if err := installDependencies(missing); err != nil {
		return fmt.Errorf("failed to install dependencies: %w", err)
	}

	if stillMissing := filterMissing(missing); len(stillMissing) > 0 {
		return fmt.Errorf("failed to install required dependencies: %s", strings.Join(stillMissing, ", "))
	}

	logSuccess("All dependencies installed successfully")
	return nil
}

// isInstalled checks if a dependency is installed using its check command or "which".
func isInstalled(dep dependency) bool {
	// Validate dependency has a name
	if dep.Name == "" {
		return false
	}

	if len(dep.CheckCmd) > 0 {
		// Validate CheckCmd has non-empty command
		if dep.CheckCmd[0] == "" {
			return false
		}
		return types.Cmd(dep.CheckCmd[0], dep.CheckCmd[1:]...).Error() == nil
	}
	_, err := exec.LookPath(dep.Name)
	return err == nil
}

// depNames extracts the names from a slice of dependencies.
func depNames(deps []dependency) []string {
	names := make([]string, len(deps))
	for i, dep := range deps {
		names[i] = dep.Name
	}
	return names
}

// filterMissing returns the names of dependencies that are still missing.
func filterMissing(deps []dependency) []string {
	if deps == nil {
		return []string{}
	}
	var missing []string
	for _, dep := range deps {
		if !isInstalled(dep) {
			missing = append(missing, dep.Name)
		}
	}
	return missing
}

// installDependencies attempts to install the given dependencies using pacman or yay.
func installDependencies(deps []dependency) error {
	var pacmanPkgs, aurPkgs []string

	for _, dep := range deps {
		pkg := dep.pkg()
		// Validate package name is not empty
		if pkg == "" {
			return fmt.Errorf("dependency has empty package name: %+v", dep)
		}

		// Check if package exists in official repos
		if types.Cmd("pacman", "-Si", pkg).Error() == nil {
			pacmanPkgs = append(pacmanPkgs, pkg)
		} else {
			aurPkgs = append(aurPkgs, pkg)
		}
	}

	if len(pacmanPkgs) > 0 {
		slog.Info("Installing from official repos", "packages", strings.Join(pacmanPkgs, ", "))
		if err := types.Sudo("pacman", append([]string{"-S", "--needed", "--noconfirm"}, pacmanPkgs...)...).Interactive().Error(); err != nil {
			return fmt.Errorf("pacman install failed: %w", err)
		}
	}

	if len(aurPkgs) > 0 {
		slog.Info("Installing from AUR", "packages", strings.Join(aurPkgs, ", "))
		client, err := yay.NewClient("needed", "noconfirm")
		if err != nil {
			return fmt.Errorf("failed to initialize yay: %w", err)
		}
		defer client.Close()

		ctx := context.Background()
		if err := client.Install(ctx, aurPkgs); err != nil {
			return fmt.Errorf("AUR install failed: %w", err)
		}
	}

	return nil
}

// listDependencies prints information about all dependencies and their installation status.
func listDependencies() {
	printCategory := func(title string, filter func(dependency) bool) {
		fmt.Println(title)
		for _, dep := range dependencies {
			if filter(dep) {
				status := "✗"
				if isInstalled(dep) {
					status = "✓"
				}
				fmt.Printf("  [%s] %-15s (package: %s)\n", status, dep.Name, dep.pkg())
			}
		}
	}

	printCategory("Required Dependencies:", func(d dependency) bool { return d.Required })
	printCategory("\nOptional Dependencies:", func(d dependency) bool { return !d.Required })
}
