package fest

import (
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
)

// Main is the entry point for the archlinux configuration management system.
// It handles command-line argument parsing and dispatches to the appropriate command handler.
//
// Available commands:
//   - apply: Synchronize system state with declared configuration
//   - save: Save current system state as declarative Go code
//   - diff: Show what would change without making actual changes
//
// This function should be called from your main package after declaring your desired configuration.
func Main() {
	slog.SetDefault(slog.New(newPrettyHandler(os.Stdout)))
	flag.Parse()
	args := flag.Args()
	if len(args) == 0 {
		usage()
		os.Exit(1)
	}

	command := args[0]

	// Check for help before dependency check to allow --help without dependencies
	if command == "help" || command == "--help" || command == "-h" {
		usage()
		return
	}

	// Warn about extra arguments
	if len(args) > 1 {
		slog.Warn("Extra arguments ignored", "arguments", args[1:])
	}

	dotfilesDir, err := os.Getwd()
	checkFatal(err, "Failed to get current working dir")
	stowDir := filepath.Join(dotfilesDir, "user")

	// Check and install dependencies
	checkFatal(checkDependencies(), "Dependency check failed")

	switch command {
	case "apply":
		cmdApply(stowDir)
	case "save":
		cmdSave(stowDir)
	case "diff":
		cmdDiff()
	default:
		slog.Error("Unknown command", "command", command)
		fmt.Println()
		usage()
		os.Exit(1)
	}
}

func usage() {
	fmt.Print(`Usage: <program> [COMMAND]

A declarative system configuration management tool for Arch Linux.

Commands:
    apply       Synchronize system state with declared configuration
                (installs packages, enables services, copies files, etc.)

    save        Save current system state as declarative Go code
                (useful for capturing manual changes)

    diff        Dry-run simulation showing what would change
                without making any actual changes

    help        Show this help message

Examples:
    go run . diff     # See what would change
    go run . apply    # Apply configuration
    go run . save     # Save current state to Go files

Configuration:
    Define your system configuration by calling functions like:
        fest.Package("vim", "git")
        fest.Service("docker")
        fest.Timedate("America/New_York", true)
`)
}

// newSystemd creates a new systemd manager for the given resource type.
func newSystemd(res ResourceName, unit, fn, file, msg string, user bool, wanted *[]string) *systemdManager {
	return &systemdManager{
		resource: res, unitType: unit, user: user, wanted: wanted,
		funcName: fn, filename: file, successMsg: msg,
	}
}

// allManagers returns all package managers in the order they should be processed.
func allManagers() []packageManager {
	userServices := newSystemd(ResourceUserServices, "service", "Service", "services.go", "user services", true, &services)
	userServices.socketWanted = &sockets
	sysServices := newSystemd(ResourceSystemServices, "service", "SystemService", "system_services.go", "system services", false, &systemServices)
	sysServices.socketWanted = &systemSockets

	return []packageManager{
		systemConfigManager{},
		pacman{},
		flatpak{},
		npmPackages{},
		goPackages{},
		rubyGems{},
		userGroups{},
		symlinks{},
		systemFiles{},
		userServices,
		newSystemd(ResourceUserTimers, "timer", "Timer", "timers.go", "user timers", true, &timers),
		newSystemd(ResourceUserSockets, "socket", "Socket", "sockets.go", "user sockets", true, &sockets),
		sysServices,
		newSystemd(ResourceSystemTimers, "timer", "SystemTimer", "system_timers.go", "system timers", false, &systemTimers),
		newSystemd(ResourceSystemSockets, "socket", "SystemSocket", "system_sockets.go", "system sockets", false, &systemSockets),
	}
}

// cmdDiff shows what would change without making actual changes.
func cmdDiff() {
	executeCommandCallbacks(PhaseBeforeDiff)

	stow := newUserStow()
	checkWarn(stow.Diff(), "Failed to diff stow")

	var wg sync.WaitGroup
	for _, mgr := range allManagers() {
		wg.Go(func() {
			checkWarn(diffPackages(mgr, mgr.Wanted()), "Failed to diff "+mgr.ResourceName())
		})
	}
	wg.Wait()

	executeCommandCallbacks(PhaseAfterDiff)
	logSuccess("diff complete")
}

// cmdSave saves the current system state as declarative Go code.
func cmdSave(stowDir string) {
	// Create stowDir if it doesn't exist
	if err := os.MkdirAll(stowDir, 0755); err != nil {
		checkFatal(err, "Failed to create stow directory")
	}

	executeCommandCallbacks(PhaseBeforeSave)

	stow := newUserStow()
	checkFatal(stow.Save(), "Failed to save with stow")

	var wg sync.WaitGroup
	for _, mgr := range allManagers() {
		wg.Go(func() {
			checkWarn(mgr.SaveAsGo(mgr.Wanted()), "Failed to save "+mgr.ResourceName())
		})
	}
	wg.Wait()

	executeCommandCallbacks(PhaseAfterSave)
}

// cmdApply synchronizes the system state with the declared configuration.
func cmdApply(stowDir string) {
	requireDir(stowDir)
	executeCommandCallbacks(PhaseBeforeApply)

	stow := newUserStow()
	checkFatal(stow.Apply(), "Failed to apply with stow")

	for _, mgr := range allManagers() {
		executeCallbacks(mgr.ResourceName(), callbacks.before, "before")

		err := syncPackages(mgr, mgr.Wanted())
		if err != nil {
			checkWarn(err, "Failed to sync "+mgr.ResourceName())
		}

		executeCallbacks(mgr.ResourceName(), callbacks.after, "after")
	}

	executeCommandCallbacks(PhaseAfterApply)
}
