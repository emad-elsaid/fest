package fest

import "github.com/emad-elsaid/types"

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/samber/lo"
)

const ResourceFlatpak ResourceName = "flatpak"

var wantedFlatpak []string

// Flatpak declares desired flatpak applications (by application ID).
// Applications are installed from Flathub by default.
//
// Example:
//
//	managers.Flatpak(
//	    "com.slack.Slack",
//	    "com.github.tchx84.Flatseal",
//	    "org.mozilla.firefox",
//	)
func Flatpak(apps ...string) { addUnique(&wantedFlatpak, apps...) }

type flatpak struct{}

func (f flatpak) Wanted() []string { return wantedFlatpak }

func (f flatpak) ResourceName() string         { return string(ResourceFlatpak) }
func (f flatpak) Match(want, have string) bool { return want == have }

func (f flatpak) ListInstalled() ([]string, error) {
	if _, err := types.Cmd("flatpak", "--version").StdoutErr(); err != nil {
		slog.Debug("flatpak is not installed or not available")
		return []string{}, nil
	}

	stdout, err := types.Cmd("flatpak", "list", "--app", "--columns=application").StdoutErr()
	if err != nil {
		return nil, err
	}

	if strings.TrimSpace(stdout) == "" {
		return []string{}, nil
	}

	var apps []string
	for line := range strings.SplitSeq(strings.TrimSpace(stdout), "\n") {
		app := strings.TrimSpace(line)
		if app != "" {
			apps = append(apps, app)
		}
	}
	return apps, nil
}

func (f flatpak) ListExplicit() ([]string, error) {
	// For flatpak, all installed apps are explicit
	return f.ListInstalled()
}

func (f flatpak) Install(apps []string) error {
	if _, err := types.Cmd("flatpak", "--version").StdoutErr(); err != nil {
		slog.Warn("flatpak is not installed, skipping flatpak installation")
		return nil
	}

	for _, app := range apps {
		slog.Info("Installing flatpak app", "app", app)
		// Install from flathub by default, with -y to auto-confirm
		if err := types.Cmd("flatpak", "install", "-y", "flathub", app).Interactive().Error(); err != nil {
			return fmt.Errorf("failed to install flatpak app %s: %w", app, err)
		}
	}
	return nil
}

func (f flatpak) Uninstall(apps []string) error {
	if _, err := types.Cmd("flatpak", "--version").StdoutErr(); err != nil {
		return nil
	}

	for _, app := range apps {
		slog.Info("Uninstalling flatpak app", "app", app)
		if err := types.Cmd("flatpak", "uninstall", "-y", app).Interactive().Error(); err != nil {
			return fmt.Errorf("failed to uninstall flatpak app %s: %w", app, err)
		}
	}
	return nil
}

func (f flatpak) MarkExplicit([]string) error {
	// No concept of explicit vs implicit for flatpak user apps
	return nil
}

func (f flatpak) Update() error {
	if _, err := types.Cmd("flatpak", "--version").StdoutErr(); err != nil {
		slog.Warn("flatpak is not installed, skipping flatpak update")
		return nil
	}

	// Flatpak apps are never version-locked, so every installed app is updated.
	slog.Info("Updating flatpak apps")
	if err := types.Cmd("flatpak", "update", "-y").Interactive().Error(); err != nil {
		return fmt.Errorf("failed to update flatpak apps: %w", err)
	}
	return nil
}

func (f flatpak) GetDependencies() (map[string][]string, error) {
	// Flatpak manages dependencies internally, no need to track them
	return nil, nil
}

func (f flatpak) SaveAsGo(wanted []string) error {
	installed, err := f.ListExplicit()
	if err != nil {
		return err
	}

	diff := lo.Without(installed, wanted...)
	if len(diff) == 0 {
		logSuccess("No new flatpak apps to save")
		return nil
	}

	if err := saveAsGoFile("flatpak.go", "Flatpak", diff); err != nil {
		return err
	}
	logSuccess("flatpak apps saved", "file", "flatpak.go", "count", len(diff))
	return nil
}
