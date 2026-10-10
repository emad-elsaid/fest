package fest

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// SystemdBugFakeSystemctl writes a fake "systemctl" onto PATH. It keeps the set
// of enabled units in a state file so cache behaviour can be observed without
// root or a real systemd instance.
func SystemdBugFakeSystemctl(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	state := filepath.Join(dir, "state")
	require.NoError(t, os.WriteFile(state, []byte("foo.service enabled\n"), 0644))

	script := "#!/bin/sh\n" +
		fmt.Sprintf("STATE=%q\n", state) +
		"[ \"$1\" = \"--user\" ] && shift\n" +
		"case \"$1\" in\n" +
		"  list-unit-files) [ -f \"$STATE\" ] && cat \"$STATE\" ;;\n" +
		"  enable)\n" +
		"    shift\n" +
		"    for u in \"$@\"; do\n" +
		"      case \"$u\" in --*) continue ;; esac\n" +
		"      printf '%s enabled\\n' \"$u\" >> \"$STATE\"\n" +
		"    done ;;\n" +
		"esac\n" +
		"exit 0\n"

	require.NoError(t, os.WriteFile(filepath.Join(dir, "systemctl"), []byte(script), 0755))
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return state
}

// TestSystemdBugInstallInvalidatesCache demonstrates that a systemd manager's
// memoized installed list is never invalidated by Install/Uninstall, so the
// "refresh after install" in syncPackages silently returns stale data.
func TestSystemdBugInstallInvalidatesCache(t *testing.T) {
	SystemdBugFakeSystemctl(t)

	mgr := &systemdManager{
		resource: ResourceUserServices,
		unitType: "service",
		user:     true,
		wanted:   &[]string{"foo", "bar"},
	}

	before, err := mgr.ListInstalled()
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"foo"}, before)

	require.NoError(t, mgr.Install([]string{"bar"}))

	after, err := mgr.ListInstalled()
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"foo", "bar"}, after,
		"ListInstalled must reflect units enabled by Install; the installed list cache has to be invalidated by mutations")
}
