package fest

import "github.com/emad-elsaid/types"

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// systemConfigType holds system configuration settings
type systemConfigType struct {
	// Timedate
	Timezone string
	NTP      bool

	// Locale
	Locale string

	// Keyboard
	Keymap  string
	Layout  string
	Model   string
	Variant string
	Options string
}

var systemConfig = systemConfigType{}

// validateLocale ensures locale string is safe for shell commands
func validateLocale(locale string) error {
	if locale == "" {
		return fmt.Errorf("locale cannot be empty")
	}
	// Locale format: language_TERRITORY.ENCODING [MODIFIER]
	// Example: en_US.UTF-8, en_US.UTF-8 UTF-8, sr_RS.UTF-8@latin
	pattern := regexp.MustCompile(`^[a-zA-Z_]+\.[A-Z0-9-]+(@[a-z]+)?( [A-Z0-9-]+)?$`)
	if !pattern.MatchString(locale) {
		return fmt.Errorf("invalid locale format: %s", locale)
	}
	return nil
}

// validateTimezone ensures timezone is valid by checking if it exists
func validateTimezone(timezone string) error {
	if timezone == "" {
		return fmt.Errorf("timezone cannot be empty")
	}
	// Check for path traversal attempts
	if strings.Contains(timezone, "..") {
		return fmt.Errorf("invalid timezone (path traversal): %s", timezone)
	}
	// Validate format: should be like America/New_York
	pattern := regexp.MustCompile(`^[A-Za-z_]+(/[A-Za-z_]+)?$`)
	if !pattern.MatchString(timezone) {
		return fmt.Errorf("invalid timezone format: %s", timezone)
	}
	// Verify it exists in zoneinfo
	zonePath := filepath.Join("/usr/share/zoneinfo", timezone)
	if _, err := os.Stat(zonePath); os.IsNotExist(err) {
		return fmt.Errorf("timezone not found: %s", timezone)
	}
	return nil
}

// Timedate sets timezone and NTP configuration.
// This configures the system timezone and enables/disables NTP time synchronization.
//
// Example:
//
//	fest.Timedate("America/New_York", true)  // Set timezone and enable NTP
func Timedate(timezone string, ntp bool) {
	systemConfig.Timezone = timezone
	systemConfig.NTP = ntp
}

// Locale sets the system locale.
// The locale string should match an entry in /etc/locale.gen.
//
// Example:
//
//	fest.Locale("en_US.UTF-8 UTF-8")
func Locale(locale string) {
	systemConfig.Locale = locale
}

// Keyboard sets keyboard configuration for both console and X11.
// Parameters:
//   - keymap: Console keymap (e.g., "us")
//   - layout: X11 keyboard layout (e.g., "us,ara" for multiple layouts)
//   - model: Keyboard model (e.g., "pc105")
//   - variant: Layout variant (e.g., "dvorak")
//   - options: Keyboard options (e.g., "ctrl:nocaps,grp:alt_shift_toggle")
//
// Example:
//
//	fest.Keyboard("us", "us", "pc105", "", "ctrl:nocaps")
func Keyboard(keymap, layout, model, variant, options string) {
	systemConfig.Keymap = keymap
	systemConfig.Layout = layout
	systemConfig.Model = model
	systemConfig.Variant = variant
	systemConfig.Options = options
}

type systemConfigManager struct{}

func (s systemConfigManager) ResourceName() string { return "system-config" }
func (s systemConfigManager) Wanted() []string {
	var wanted []string
	if systemConfig.Timezone != "" {
		wanted = append(wanted, "timezone:"+systemConfig.Timezone)
	}
	if systemConfig.NTP {
		wanted = append(wanted, "ntp:enabled")
	} else if systemConfig.Timezone != "" {
		// Support disabling NTP when timezone is set
		wanted = append(wanted, "ntp:disabled")
	}
	if systemConfig.Locale != "" {
		wanted = append(wanted, "locale:"+systemConfig.Locale)
	}
	if systemConfig.Keymap != "" || systemConfig.Layout != "" {
		wanted = append(wanted, "keyboard:configured")
	}
	return wanted
}
func (s systemConfigManager) Match(want, have string) bool { return want == have }
func (s systemConfigManager) ListInstalled() ([]string, error) {
	var installed []string

	// Check timezone
	if systemConfig.Timezone != "" {
		output, err := types.Cmd("timedatectl", "show", "-p", "Timezone", "--value").StdoutErr()
		if err == nil && strings.TrimSpace(output) == systemConfig.Timezone {
			installed = append(installed, "timezone:"+systemConfig.Timezone)
		}
	}

	// Check NTP
	if systemConfig.NTP {
		output, err := types.Cmd("timedatectl", "show", "-p", "NTP", "--value").StdoutErr()
		if err == nil && strings.TrimSpace(output) == "yes" {
			installed = append(installed, "ntp:enabled")
		}
	}

	// Check locale
	if systemConfig.Locale != "" {
		output, err := types.Cmd("localectl", "status").StdoutErr()
		if err == nil {
			// Parse locale more carefully - look for the actual System Locale line
			for _, line := range strings.Split(output, "\n") {
				line = strings.TrimSpace(line)
				// Look for lines like: "System Locale: LANG=en_US.UTF-8"
				if strings.HasPrefix(line, "System Locale:") || strings.Contains(line, "LANG=") {
					// Extract the LANG value
					if idx := strings.Index(line, "LANG="); idx != -1 {
						// Get everything after LANG=
						langPart := line[idx+5:]
						// Extract just the locale value (stop at whitespace or comma)
						locale := strings.FieldsFunc(langPart, func(r rune) bool {
							return r == ' ' || r == '\t' || r == ','
						})[0]
						if locale == systemConfig.Locale {
							installed = append(installed, "locale:"+systemConfig.Locale)
							break
						}
					}
				}
			}
		}
	}

	// Check keyboard (simplified check)
	if systemConfig.Keymap != "" || systemConfig.Layout != "" {
		// Just mark as installed if any keyboard config exists
		output, err := types.Cmd("localectl", "status").StdoutErr()
		if err == nil && strings.Contains(output, "Keymap:") {
			installed = append(installed, "keyboard:configured")
		}
	}

	return installed, nil
}
func (s systemConfigManager) ListExplicit() ([]string, error) { return s.ListInstalled() }
func (s systemConfigManager) Install(configs []string) error {
	for _, cfg := range configs {
		parts := strings.SplitN(cfg, ":", 2)
		if len(parts) != 2 {
			return fmt.Errorf("malformed config (missing colon): %s", cfg)
		}

		switch parts[0] {
		case "timezone":
			if err := validateTimezone(parts[1]); err != nil {
				return fmt.Errorf("invalid timezone: %w", err)
			}
			slog.Info("Setting timezone", "timezone", parts[1])
			if err := types.Sudo("timedatectl", "set-timezone", parts[1]).Interactive().Error(); err != nil {
				return err
			}
		case "ntp":
			if parts[1] == "enabled" {
				slog.Info("Enabling NTP")
				if err := types.Sudo("timedatectl", "set-ntp", "true").Interactive().Error(); err != nil {
					return err
				}
			} else if parts[1] == "disabled" {
				slog.Info("Disabling NTP")
				if err := types.Sudo("timedatectl", "set-ntp", "false").Interactive().Error(); err != nil {
					return err
				}
			} else {
				return fmt.Errorf("invalid ntp value (must be enabled or disabled): %s", parts[1])
			}
		case "locale":
			if err := validateLocale(parts[1]); err != nil {
				return fmt.Errorf("invalid locale: %w", err)
			}
			slog.Info("Setting locale", "locale", parts[1])
			// Use | as sed delimiter to avoid issues with / in locale strings
			sedPattern := fmt.Sprintf("s|^#%s|%s|", parts[1], parts[1])
			if err := types.Sudo("sed", "-i", sedPattern, "/etc/locale.gen").Interactive().Error(); err != nil {
				slog.Warn("Failed to uncomment locale in /etc/locale.gen", "error", err)
			}
			if err := types.Sudo("locale-gen").Interactive().Error(); err != nil {
				return err
			}
			if err := types.Sudo("localectl", "set-locale", "LANG="+parts[1]).Interactive().Error(); err != nil {
				return err
			}
		case "keyboard":
			slog.Info("Setting keyboard configuration")
			keymapArgs := []string{"set-keymap"}
			if systemConfig.Keymap != "" {
				keymapArgs = append(keymapArgs, systemConfig.Keymap)
			} else {
				keymapArgs = append(keymapArgs, "us")
			}
			if err := types.Sudo("localectl", keymapArgs...).Interactive().Error(); err != nil {
				return err
			}

			// Set X11 keyboard layout if specified
			if systemConfig.Layout != "" {
				x11Args := []string{"set-x11-keymap", systemConfig.Layout}
				// Only add optional parameters if they're non-empty
				if systemConfig.Model != "" {
					x11Args = append(x11Args, systemConfig.Model)
					if systemConfig.Variant != "" {
						x11Args = append(x11Args, systemConfig.Variant)
						if systemConfig.Options != "" {
							x11Args = append(x11Args, systemConfig.Options)
						}
					} else if systemConfig.Options != "" {
						// variant empty but options set: need empty variant placeholder
						x11Args = append(x11Args, "", systemConfig.Options)
					}
				} else if systemConfig.Variant != "" {
					// model empty but variant set: need empty model placeholder
					x11Args = append(x11Args, "", systemConfig.Variant)
					if systemConfig.Options != "" {
						x11Args = append(x11Args, systemConfig.Options)
					}
				} else if systemConfig.Options != "" {
					// both empty but options set: need two empty placeholders
					x11Args = append(x11Args, "", "", systemConfig.Options)
				}
				if err := types.Sudo("localectl", x11Args...).Interactive().Error(); err != nil {
					return err
				}
			}
		default:
			return fmt.Errorf("unrecognized system config type: %s", parts[0])
		}
	}
	return nil
}
func (s systemConfigManager) Uninstall(configs []string) error {
	slog.Warn("Skipping uninstall for system config", "count", len(configs))
	return nil
}
func (s systemConfigManager) MarkExplicit(configs []string) error { return nil }
func (s systemConfigManager) Update() error                       { return nil }
func (s systemConfigManager) GetDependencies() (map[string][]string, error) {
	return make(map[string][]string), nil
}
func (s systemConfigManager) SaveAsGo(wanted []string) error { return nil }
