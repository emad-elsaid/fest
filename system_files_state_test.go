package fest

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestSysFilesStateBugLoadStateReportsCorruptStateFile documents that a
// corrupted (unparseable) state file must surface as an error instead of being
// silently replaced by an empty state.
//
// Bug: loadState swallowed json.Unmarshal failures and returned a brand-new
// empty state together with a nil error. The error return exists precisely so
// callers can tell "no state yet" apart from "state is broken", and both callers
// rely on it:
//   - Install only starts fresh (and warns) when loadState returns an error.
//     With a nil error it treats every file as untracked and backs up the
//     *currently deployed* content over the true original backup - destroying
//     the ability to ever restore the original.
//   - Uninstall bails out on error but with a nil error proceeds, finds nothing
//     tracked, and then saveState overwrites the corrupt file, permanently
//     forgetting every managed file.
func TestSysFilesStateBugLoadStateReportsCorruptStateFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	require.NoError(t, os.MkdirAll(stateDir(), 0o755))
	require.NoError(t, os.WriteFile(statePath(), []byte(`{"version":1,"files":{`), 0o644))

	state, err := loadState()
	require.Error(t, err,
		"a corrupt state file must be reported, not silently discarded as empty")
	require.Nil(t, state,
		"no state should be returned when the state file cannot be decoded")
}

// TestSysFilesStateBugStateDirRelativeWhenHomeUndefined documents that the
// state directory must be an absolute path in the user's home even when
// $HOME cannot be resolved.
//
// Bug: stateDir discarded the error from os.UserHomeDir(), so when HOME was
// unset the home part was the empty string and filepath.Join yielded the
// *relative* path ".local/share/dotfiles". State and backups were then read and
// written relative to the current working directory, so running fest from a
// different directory silently saw no previous state (and scattered new state
// files across directories). Every other UserHomeDir caller in the codebase
// handles the error.
func TestSysFilesStateBugStateDirRelativeWhenHomeUndefined(t *testing.T) {
	t.Setenv("HOME", "")

	dir := stateDir()
	require.True(t, filepath.IsAbs(dir),
		"state directory must be absolute even when $HOME is unavailable, got %q", dir)
}
