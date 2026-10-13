//go:build e2e

// Package e2e runs the end-to-end test for fest: it boots a stock Arch Linux
// virtual machine with Vagrant, applies a configuration inside it and asserts
// the resulting system state.
//
// Nothing here is mocked: real pacman, real systemd, real timedatectl and
// localectl. Every other test in the repository runs against fake command
// output, so this is the only test that proves fest actually drives a system.
//
// Run it with the e2e build tag and a generous timeout:
//
//	go test -tags e2e -timeout 45m -v ./e2e/
//
// Set FEST_E2E_KEEP=1 to leave the VM running afterwards for debugging.
package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// vmRepoDir is where provision.sh unpacks the repository inside the VM.
const vmRepoDir = "/home/vagrant/fest"

// configDir is the configuration directory fest runs from: it holds the user/
// and system/ trees fest reads. USER and HOME are set explicitly because a
// non-login SSH command does not always export them.
const configDir = vmRepoDir + "/e2e/config"

// applyCmd builds the e2e configuration inside the VM and applies it. Stdin is
// /dev/null so fest's interactive prompts (for example "uninstall these
// packages?") take their non-interactive default instead of blocking.
const applyCmd = "cd " + vmRepoDir + " && go build -o /tmp/fest-e2e ./e2e/config && " +
	"cd " + configDir + " && USER=vagrant HOME=/home/vagrant /tmp/fest-e2e apply </dev/null"

// markers that must not appear a second time, so the test doubles as an
// idempotency check: applying an already-satisfied configuration does nothing.
var changeMarkers = []string{
	"Installing packages",
	"Setting timezone",
	"Setting locale",
	"Setting keyboard",
	"Adding user to group",
}

func TestEndToEnd(t *testing.T) {
	if _, err := exec.LookPath("vagrant"); err != nil {
		t.Skip("vagrant is not installed; skipping VM end-to-end test")
	}

	root := repoRoot(t)
	env := append(os.Environ(), "FEST_SRC_TARBALL="+packRepo(t, root))

	// run invokes vagrant from the directory holding the Vagrantfile.
	run := func(args ...string) ([]byte, error) {
		cmd := exec.Command("vagrant", args...)
		cmd.Dir = filepath.Join(root, "e2e")
		cmd.Env = env
		return cmd.CombinedOutput()
	}
	must := func(args ...string) []byte {
		t.Helper()
		out, err := run(args...)
		if err != nil {
			t.Fatalf("vagrant %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return out
	}

	t.Cleanup(func() {
		if os.Getenv("FEST_E2E_KEEP") != "" {
			t.Log("FEST_E2E_KEEP is set; leaving the VM running")
			return
		}
		if out, err := run("destroy", "-f"); err != nil {
			t.Logf("vagrant destroy failed: %v\n%s", err, out)
		}
	})

	t.Log("booting the VM and provisioning it (the first run downloads the box and can take several minutes)")
	must("up")

	t.Log("building the configuration and applying it inside the VM")
	must("ssh", "-c", applyCmd)

	t.Log("verifying the resulting system state")
	must("ssh", "-c", "bash "+vmRepoDir+"/e2e/verify.sh")

	t.Log("applying a second time to confirm the configuration is idempotent")
	out, err := run("ssh", "-c", applyCmd)
	if err != nil {
		t.Fatalf("second apply failed: %v\n%s", err, out)
	}
	for _, marker := range changeMarkers {
		if strings.Contains(string(out), marker) {
			t.Errorf("second apply was not idempotent: it still did %q\n%s", marker, out)
		}
	}
}

// repoRoot returns the repository root, one level above this package.
func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatalf("locating the repository root: %v", err)
	}
	return root
}

// packRepo tars the working tree so the VM builds the code under test, not a
// checked-in copy or a clone.
func packRepo(t *testing.T, root string) string {
	t.Helper()
	tarball := filepath.Join(t.TempDir(), "fest-src.tar.gz")
	out, err := exec.Command("tar",
		"--exclude=./.git",
		"--exclude=./e2e/.vagrant",
		"-czf", tarball,
		"-C", root, ".",
	).CombinedOutput()
	if err != nil {
		t.Fatalf("packing the repository: %v\n%s", err, out)
	}
	return tarball
}
