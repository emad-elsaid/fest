package fest

import (
	"fmt"
	"log/slog"
	"sync"

	"github.com/samber/lo"
)

// ResourceName represents a package manager resource type identifier.
type ResourceName string

// packageManager defines the interface that all resource managers must implement.
// Each manager handles a specific type of system resource (packages, services, files, etc.).
type packageManager interface {
	// ResourceName returns a human-readable identifier for this resource type.
	ResourceName() string

	// Wanted returns the list of desired resources declared by the user.
	Wanted() []string

	// Match returns true if the want and have strings represent the same resource.
	// This allows for fuzzy matching (e.g., version flexibility).
	Match(want, have string) bool

	// ListInstalled returns all currently installed resources of this type.
	ListInstalled() ([]string, error)

	// ListExplicit returns resources that were explicitly installed (vs dependencies).
	// Some managers treat all resources as explicit.
	ListExplicit() ([]string, error)

	// Install installs the given resources.
	Install(pkgs []string) error

	// Uninstall removes the given resources.
	Uninstall(pkgs []string) error

	// MarkExplicit marks resources as explicitly installed.
	MarkExplicit(pkgs []string) error

	// GetDependencies returns a map of resource -> dependencies.
	// Returns nil if dependency tracking is not supported.
	GetDependencies() (map[string][]string, error)

	// SaveAsGo generates Go code that declares the given resources.
	// This is used during the "save" command to persist system state.
	SaveAsGo(wanted []string) error
}

// implicitWanter is implemented by package managers whose resources may appear
// implicitly because a declared resource triggers them (e.g. a service activated
// by a declared socket).
type implicitWanter interface {
	ImplicitWanted() []string
}

// Callback is a function that can be executed before or after a package manager operation.
type Callback func()

// callbacks stores Before and After callbacks for each package manager resource.
var callbacks = struct {
	sync.RWMutex
	before map[string][]Callback
	after  map[string][]Callback
}{
	before: make(map[string][]Callback),
	after:  make(map[string][]Callback),
}

// Before registers a callback to be executed before the given resource name is synced.
func Before(resourceName ResourceName, callback Callback) {
	callbacks.Lock()
	defer callbacks.Unlock()
	callbacks.before[string(resourceName)] = append(callbacks.before[string(resourceName)], callback)
}

// After registers a callback to be executed after the given resource name is synced.
func After(resourceName ResourceName, callback Callback) {
	callbacks.Lock()
	defer callbacks.Unlock()
	callbacks.after[string(resourceName)] = append(callbacks.after[string(resourceName)], callback)
}

// ============================================================
// Command Lifecycle Events
// ============================================================

// CommandPhase represents a phase in the command lifecycle.
type CommandPhase string

// Available command lifecycle phases.
const (
	PhaseBeforeDiff  CommandPhase = "before-diff"
	PhaseAfterDiff   CommandPhase = "after-diff"
	PhaseBeforeSave  CommandPhase = "before-save"
	PhaseAfterSave   CommandPhase = "after-save"
	PhaseBeforeApply CommandPhase = "before-apply"
	PhaseAfterApply  CommandPhase = "after-apply"
)

// commandCallbacks stores callbacks for command lifecycle phases.
var (
	commandCallbacks   = make(map[CommandPhase][]Callback)
	commandCallbacksMu sync.RWMutex
)

// OnCommand registers a callback to be executed during the given command phase.
func OnCommand(phase CommandPhase, callback Callback) {
	commandCallbacksMu.Lock()
	defer commandCallbacksMu.Unlock()
	commandCallbacks[phase] = append(commandCallbacks[phase], callback)
}

// executeCommandCallbacks runs all callbacks for a given command phase.
func executeCommandCallbacks(phase CommandPhase) {
	commandCallbacksMu.RLock()
	callbacks := commandCallbacks[phase]
	commandCallbacksMu.RUnlock()

	if len(callbacks) == 0 {
		return
	}

	slog.Debug(fmt.Sprintf("Executing command callbacks for %s (%d handlers)", phase, len(callbacks)))

	for i, cb := range callbacks {
		slog.Debug(fmt.Sprintf("Running callback %d/%d for %s", i+1, len(callbacks), phase))

		// Recover from panics to prevent one handler from breaking others
		func() {
			defer func() {
				if r := recover(); r != nil {
					slog.Warn(fmt.Sprintf("Callback %d panicked during %s: %v", i+1, phase, r))
				}
			}()
			cb()
		}()
	}
}

// executeCallbacks runs all callbacks for a given resource and callback type.
func executeCallbacks(resourceName string, callbackMap map[string][]Callback, when string) error {
	cbs := callbackMap[resourceName]
	if len(cbs) == 0 {
		return nil
	}

	for i, cb := range cbs {
		slog.Debug(fmt.Sprintf("Executing callbacks %s %s manager (%d/%d)", when, resourceName, i+1, len(cbs)))

		// Recover from panics to prevent one handler from breaking others
		func() {
			defer func() {
				if r := recover(); r != nil {
					slog.Warn(fmt.Sprintf("Callback %d panicked %s %s: %v", i+1, when, resourceName, r))
				}
			}()
			cb()
		}()
	}

	return nil
}

// implicitWanted returns resources that are implicitly present because another
// declared resource triggers them (e.g. a service activated by a declared
// socket). These must never be uninstalled. Managers without implicit resources
// return nil.
func implicitWanted(pm packageManager) []string {
	if iw, ok := pm.(implicitWanter); ok {
		return iw.ImplicitWanted()
	}
	return nil
}

// unwantedResources returns installed resources that are neither explicitly
// wanted, implicitly wanted, nor kept as a dependency of a wanted resource.
func unwantedResources(pm packageManager, installed, wanted, implicit []string, keep map[string]bool) []string {
	var toUninstall []string
	for _, installedPkg := range installed {
		isWanted := lo.ContainsBy(wanted, func(w string) bool { return pm.Match(w, installedPkg) }) ||
			lo.ContainsBy(implicit, func(w string) bool { return pm.Match(w, installedPkg) })
		if !isWanted && !keep[installedPkg] {
			toUninstall = append(toUninstall, installedPkg)
		}
	}
	return toUninstall
}

// syncPackages syncs the system state with wanted packages.
// It installs missing packages, marks implicit packages as explicit, and optionally removes unwanted ones.
func syncPackages(pm packageManager, wanted []string) error {
	rn := pm.ResourceName()
	installed, err := pm.ListInstalled()
	if err != nil {
		return err
	}
	explicit, err := pm.ListExplicit()
	if err != nil {
		return err
	}
	toInstall := subtract(pm, wanted, installed)
	toMark := intersect(pm, subtract(pm, wanted, explicit), installed)
	if len(toMark) > 0 {
		slog.Info("Marking "+rn+" as explicit", "count", len(toMark))
		if err := pm.MarkExplicit(toMark); err != nil {
			return err
		}
	}
	if len(toInstall) > 0 {
		slog.Info("Installing "+rn, "count", len(toInstall))
		if err := pm.Install(toInstall); err != nil {
			return err
		}
	}
	deps, err := pm.GetDependencies()
	if err != nil {
		return err
	}
	installed, err = pm.ListInstalled() // refresh after install
	if err != nil {
		return err
	}
	keep := getKeepPackages(wanted, deps)
	toUninstall := unwantedResources(pm, installed, wanted, implicitWanted(pm), keep)
	if len(toUninstall) == 0 {
		if len(toInstall) == 0 && len(toMark) == 0 {
			logSuccess(rn + ": up to date")
		} else {
			logSuccess(rn + ": synced")
		}
		return nil
	}
	slog.Info("Found unnecessary "+rn, "count", len(toUninstall), rn, toUninstall)
	if confirm, _ := askYesNo("Do you want to uninstall these " + rn + "?"); !confirm {
		slog.Info("Skipping " + rn + " removal")
		return nil
	}
	slog.Warn("Uninstalling "+rn, "count", len(toUninstall))
	if err := pm.Uninstall(toUninstall); err != nil {
		return err
	}
	logSuccess(rn + ": cleaned up")
	return nil
}

// diffPackages shows what would change without making changes.
// This is used by the "diff" command to preview operations.
func diffPackages(pm packageManager, wanted []string) error {
	rn := pm.ResourceName()
	installed, err := pm.ListInstalled()
	if err != nil {
		return err
	}
	explicit, err := pm.ListExplicit()
	if err != nil {
		return err
	}
	toInstall := subtract(pm, wanted, installed)
	toMark := intersect(pm, subtract(pm, wanted, explicit), installed)
	deps, err := pm.GetDependencies()
	if err != nil {
		return err
	}
	keep := getKeepPackages(wanted, deps)
	toUninstall := unwantedResources(pm, installed, wanted, implicitWanted(pm), keep)
	if len(toInstall) == 0 && len(toMark) == 0 && len(toUninstall) == 0 {
		logSuccess(rn + ": no changes")
		return nil
	}

	logInfoIf(len(toInstall) > 0, "Would install "+rn, "count", len(toInstall), "items", toInstall)
	logInfoIf(len(toMark) > 0, "Would mark "+rn+" as explicit", "count", len(toMark), "items", toMark)

	// Show detailed uninstall preview for system-files
	if rn == "system-files" && len(toUninstall) > 0 {
		if sf, ok := pm.(systemFiles); ok {
			preview := sf.UninstallPreview(toUninstall)
			for _, path := range toUninstall {
				action := preview[path]
				slog.Info("Would uninstall "+rn, "path", path, "action", action)
			}
		}
	} else {
		logInfoIf(len(toUninstall) > 0, "Would uninstall "+rn, "count", len(toUninstall), "items", toUninstall)
	}
	return nil
}
