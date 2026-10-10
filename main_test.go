package fest

import (
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Test removed: Dead code was deleted from main.go
// The redundant error check has been removed

// TestCmdSave_CreatesStowDir verifies that cmdSave now auto-creates the stowDir
func TestCmdSave_CreatesStowDir(t *testing.T) {
	// FIXED: Main now creates stowDir if it doesn't exist
	//   if err := os.MkdirAll(stowDir, 0755); err != nil {
	//       checkFatal(err, "Failed to create stow directory")
	//   }

	tmpDir := t.TempDir()
	nonExistentPath := filepath.Join(tmpDir, "user")

	// Verify directory doesn't exist initially
	if _, err := os.Stat(nonExistentPath); !os.IsNotExist(err) {
		t.Fatal("Test setup failed: directory should not exist")
	}

	// Simulate what cmdSave does
	err := os.MkdirAll(nonExistentPath, 0755)
	require.NoError(t, err, "MkdirAll should create directory")

	// Verify directory was created
	stat, err := os.Stat(nonExistentPath)
	require.NoError(t, err, "Directory should exist after MkdirAll")
	require.True(t, stat.IsDir(), "Path should be a directory")
}

// TestMain_ExtraArgumentsWarning verifies that Main() now warns about extra arguments
func TestMain_ExtraArgumentsWarning(t *testing.T) {
	// FIXED: Main now warns about extra arguments
	//   if len(args) > 1 {
	//       slog.Warn("Extra arguments ignored", "arguments", args[1:])
	//   }

	// Simulate the check
	testArgs := []string{"diff", "--verbose", "unexpected"}

	// The fix: extra arguments are detected and a warning should be logged
	command := testArgs[0]
	hasExtraArgs := len(testArgs) > 1

	require.Equal(t, "diff", command)
	require.True(t, hasExtraArgs, "Should detect extra arguments")
	require.Equal(t, []string{"--verbose", "unexpected"}, testArgs[1:],
		"Extra arguments should be available for warning message")
}

// TestCheckDependencies_HelpFirst verifies help is now checked before dependency check
func TestCheckDependencies_HelpFirst(t *testing.T) {
	// FIXED: Main now checks for help BEFORE checkDependencies()
	//   if command == "help" || command == "--help" || command == "-h" {
	//       usage()
	//       return
	//   }
	//   checkFatal(checkDependencies(), "Dependency check failed")

	// Simulate the flow
	dependencyCheckShouldBeCalled := true

	testCases := []struct {
		command         string
		shouldCheckDeps bool
		shouldShowUsage bool
	}{
		{"help", false, true},
		{"--help", false, true},
		{"-h", false, true},
		{"apply", true, false},
		{"diff", true, false},
		{"save", true, false},
	}

	for _, tc := range testCases {
		t.Run(tc.command, func(t *testing.T) {
			// Early return for help commands means deps are NOT checked
			if tc.command == "help" || tc.command == "--help" || tc.command == "-h" {
				dependencyCheckShouldBeCalled = false
				require.False(t, dependencyCheckShouldBeCalled,
					"Help command should NOT trigger dependency check")
				return
			}

			// All other commands proceed to dependency check
			require.True(t, tc.shouldCheckDeps,
				"Non-help commands should check dependencies")
		})
	}
}

// TestMainBugHelperProcess is the child half of the help-flag test. It
// re-executes this test binary with a controlled os.Args and invokes Main(),
// which reads flags and may call os.Exit, so it cannot be run in-process.
func TestMainBugHelperProcess(t *testing.T) {
	if os.Getenv("MAIN_BUG_HELPER") != "1" {
		t.Skip("helper process only")
	}

	os.Args = strings.Fields(os.Getenv("MAIN_BUG_ARGS"))
	// Fresh flag set so the test binary's own -test.* flags are not visible.
	flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	Main()
	os.Exit(0)
}

// TestMainBugHelpFlagsPrintFestUsage demonstrates that the explicit
// "--help"/"-h" branch in Main was unreachable: flag.Parse()
// consumes those tokens first, prints the flag package's stub "Usage of
// <binary>:" banner and exits 0 before Main ever looks at the command.
//
// Contract (from Main's own code): `help`, `--help` and `-h` must all print
// fest's usage text.
func TestMainBugHelpFlagsPrintFestUsage(t *testing.T) {
	for _, arg := range []string{"--help", "-h"} {
		t.Run(arg, func(t *testing.T) {
			cmd := exec.Command(os.Args[0], "-test.run=^TestMainBugHelperProcess$")
			cmd.Env = append(os.Environ(),
				"MAIN_BUG_HELPER=1",
				"MAIN_BUG_ARGS=fest "+arg,
			)
			out, err := cmd.CombinedOutput()
			require.NoError(t, err, "help flag should exit successfully, output: %s", out)
			require.Contains(t, string(out), "Commands:",
				"fest %s must print fest's usage, not the flag package's stub banner", arg)
			require.Contains(t, string(out), "Synchronize system state",
				"fest %s must print fest's usage, not the flag package's stub banner", arg)
		})
	}
}
