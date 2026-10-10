package fest

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// FlatpakBugFake installs a fake `flatpak` at the front of PATH whose
// `list` subcommand prints listOut verbatim. --version succeeds.
func FlatpakBugFake(t *testing.T, listOut string) {
	t.Helper()

	dir := t.TempDir()
	listFile := filepath.Join(dir, "list.out")
	require.NoError(t, os.WriteFile(listFile, []byte(listOut), 0644))

	script := fmt.Sprintf(`#!/bin/sh
if [ "$1" = "--version" ]; then echo "Flatpak 1.14.4"; exit 0; fi
if [ "$1" = "list" ]; then cat %q; exit 0; fi
exit 0
`, listFile)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "flatpak"), []byte(script), 0755))

	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// TestFlatpakBugDeclareRejectsEmptyIDs documents that Flatpak must reject
// empty and whitespace-only application IDs, exactly like NpmPackage does
// (npm_packages.go: NpmPackage filters strings.TrimSpace(pkg) != "").
//
// Flatpak() currently forwards every argument straight to addUnique. An empty
// entry therefore enters wantedFlatpak. Downstream that is not merely a wasted
// `flatpak install ... ""`:
//   - Match("", installed) is always false, so subtract() believes the empty
//     ID is missing and Install() runs `flatpak install -y flathub ""`;
//   - unwantedResources() likewise fails to match every installed app against
//     the empty wanted entry, so `fest apply` offers to uninstall ALL installed
//     flatpak apps.
func TestFlatpakBugDeclareRejectsEmptyIDs(t *testing.T) {
	old := wantedFlatpak
	wantedFlatpak = nil
	defer func() { wantedFlatpak = old }()

	Flatpak("com.slack.Slack", "", "   ", "\t", "\n", "org.mozilla.firefox")

	require.Equal(t, []string{"com.slack.Slack", "org.mozilla.firefox"}, wantedFlatpak,
		"empty/whitespace-only app IDs must be filtered out of the declaration")
}

// TestFlatpakBugListInstalledDedupes documents that ListInstalled must return
// each installed application once.
//
// `flatpak list --app --columns=application` prints one line per installed ref,
// so an application installed on more than one branch (e.g. stable + beta),
// architecture, or installation (user + system) produces the same application
// ID on several lines. ListInstalled returns those lines verbatim, so the value
// is not a set. Callers treat it as one:
//   - unwantedResources() then emits the same app twice in toUninstall, giving
//     a bogus "count=2" and a second `flatpak uninstall` that fails after the
//     first succeeded;
//   - SaveAsGo() writes duplicate fest.Flatpak(...) entries.
func TestFlatpakBugListInstalledDedupes(t *testing.T) {
	FlatpakBugFake(t, "com.a.A\ncom.b.B\ncom.a.A\n")

	got, err := flatpak{}.ListInstalled()
	require.NoError(t, err)
	require.Equal(t, []string{"com.a.A", "com.b.B"}, got,
		"installed resources are identified by application ID, so duplicate IDs must be collapsed")
}
