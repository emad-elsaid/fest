# Agent Guide: fest

A declarative system configuration management framework for Arch Linux written in Go.
Users declare their desired system state (packages, services, files, configs) as Go code,
and `fest` syncs the actual system to match. It is imported as a library by the user's
program, with `fest.Main()` as the entry point.

## Build & Run Commands

### Standard Operations
```bash
# Run the CLI (primary usage pattern)
go run . diff        # Preview changes without applying
go run . apply       # Synchronize system to match declared configuration
go run . update      # Upgrade installed packages, skipping version-locked ones
go run . save        # Save current system state as Go code

# Build binary
go build             # Outputs ./fest
go build -o mysystem # Custom binary name

# Dependency management
go mod tidy          # Clean and update dependencies
go mod download      # Download dependencies without installing
go get -u ./...      # Update all dependencies
```

### Testing
Tests exist throughout the module (48 `*_test.go` files). They use
`testify/require` (direct dependency); `gock` (HTTP mocking) and `cupaloy`
(snapshot testing) appear in some tests. Follow Go table-driven conventions:
```bash
go test ./...                    # Run all tests
go test -v ./...                 # Verbose output
go test -run TestFuncName        # Run single test function
go test -run TestFuncName/SubTest # Run specific subtest
go test ./yay/...                # Run vendored yay tests
go test -race ./...              # Run with race detector
go test -cover ./...             # Show coverage
```

### Code Quality
```bash
gofmt -w .           # Format all Go files
go vet ./...         # Run Go's built-in linter
go mod verify        # Verify dependencies
```

## Project Structure

```
/home/emad/code/fest/
├── main.go               # Entry point: Fest.Main(), CLI commands (apply/save/diff)
├── interface.go          # Core packageManager interface, Before/After/OnCommand
├── common.go             # Shared utilities (addUnique, askYesNo, subtract, intersect)
├── dependencies.go       # Dependency (tool) checking and installation
├── logger.go             # Custom slog pretty handler + log helpers
├── pacman.go             # pacman package manager (incl. AUR via embedded yay)
├── flatpak.go            # Flatpak application manager
├── go_packages.go        # Go package manager (go install)
├── npm_packages.go       # npm global package manager
├── ruby_gems.go          # Ruby gem manager
├── systemd.go            # Systemd service/timer/socket manager
├── system_config.go      # System configuration (timezone, locale, keyboard)
├── system_files.go       # Deploy and track system configuration files
├── system_files_state.go # State tracking for system files
├── user_groups.go        # User group membership management
├── user_stow.go          # GNU Stow dotfile manager
├── user_symlinks.go      # Broken symlink cleanup
├── yay/                  # Vendored copy of yay (AUR helper), GPL v3
│   └── pkg/              # yay internal subpackages (db, dep, settings, sync, ...)
├── docs/                 # GitHub Pages site (index.html, icon.svg, CNAME)
├── example/              # Example user configuration (uses a replace directive)
├── go.mod                # Module definition (go 1.26) - github.com/emad-elsaid/fest
└── *_test.go             # Table-driven tests alongside source files
```

## Code Style Guidelines

### Package & Imports
- Package: `package fest`
- Group imports: stdlib, external, then local (with blank lines between groups)
- Use `github.com/emad-elsaid/types` for command execution (`types.Cmd()`)
- Use `github.com/samber/lo` for functional utilities (ContainsBy, Filter, Reject, Without)

### Naming Conventions
- **Types**: Descriptive types with clear purpose (e.g., `packageManager`, `ResourceName`, `CommandPhase`, `Callback`, `dependency`)
- **Constants**: PascalCase with prefix (e.g., `ResourcePackages`, `PhaseBeforeApply`)
- **Public API**: PascalCase exported functions (e.g., `Main()`, `Package()`, `Service()`)
- **Internal**: Short, descriptive lowercase names (e.g., `pm`, `mgr`, `rn`, `wg`, `wantedX`)

### Code Organization
- **Keep functions short**: Break complex logic into smaller functions
- **Interface-driven design**: All resource managers implement `packageManager` interface
- **Callback system**: Use `Before()`, `After()`, `OnCommand()` for hooks
- **Two-phase execution**: Separate diff (preview) from apply (sync)

### Error Handling
- `checkFatal(err, msg)` for unrecoverable errors during critical operations
- `checkWarn(err, msg)` for non-critical errors that shouldn't stop execution
- `logInfoIf(cond, msg, ...)` / `logIf(cond, level, msg, ...)` for conditional logging
- Return errors from functions, let caller decide severity
- Log errors with structured logging: `slog.Error()`, `slog.Warn()`

### Logging
- Use structured logging with `log/slog`
- Custom pretty handler (`newPrettyHandler`) for user-facing output
- Custom success level: `levelSuccess = slog.Level(2)`, via `logSuccess(msg, ...)`
- Log levels: `slog.Debug()`, `slog.Info()`, `slog.Warn()`, `slog.Error()`
- Include context: `slog.Info("msg", "key", value, "count", len(items))`

### Comments & Documentation
- Every exported function/type must have a godoc comment
- Comments start with the name of the thing being documented
- Inline comments for complex logic, not obvious statements

### Types
- Use `any` instead of `interface{}`
- Define type aliases for clarity (e.g., `type ResourceName string`)
- Prefer explicit types over generic interfaces

### Concurrency
- Use `sync.WaitGroup` for parallel operations
- Prefer `wg.Go()` over `wg.Add(1)` + `go func()`
- Run independent package managers in parallel during diff/save
- Run package managers sequentially during apply (for Before/After callbacks)

### Testing
- Use table-driven tests with `t.Run(name, ...)` for multi-case tests
- Use `testify/require` for assertions
- Test files: `*_test.go` alongside source files
- Test function naming: `func TestFunctionName(t *testing.T)`

### Dependencies
- Minimize external dependencies
- Prefer stdlib when possible
- Direct deps include: types, color, promptui, lo, go-version, alone,
  aur, go-alpm, votar, go-pacmanconf, go-srcinfo, gotext (yay),
  testify, gock, cupaloy (testing)

## Architecture Patterns

### packageManager Interface
All resource managers implement this interface:
- `ResourceName()` - Human-readable identifier
- `Wanted()` - User-declared resources
- `Match(want, have)` - Fuzzy matching for version flexibility
- `ListInstalled()` - Currently installed resources
- `ListExplicit()` - Explicitly installed (vs dependencies)
- `Install()`, `Uninstall()`, `MarkExplicit()` - State mutations
- `Update()` - Upgrade resources, skipping version-locked ones (nil if unsupported)
- `GetDependencies()` - Dependency graph (nil if unsupported)
- `SaveAsGo()` - Generate declarative Go code

### Callback System
- `Before(ResourceName, Callback)` - Execute before resource sync
- `After(ResourceName, Callback)` - Execute after resource sync
- `OnCommand(CommandPhase, Callback)` - Execute at command lifecycle phases
- `PhaseBeforeDiff`, `PhaseAfterDiff`, `PhaseBeforeSave`, `PhaseAfterSave`,
  `PhaseBeforeApply`, `PhaseAfterApply`

### Commands Flow
**diff**: `PhaseBeforeDiff` → parallel `diffPackages` all managers → `PhaseAfterDiff`
**save**: `PhaseBeforeSave` → parallel `mgr.SaveAsGo` all managers → `PhaseAfterSave`
**apply**: `PhaseBeforeApply` → sequential sync (with Before/After per manager) → `PhaseAfterApply`
**update**: `PhaseBeforeUpdate` → sequential `mgr.Update` skip version-locked → `PhaseAfterUpdate`

### Manager Processing Order (allManagers)
`systemConfigManager` → `pacman` → `flatpak` → `npmPackages` → `goPackages` →
`rubyGems` → `userGroups` → `symlinks` → `systemFiles` → systemd units
(user services/timers/sockets, then system services/timers/sockets).

## Common Operations

### Adding New Resource Type
1. Define a `ResourceName` constant (e.g., `ResourceFoo`)
2. Define a global slice for wanted items (e.g., `var wantedFoo []string`)
3. Create exported function to add items (e.g., `func Foo(items ...string) { addUnique(&wantedFoo, items...) }`)
4. Implement the `packageManager` interface
5. Add to `allManagers()` in main.go

### Command Execution
Use `github.com/emad-elsaid/types`:
```go
stdout, err := types.Cmd("command", "arg1", "arg2").StdoutErr()
```

### Version Handling
For versioned resources (npm, go packages, ruby gems):
- Use `splitVer()` or `splitNpmVer()` to parse package@version
- Use `matchWithVersion()` for flexible matching

## Notes for Agents
- This is a library + CLI tool meant to be imported by the user's Go program
- Users declare configuration in `init()` functions, then call `fest.Main()`
- The framework operates in phases: diff (preview) → apply (sync)
- State is tracked to enable cleanup of unwanted resources (e.g., system-files state)
- Dependencies are respected - won't remove packages that others depend on
- `yay/` is a vendored GPL v3 dependency (AUR) - keep it in sync with upstream yay
- The `docs/` directory holds a GitHub Pages site; `example/` is a sample config
  that imports `fest` via a local `replace` directive
