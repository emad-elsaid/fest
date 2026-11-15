package fest

import (
	"fmt"
	"log/slog"
	"os"
	"regexp"

	"github.com/emad-elsaid/fest/stow"
	"github.com/emad-elsaid/types"
	"github.com/manifoldco/promptui"
)

// userStow manages the user/ dotfile tree with the pure Go stow implementation.
type userStow struct {
	// choose prompts the user to pick one of items. Tests replace it.
	choose func(label string, items []string) (string, error)
}

func newUserStow() userStow {
	return userStow{choose: choosePrompt}
}

func choosePrompt(label string, items []string) (string, error) {
	prompt := promptui.Select{Label: label, Items: items}
	_, selected, err := prompt.Run()
	return selected, err
}

// workingDir returns the current working directory. PWD is preferred over
// os.Getwd so a symlinked working directory is preserved instead of resolved.
func workingDir() (string, error) {
	if pwd := os.Getenv("PWD"); pwd != "" {
		return pwd, nil
	}
	return os.Getwd()
}

// getDirs returns the dotfiles directory and the stow target directory.
func (u userStow) getDirs() (dotfilesDir, targetDir string, err error) {
	dotfilesDir, err = workingDir()
	if err != nil {
		return "", "", err
	}

	targetDir, err = os.UserHomeDir()
	if err != nil {
		return "", "", err
	}

	return dotfilesDir, targetDir, nil
}

// options builds the stow options used by Apply and Diff (and, with adopt set,
// by Save) so the preview always matches what apply will do.
func (u userStow) options(dotfilesDir, targetDir string, adopt bool) stow.Options {
	return stow.Options{
		StowDir:   dotfilesDir,
		TargetDir: targetDir,
		Adopt:     adopt,
		Override:  []*regexp.Regexp{regexp.MustCompile(".*")},
	}
}

// Apply stows the user/ tree into the home directory, prompting to resolve any
// target files that would be overwritten.
func (u userStow) Apply() error {
	dotfilesDir, targetDir, err := u.getDirs()
	if err != nil {
		return err
	}

	opts := u.options(dotfilesDir, targetDir, false)
	if err := u.resolveConflicts("user", &opts); err != nil {
		return fmt.Errorf("failed to handle conflicts: %w", err)
	}

	plan, err := stow.Stow("user", opts)
	if err != nil {
		return fmt.Errorf("failed to apply dotfiles: %w", err)
	}

	if len(plan.Actions) == 0 {
		logSuccess("dotfiles: up to date")
		return nil
	}

	logSuccess("dotfiles: applied")
	return nil
}

// Save adopts files from the home directory into the user/ tree, so local
// edits are captured as dotfiles.
func (u userStow) Save() error {
	dotfilesDir, targetDir, err := u.getDirs()
	if err != nil {
		return err
	}

	opts := u.options(dotfilesDir, targetDir, true)
	if err := u.resolveConflicts("user", &opts); err != nil {
		return fmt.Errorf("failed to handle conflicts: %w", err)
	}

	plan, err := stow.Stow("user", opts)
	if err != nil {
		return fmt.Errorf("adoption failed: %w", err)
	}

	if len(plan.Actions) == 0 {
		logSuccess("dotfiles: up to date")
		return nil
	}

	logSuccess("dotfiles: adopted")
	return nil
}

// Diff prints the operations stowing would perform without changing anything.
func (u userStow) Diff() error {
	dotfilesDir, targetDir, err := u.getDirs()
	if err != nil {
		return err
	}

	plan, err := stow.PlanStow("user", u.options(dotfilesDir, targetDir, false))
	if err != nil {
		return err
	}

	for _, act := range plan.Actions {
		slog.Info(act.String())
	}
	for _, c := range plan.Conflicts {
		slog.Warn("Conflict: "+c.RelPath, "reason", c.Reason)
	}

	if len(plan.Actions) == 0 && len(plan.Conflicts) == 0 {
		logSuccess("dotfiles: no changes")
	}
	return nil
}

// resolveConflicts prompts for every conflict in a simulated stow run, handling
// the chosen resolution and recording skipped paths in opts.Skip.
func (u userStow) resolveConflicts(pkg string, opts *stow.Options) error {
	simulated := *opts
	simulated.Simulate = true

	plan, err := stow.PlanStow(pkg, simulated)
	if err != nil {
		return err
	}
	if len(plan.Conflicts) == 0 {
		return nil
	}

	slog.Info("Found conflicting files", "count", len(plan.Conflicts))

	for _, conflict := range plan.Conflicts {
		keep, err := u.handleConflict(conflict)
		if err != nil {
			return fmt.Errorf("failed to handle conflict for %s: %w", conflict.RelPath, err)
		}
		if keep {
			if opts.Skip == nil {
				opts.Skip = make(map[string]bool)
			}
			opts.Skip[conflict.RelPath] = true
		}
	}

	return nil
}

// handleConflict asks the user how to resolve a single conflict. It returns
// true when the target should be left untouched. Overwriting is not offered for
// directories, which would require deleting a whole tree.
func (u userStow) handleConflict(conflict stow.Conflict) (bool, error) {
	items := []string{"Overwrite", "Show diff", "Keep target"}
	if fi, err := os.Lstat(conflict.TargetPath); err == nil && fi.IsDir() {
		items = []string{"Show diff", "Keep target"}
	}

	for {
		selected, err := u.choose(fmt.Sprintf("Conflict: %s", conflict.TargetPath), items)
		if err != nil {
			return false, err
		}

		switch selected {
		case "Overwrite":
			if err := os.Remove(conflict.TargetPath); err != nil && !os.IsNotExist(err) {
				return false, fmt.Errorf("failed to remove: %w", err)
			}
			slog.Info("Removed conflicting file", "path", conflict.TargetPath)
			return false, nil
		case "Show diff":
			checkWarn(u.showDiff(conflict.TargetPath, conflict.SourcePath), "Failed to show diff")
			continue
		case "Keep target":
			slog.Info("Keeping target file", "file", conflict.TargetPath)
			return true, nil
		}
	}
}

// showDiff prints a unified diff between the stowed file and its target.
func (u userStow) showDiff(targetPath, stowPath string) error {
	stowInfo, err := os.Lstat(stowPath)
	if os.IsNotExist(err) {
		slog.Info("File doesn't exist in stow directory yet (new file)")
		return nil
	}
	if err != nil {
		return err
	}

	targetInfo, err := os.Lstat(targetPath)
	if err != nil {
		return err
	}

	if targetInfo.IsDir() || stowInfo.IsDir() {
		slog.Info("Cannot diff directories")
		return nil
	}

	// diff returns non-zero when files differ, so we ignore the error
	_ = types.Cmd("diff", "-u", stowPath, targetPath).Interactive().Run()

	return nil
}
