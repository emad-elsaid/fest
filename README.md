# Fest

[![Go Reference](https://pkg.go.dev/badge/github.com/emad-elsaid/fest.svg)](https://pkg.go.dev/github.com/emad-elsaid/fest)
[![Go Report Card](https://goreportcard.com/badge/github.com/emad-elsaid/fest)](https://goreportcard.com/report/github.com/emad-elsaid/fest)
[![License](https://img.shields.io/github/license/emad-elsaid/fest)](https://github.com/emad-elsaid/fest/blob/master/LICENSE)

A declarative system configuration management framework for Arch Linux written in Go.

> **Note**: This is a complete rewrite from the original Ruby implementation. The Go version provides
> better performance, type safety, and easier distribution as a single binary.

## Overview

`fest` allows you to declare your entire system state (packages, services, files, configurations) as Go code, and synchronizes your Arch Linux system to match that declared state. Think of it as a type-safe, compiled alternative to configuration management tools like Ansible or NixOS, specifically tailored for Arch Linux.

## Features

- **Declarative Configuration**: Define what you want, not how to get it
- **Package Management**: Manages pacman packages (including AUR via embedded yay)
- **Multiple Package Managers**: Support for Flatpak, npm, Go packages, Ruby gems
- **System Configuration**: Timezone, locale, keyboard settings
- **Systemd Services**: Enable/disable system and user services, timers, and sockets
- **User Groups**: Manage user group memberships
- **System Files**: Deploy and track system configuration files
- **Dotfile Management**: GNU Stow integration for user dotfiles
- **Broken Symlink Cleanup**: Automatically detect and remove broken symlinks
- **Two-Phase Execution**: Preview changes before applying (diff mode)
- **Updates**: Upgrade installed packages while respecting version pins
- **State Tracking**: Knows what it installed and can clean up unwanted resources
- **Dependency Awareness**: Won't remove packages that other wanted packages depend on

## Installation

### Prerequisites

Before using this framework, ensure you have:

1. Arch Linux system
2. Go 1.25+ installed
3. `stow` for dotfile management

**Note**: This project includes an embedded copy of [yay](https://github.com/Jguer/yay) for AUR package management. No separate yay installation is required.

### Quick Start

1. Create a new Go module for your system configuration:

```bash
mkdir -p ~/mysystem
cd ~/mysystem
go mod init mysystem
go get github.com/emad-elsaid/fest
```

2. Create your configuration file (e.g., `main.go`):

```go
package main

import "github.com/emad-elsaid/fest"

func init() {
    // Declare packages
    fest.Package(
        "vim",
        "git",
        "docker",
        "firefox",
    )

    // Enable services
    fest.SystemService("docker")

    // Configure system
    fest.Timedate("America/New_York", true)
    fest.Locale("en_US.UTF-8 UTF-8")
}

func main() {
    fest.Main()
}
```

3. Run commands:

```bash
# Preview what would change
go run . diff

# Apply configuration
go run . apply

# Update installed packages (skipping version-pinned ones)
go run . update

# Save current system state back to Go files
go run . save
```

## Usage

### Commands

- **`apply`**: Synchronize your system to match the declared configuration
  - Installs missing packages
  - Enables/disables services
  - Deploys system files
  - Removes unwanted resources (with confirmation)

- **`diff`**: Show what would change without making any modifications
  - Useful for previewing changes before applying
  - Safe to run anytime

- **`save`**: Capture current system state as Go code
  - Generates `.go` files with function calls that match your system
  - Useful after manual installations to capture them declaratively

- **`update`**: Upgrade installed packages to newer versions
  - Updates each package manager's declared packages
  - Skips packages locked to a specific version (e.g. `eslint@8.50.0`)
  - Runs a full system upgrade for pacman (nothing is version-locked)

### Package Management

#### Pacman Packages

```go
// Individual packages
fest.Package("vim", "git", "docker")

// Package groups
fest.PackageGroup("base-devel")
```

#### Flatpak Applications

```go
fest.Flatpak(
    "com.slack.Slack",
    "org.mozilla.firefox",
)
```

#### NPM Packages (Global)

```go
fest.NpmPackage(
    "typescript",
    "@vue/cli",
    "eslint@8.50.0",  // Version pinning
)
```

#### Go Packages

```go
fest.GoPackage(
    "github.com/golangci/golangci-lint/cmd/golangci-lint@latest",
    "golang.org/x/tools/cmd/goimports",
)
```

#### Ruby Gems

```go
fest.RubyGem(
    "bundler",
    "rails@7.0.0",  // Version pinning
)
```

### System Configuration

#### Timezone and NTP

```go
fest.Timedate("America/New_York", true)  // timezone, enable NTP
```

#### Locale

```go
fest.Locale("en_US.UTF-8 UTF-8")
```

#### Keyboard

```go
fest.Keyboard(
    "us",        // keymap
    "us",        // layout
    "pc105",     // model
    "",          // variant
    "ctrl:nocaps", // options
)
```

### Systemd Services

```go
// System services
fest.SystemService("docker", "sshd")
fest.SystemTimer("fstrim")
fest.SystemSocket("docker")

// User services
fest.Service("syncthing")
fest.Timer("backup")
fest.Socket("pipewire")
```

### User Groups

```go
fest.Group("docker", "wheel", "audio", "video")
```

### System Files

Place files in a `system/` directory mirroring their target paths:

```
system/
  etc/
    hosts
    pacman.conf
  usr/
    local/
      bin/
        myscript
```

```go
// Automatically discovers and manages files in system/
fest.SystemFilesDir("system")
```

### Dotfiles with GNU Stow

Place your dotfiles in `user/` directory:

```
user/
  .config/
    nvim/
      init.vim
  .bashrc
  .vimrc
```

Dotfiles are automatically managed when using `apply` or `save` commands.

### Lifecycle Hooks

Execute custom code before or after resource synchronization:

```go
// Run after docker is installed
fest.After(fest.ResourcePackages, func() {
    // Custom setup logic
})

// Run before applying configuration
fest.OnCommand(fest.PhaseBeforeApply, func() {
    // Pre-apply checks
})
```

## Architecture

### Two-Phase Execution

1. **Configuration Phase**: Builds lists of desired state by executing your Go code
2. **Synchronization Phase**: Compares current state with desired state and applies changes

### Package Manager Interface

All resource types implement the same interface:

```go
type packageManager interface {
    ResourceName() string
    Wanted() []string
    Match(want, have string) bool
    ListInstalled() ([]string, error)
    ListExplicit() ([]string, error)
    Install(pkgs []string) error
    Uninstall(pkgs []string) error
    MarkExplicit(pkgs []string) error
    GetDependencies() (map[string][]string, error)
    Update() error
    SaveAsGo(wanted []string) error
}
```

### State Tracking

- Pacman packages: Uses pacman's built-in explicit/dependency tracking
- System files: Maintains state in `~/.local/share/dotfiles/system-files-state.json`
- Dotfiles: Managed via GNU Stow
- Services: Tracked via systemd's enabled/disabled state

## Advanced Topics

### Modular Configuration

Organize your configuration into multiple files:

```
mysystem/
  main.go           # Entry point
  packages.go       # Package declarations
  services.go       # Service declarations
  system.go         # System configuration
```

Each file can have `init()` functions that declare resources:

```go
// packages.go
package main

import "github.com/emad-elsaid/fest"

func init() {
    fest.Package("vim", "git")
}
```

### Machine-Specific Configuration

Use build tags or environment variables for machine-specific config:

```go
// +build workstation

package main

import "github.com/emad-elsaid/fest"

func init() {
    fest.Package("docker", "kubectl")
}
```

### Custom Dependencies

Add custom dependency checks:

```go
fest.OnCommand(fest.PhaseBeforeApply, func() {
    // Custom validation logic
})
```

## Troubleshooting

### Dependency Errors

If you see dependency errors, ensure all required tools are installed:

```bash
go run . diff  # Will check and attempt to install missing dependencies
```

### Permission Issues

Some operations require sudo. The tool will prompt for sudo password when needed.

### State Conflicts

If the state file becomes corrupted:

```bash
rm ~/.local/share/dotfiles/system-files-state.json
go run . apply  # Rebuilds state
```

## Comparison to Other Tools

| Feature             | fest      | Ansible | NixOS |
|---------------------|-----------|---------|-------|
| Language            | Go        | YAML    | Nix   |
| Type Safety         | ✓         | ✗       | ✓     |
| Compilation         | ✓         | ✗       | ✓     |
| Arch-Specific       | ✓         | ✗       | ✗     |
| Requires New Distro | ✗         | ✗       | ✓     |
| Learning Curve      | Low       | Medium  | High  |

## Contributing

Contributions are welcome! This framework is designed to be extended with new package managers and resource types.

To add a new resource manager:

1. Implement the `packageManager` interface
2. Add it to `allManagers()` in `main.go`
3. Create public functions for users to declare resources

## Migration from Ruby Version

If you're migrating from the original Ruby implementation:

1. The API is very similar but uses Go syntax instead of Ruby
2. Replace `require` statements with `import`
3. Replace `do` blocks with `func init()` functions
4. The command structure is the same (`apply`, `save`, `diff`, `update`)

## License

This project is licensed under the GNU General Public License v3.0 - see the [LICENSE](LICENSE) file for details.

This project incorporates code from [yay](https://github.com/Jguer/yay) which is also licensed under GPL v3.

