package fest

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/samber/lo"
	"github.com/stretchr/testify/require"
)

// BUG 1: TestListInstalled_InconsistentErrorHandling demonstrates that
// ListInstalled uses Stdout()+Error() instead of StdoutErr() which is
// used by getPrimaryGroup(). This inconsistency means error messages
// from stderr are not captured in the error.
func TestListInstalled_InconsistentErrorHandling(t *testing.T) {
	u := userGroups{}

	originalUser := os.Getenv("USER")
	defer func() {
		if originalUser != "" {
			os.Setenv("USER", originalUser)
		}
	}()

	// Set to nonexistent user to trigger error
	os.Setenv("USER", "nonexistent_user_99999")

	_, err := u.ListInstalled()

	// The command should fail
	require.Error(t, err)

	// BUG: Error message may not contain helpful stderr output
	// because ListInstalled uses Stdout() instead of StdoutErr()
	// Compare with getPrimaryGroup() which uses StdoutErr()
	t.Logf("Error message: %v", err)

	// This demonstrates inconsistent error handling pattern
	// getPrimaryGroup: stdout, err := types.Cmd(...).StdoutErr()
	// ListInstalled:   stdout := cmd.Stdout(); err := cmd.Error()
}

// BUG 2: TestGetPrimaryGroup_NoEmptyValidation demonstrates that
// getPrimaryGroup doesn't validate the output is non-empty.
// If `id -gn` returns empty string, it will be used in ListInstalled
// causing incorrect filtering.
func TestGetPrimaryGroup_NoEmptyValidation(t *testing.T) {
	u := userGroups{}

	primary, err := u.getPrimaryGroup(os.Getenv("USER"))

	if err != nil {
		t.Skip("Cannot test on this system")
	}

	// BUG: Function returns empty string without error if command
	// returns whitespace-only output
	// This should be validated: if empty, return error
	if primary == "" {
		t.Error("BUG FOUND: getPrimaryGroup returned empty string without error")
	}
}

// BUG 3: TestInstall_NoGroupValidation demonstrates that Install
// doesn't validate group names before attempting to add user.
func TestInstall_NoGroupValidation(t *testing.T) {
	u := userGroups{}

	tests := []struct {
		name       string
		groups     []string
		shouldFail bool
	}{
		{
			name:       "empty group name",
			groups:     []string{""},
			shouldFail: true,
		},
		{
			name:       "whitespace only",
			groups:     []string{"   "},
			shouldFail: true,
		},
		{
			name:       "group with spaces",
			groups:     []string{"my group"},
			shouldFail: true,
		},
	}

	originalUser := os.Getenv("USER")
	defer func() {
		if originalUser != "" {
			os.Setenv("USER", originalUser)
		}
	}()

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Verify validation catches invalid group names BEFORE calling sudo

			if originalUser == "" {
				os.Setenv("USER", "testuser")
			}

			err := u.Install(tc.groups)

			// Should get validation error, not sudo error
			if tc.shouldFail {
				require.Error(t, err, "Should reject invalid group name")
				require.Contains(t, err.Error(), "group name", "Error should mention group name validation")
			} else {
				// Valid groups would require sudo, skip for unit tests
				t.Skip("Skipping valid group test (requires sudo)")
			}
		})
	}
}

// BUG 4: TestUninstall_NoGroupValidation - same as Install
func TestUninstall_NoGroupValidation(t *testing.T) {
	u := userGroups{}

	originalUser := os.Getenv("USER")
	defer func() {
		if originalUser != "" {
			os.Setenv("USER", originalUser)
		}
	}()

	if originalUser == "" {
		os.Setenv("USER", "testuser")
	}

	// Should return validation error BEFORE calling gpasswd
	err := u.Uninstall([]string{""})

	require.Error(t, err, "Should reject empty group name")
	require.Contains(t, err.Error(), "group name", "Error should mention group name validation")
}

// BUG 5: TestUninstall_NoPrimaryGroupCheck demonstrates that Uninstall
// doesn't check if a group is the user's primary group before attempting
// removal. gpasswd -d cannot remove a user from their primary group.
func TestUninstall_NoPrimaryGroupCheck(t *testing.T) {
	u := userGroups{}

	originalUser := os.Getenv("USER")
	if originalUser == "" {
		t.Skip("USER not set")
	}
	defer os.Setenv("USER", originalUser)

	primary, err := u.getPrimaryGroup(os.Getenv("USER"))
	if err != nil {
		t.Skip("Cannot get primary group")
	}

	// BUG: Function doesn't check if group being removed is primary group
	// Expected: Should call getPrimaryGroup() and validate
	// Actual: Directly calls gpasswd -d which will fail for primary group

	// We can't test actual removal without sudo, but we can verify
	// the function doesn't do validation by checking it doesn't call
	// getPrimaryGroup()

	t.Logf("Primary group: %s", primary)
	t.Log("BUG: Uninstall() doesn't validate against primary group")
	t.Log("Calling gpasswd -d to remove primary group will fail")

	// If we tried: u.Uninstall([]string{primary})
	// It would fail with: "cannot remove user from their primary group"
}

// BUG 6: TestInstall_NoUsernameValidation demonstrates potential
// command injection via USERNAME environment variable
func TestInstall_NoUsernameValidation(t *testing.T) {
	u := userGroups{}

	originalUser := os.Getenv("USER")
	defer func() {
		if originalUser != "" {
			os.Setenv("USER", originalUser)
		}
	}()

	// BUG: Username is not validated before being passed to command
	// types.Sudo() might sanitize, but defensive programming says
	// validate input before use

	tests := []struct {
		name     string
		username string
	}{
		{"empty", ""},
		{"with semicolon", "user;echo"},
		{"with pipe", "user|cat"},
		{"with space", "my user"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			os.Setenv("USER", tc.username)

			err := u.Install([]string{"testgroup"})

			// Should get validation error for invalid username
			require.Error(t, err, "Should reject invalid username: %q", tc.username)
			// Error message varies: empty -> "USER env var not set", invalid -> "invalid username format"
		})
	}
}

// BUG 7: TestUninstall_NoUsernameValidation - same as Install
func TestUninstall_NoUsernameValidation(t *testing.T) {
	u := userGroups{}

	originalUser := os.Getenv("USER")
	defer func() {
		if originalUser != "" {
			os.Setenv("USER", originalUser)
		}
	}()

	os.Setenv("USER", "user;id")

	err := u.Uninstall([]string{"testgroup"})

	// Should get validation error for invalid username
	require.Error(t, err, "Should reject invalid username")
}

// NOT A BUG: Test to verify Group() deduplication works correctly
func TestGroup_Deduplication(t *testing.T) {
	originalGroups := groups
	defer func() { groups = originalGroups }()

	groups = []string{}

	Group("docker", "wheel")
	require.Equal(t, []string{"docker", "wheel"}, groups)

	Group("wheel", "audio")
	require.Equal(t, []string{"docker", "wheel", "audio"}, groups)

	require.Equal(t, len(groups), len(lo.Uniq(groups)))
}

// NOT A BUG: Verify SaveAsGo logic is correct
func TestSaveAsGo_LogicCorrect(t *testing.T) {
	installed := []string{"docker", "wheel", "audio"}
	wanted := []string{"docker"}

	// SaveAsGo finds groups installed but not in wanted
	// to save them to file
	diff := lo.Without(installed, wanted...)

	require.ElementsMatch(t, []string{"wheel", "audio"}, diff)
}

// Test basic functionality
func TestUserGroups_BasicFunctionality(t *testing.T) {
	u := userGroups{}

	require.Equal(t, "user groups", u.ResourceName())
	require.True(t, u.Match("docker", "docker"))
	require.False(t, u.Match("docker", "wheel"))

	err := u.MarkExplicit([]string{"test"})
	require.NoError(t, err)

	deps, err := u.GetDependencies()
	require.NoError(t, err)
	require.Nil(t, deps)
}

// Test ListInstalled with missing USER env
func TestListInstalled_MissingUserEnv(t *testing.T) {
	u := userGroups{}

	originalUser := os.Getenv("USER")
	defer func() {
		if originalUser != "" {
			os.Setenv("USER", originalUser)
		}
	}()

	os.Unsetenv("USER")

	_, err := u.ListInstalled()
	require.Error(t, err)
	require.Contains(t, err.Error(), "USER env var not set")
}

// Test Install with missing USER env
func TestInstall_MissingUserEnv(t *testing.T) {
	u := userGroups{}

	originalUser := os.Getenv("USER")
	defer func() {
		if originalUser != "" {
			os.Setenv("USER", originalUser)
		}
	}()

	os.Unsetenv("USER")

	err := u.Install([]string{"testgroup"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "USER env var not set")
}

// Test Uninstall with missing USER env
func TestUninstall_MissingUserEnv(t *testing.T) {
	u := userGroups{}

	originalUser := os.Getenv("USER")
	defer func() {
		if originalUser != "" {
			os.Setenv("USER", originalUser)
		}
	}()

	os.Unsetenv("USER")

	err := u.Uninstall([]string{"testgroup"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "USER env var not set")
}

// Test getPrimaryGroup returns trimmed result
func TestGetPrimaryGroup_Trimmed(t *testing.T) {
	u := userGroups{}

	primary, err := u.getPrimaryGroup(os.Getenv("USER"))
	if err != nil {
		t.Skip("Cannot test on this system")
	}

	require.Equal(t, strings.TrimSpace(primary), primary)
	require.NotEmpty(t, primary)
}

// Test ListInstalled filters primary group correctly
func TestListInstalled_FiltersPrimaryGroup(t *testing.T) {
	u := userGroups{}

	originalUser := os.Getenv("USER")
	if originalUser == "" {
		t.Skip("USER not set")
	}
	defer os.Setenv("USER", originalUser)

	groups, err := u.ListInstalled()
	require.NoError(t, err)

	primary, err := u.getPrimaryGroup(os.Getenv("USER"))
	require.NoError(t, err)

	// Primary group should be filtered out
	require.NotContains(t, groups, primary)
}

// Test Wanted returns global groups
func TestWanted_ReturnsGlobal(t *testing.T) {
	originalGroups := groups
	defer func() { groups = originalGroups }()

	groups = []string{"test1", "test2"}

	u := userGroups{}
	require.Equal(t, groups, u.Wanted())
}

// UserGroupsBugCurrentPrimary returns the primary group name of the current process.
func UserGroupsBugCurrentPrimary(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("id", "-gn").Output()
	require.NoError(t, err)
	return strings.TrimSpace(string(out))
}

// UserGroupsBugPickTargetUser scans /etc/passwd for a user whose primary group
// differs from currentPrimary. Returns the username and its primary group.
func UserGroupsBugPickTargetUser(t *testing.T, currentPrimary string) (string, string) {
	t.Helper()
	data, err := os.ReadFile("/etc/passwd")
	if err != nil {
		t.Skip("cannot read /etc/passwd")
	}
	for _, line := range strings.Split(string(data), "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Split(line, ":")
		if len(fields) < 4 {
			continue
		}
		name := fields[0]
		out, err := exec.Command("id", "-gn", name).Output()
		if err != nil {
			continue
		}
		primary := strings.TrimSpace(string(out))
		if primary == "" || primary == currentPrimary {
			continue
		}
		// The primary group must actually be reported by `id -Gn <user>`.
		gnOut, err := exec.Command("id", "-Gn", name).Output()
		if err != nil {
			continue
		}
		if !strings.Contains(" "+strings.TrimSpace(string(gnOut))+" ", " "+primary+" ") {
			continue
		}
		return name, primary
	}
	t.Skip("no alternate user found on this system")
	return "", ""
}

// TestUserGroupsBugListInstalledFiltersWrongPrimaryGroup demonstrates that
// ListInstalled filters out the *current process's* primary group instead of
// the primary group of the user named by $USER. Since it lists the groups of
// $USER via `id -Gn $USER`, it must exclude $USER's primary group — that group
// cannot be managed with `usermod -aG` / `gpasswd -d`.
func TestUserGroupsBugListInstalledFiltersWrongPrimaryGroup(t *testing.T) {
	currentPrimary := UserGroupsBugCurrentPrimary(t)
	targetUser, targetPrimary := UserGroupsBugPickTargetUser(t, currentPrimary)

	originalUser, hadUser := os.LookupEnv("USER")
	defer func() {
		if hadUser {
			os.Setenv("USER", originalUser)
		} else {
			os.Unsetenv("USER")
		}
	}()

	os.Setenv("USER", targetUser)

	installed, err := userGroups{}.ListInstalled()
	require.NoError(t, err)

	require.NotContains(t, installed, targetPrimary,
		"ListInstalled($USER=%q) leaked %q's primary group %q because it filtered the current process's primary group %q instead",
		targetUser, targetUser, targetPrimary, currentPrimary)
}
