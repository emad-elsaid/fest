package yay

import (
	"context"
	"fmt"
	"os"

	"github.com/emad-elsaid/fest/yay/pkg/db"
	"github.com/emad-elsaid/fest/yay/pkg/db/ialpm"
	"github.com/emad-elsaid/fest/yay/pkg/runtime"
	"github.com/emad-elsaid/fest/yay/pkg/settings"
	"github.com/emad-elsaid/fest/yay/pkg/settings/parser"
	"github.com/emad-elsaid/fest/yay/pkg/text"
)

// Client provides a high-level API for yay operations
type Client struct {
	logger     *text.Logger
	cfg        *settings.Configuration
	run        *runtime.Runtime
	dbExecutor db.Executor
	args       []string // Arguments to pass to install operations
}

// NewClient creates a new yay client for programmatic use
// args are passed to the install command (e.g., "needed", "noconfirm")
func NewClient(args ...string) (*Client, error) {
	InitGotext()

	logger := text.NewLogger(os.Stdout, os.Stderr, os.Stdin, false, "yay")

	configPath := settings.GetConfigPath()
	cfg, err := settings.NewConfig(logger, configPath, YayVersion)
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}

	// Ensure AUR URLs are properly set
	if cfg.AURURL == "" {
		cfg.AURURL = "https://aur.archlinux.org"
	}
	if cfg.AURRPCURL == "" {
		cfg.AURRPCURL = cfg.AURURL + "/rpc?"
	}

	// Create default arguments for library use
	cmdArgs := parser.MakeArguments()

	run, err := runtime.NewRuntime(cfg, cmdArgs, YayVersion)
	if err != nil {
		return nil, fmt.Errorf("failed to create runtime: %w", err)
	}

	dbExecutor, err := ialpm.NewExecutor(run.PacmanConf, run.Logger.Child("db"))
	if err != nil {
		return nil, fmt.Errorf("failed to create db executor: %w", err)
	}

	return &Client{
		logger:     logger,
		cfg:        cfg,
		run:        run,
		dbExecutor: dbExecutor,
		args:       args,
	}, nil
}

// Close cleans up resources
func (c *Client) Close() {
	if c.dbExecutor != nil {
		c.dbExecutor.Cleanup()
	}
}

// Install installs one or more packages (from repos or AUR)
func (c *Client) Install(ctx context.Context, packages []string) error {
	if len(packages) == 0 {
		return nil
	}

	// Create arguments for sync operation
	cmdArgs := parser.MakeArguments()
	cmdArgs.AddArg("S", "sync")

	// Add user-provided args
	for _, arg := range c.args {
		cmdArgs.AddArg(arg)
	}

	cmdArgs.Targets = packages

	return syncInstall(ctx, c.run, cmdArgs, c.dbExecutor)
}

// Sysupgrade upgrades every installed package (both official repos and AUR) to
// its latest version, refreshing the package databases first.
func (c *Client) Sysupgrade(ctx context.Context) error {
	cmdArgs := parser.MakeArguments()
	cmdArgs.AddArg("S", "sync")
	cmdArgs.AddArg("y", "refresh")
	cmdArgs.AddArg("u", "sysupgrade")

	// Add user-provided args
	for _, arg := range c.args {
		cmdArgs.AddArg(arg)
	}

	return syncInstall(ctx, c.run, cmdArgs, c.dbExecutor)
}
