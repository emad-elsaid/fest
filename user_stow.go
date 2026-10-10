package fest

import "github.com/emad-elsaid/types"

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"

	"github.com/manifoldco/promptui"
	"github.com/samber/lo"
)

type userStow struct{}

func newUserStow() userStow {
	return userStow{}
}

func (u userStow) getDirs() (dotfilesDir, stowDir, targetDir string, err error) {
	dotfilesDir = os.Getenv("PWD")
	if dotfilesDir == "" {
		dotfilesDir, err = os.Getwd()
		if err != nil {
			return "", "", "", err
		}
	}

	stowDir = filepath.Join(dotfilesDir, "user")
	targetDir, err = os.UserHomeDir()
	if err != nil {
		return "", "", "", err
	}

	return dotfilesDir, stowDir, targetDir, nil
}

func (u userStow) Apply() error {
	dotfilesDir, stowDir, targetDir, err := u.getDirs()
	if err != nil {
		return err
	}

	if err := u.handleApplyConflicts(dotfilesDir, stowDir, targetDir); err != nil {
		return fmt.Errorf("failed to handle conflicts: %w", err)
	}

	if err := types.Cmd("stow", "--verbose", "-d", dotfilesDir, "-t", targetDir, "--override=.*", "user").Interactive().Error(); err != nil {
		return fmt.Errorf("failed to apply dotfiles: %w", err)
	}

	logSuccess("dotfiles: applied")

	return nil
}

func (u userStow) Save() error {
	dotfilesDir, stowDir, targetDir, err := u.getDirs()
	if err != nil {
		return err
	}

	if err := u.handleConflicts(dotfilesDir, stowDir, targetDir); err != nil {
		return fmt.Errorf("failed to handle conflicts: %w", err)
	}

	if err := types.Cmd("stow", "--adopt", "--verbose", "-d", dotfilesDir, "-t", targetDir, "user").Interactive().Error(); err != nil {
		return fmt.Errorf("adoption failed: %w", err)
	}

	logSuccess("dotfiles: adopted")

	return nil
}

func (u userStow) Diff() error {
	dotfilesDir, _, targetDir, err := u.getDirs()
	if err != nil {
		return err
	}

	return types.Cmd("stow", "--simulate", "--verbose", "-d", dotfilesDir, "-t", targetDir, "user").Interactive().Error()
}

func (u userStow) handleApplyConflicts(dotfilesDir, stowDir, targetDir string) error {
	output := types.Cmd("stow", "--simulate", "--verbose", "-d", dotfilesDir, "-t", targetDir, "user").StdoutStderr()
	conflicts := u.parseApplyConflicts(output)
	if len(conflicts) == 0 {
		return nil
	}

	slog.Info("Found conflicting files", "count", len(conflicts))

	for _, file := range conflicts {
		targetPath := filepath.Join(targetDir, file)
		stowPath := filepath.Join(stowDir, file)

		if err := u.handleApplyConflict(targetPath, stowPath); err != nil {
			return fmt.Errorf("failed to handle conflict for %s: %w", file, err)
		}
	}

	return nil
}

func (u userStow) handleConflicts(dotfilesDir, stowDir, targetDir string) error {
	output := types.Cmd("stow", "--adopt", "--simulate", "--verbose", "-d", dotfilesDir, "-t", targetDir, "user").StdoutStderr()
	conflicts := u.parseConflicts(output)
	if len(conflicts) == 0 {
		return nil
	}

	slog.Info("Found conflicting files", "count", len(conflicts))

	for _, file := range conflicts {
		targetPath := filepath.Join(targetDir, file)
		stowPath := filepath.Join(stowDir, file)

		if err := u.handleConflict(targetPath, stowPath); err != nil {
			return fmt.Errorf("failed to handle conflict for %s: %w", file, err)
		}
	}

	return nil
}

func (u userStow) parseApplyConflicts(output string) []string {
	re := regexp.MustCompile(`cannot stow [^\n]+ over existing target ([^\n]+) since`)
	return lo.Map(re.FindAllStringSubmatch(output, -1), func(m []string, _ int) string { return m[1] })
}

func (u userStow) parseConflicts(output string) []string {
	re := regexp.MustCompile(`existing target is not owned by stow: ([^\n]+)`)
	return lo.Map(re.FindAllStringSubmatch(output, -1), func(m []string, _ int) string { return m[1] })
}

func (u userStow) handleApplyConflict(targetPath, stowPath string) error {
	for {
		prompt := promptui.Select{
			Label: fmt.Sprintf("Conflict: %s", targetPath),
			Items: []string{"Overwrite", "Show diff", "Keep target"},
		}
		idx, _, err := prompt.Run()
		if err != nil {
			return err
		}

		switch idx {
		case 0: // Overwrite
			if err := os.RemoveAll(targetPath); err != nil {
				return fmt.Errorf("failed to remove: %w", err)
			}
			slog.Info("Removed conflicting file", "path", targetPath)
			return nil
		case 1: // Show diff
			checkWarn(u.showDiff(targetPath, stowPath), "Failed to show diff")
		case 2: // Keep target
			slog.Info("Keeping target file", "file", targetPath)
			return nil
		}
	}
}

func (u userStow) handleConflict(targetPath, stowPath string) error {
	for {
		prompt := promptui.Select{
			Label: fmt.Sprintf("Conflict: %s", targetPath),
			Items: []string{"Adopt", "Delete", "Show diff", "Keep target"},
		}
		idx, _, err := prompt.Run()
		if err != nil {
			return err
		}

		switch idx {
		case 0: // Adopt
			if err := os.MkdirAll(filepath.Dir(stowPath), 0755); err != nil {
				return fmt.Errorf("failed to create directory: %w", err)
			}
			if err := os.Rename(targetPath, stowPath); err != nil {
				return fmt.Errorf("failed to move file: %w", err)
			}
			slog.Info("Adopted file", "from", targetPath, "to", stowPath)
			return nil
		case 1: // Delete
			if err := os.RemoveAll(targetPath); err != nil {
				return fmt.Errorf("failed to remove: %w", err)
			}
			slog.Info("Deleted", "path", targetPath)
			return nil
		case 2: // Show diff
			checkWarn(u.showDiff(targetPath, stowPath), "Failed to show diff")
		case 3: // Keep target
			slog.Info("Keeping target file", "file", targetPath)
			return nil
		}
	}
}

func (u userStow) showDiff(targetPath, stowPath string) error {
	stowInfo, err := os.Stat(stowPath)
	if os.IsNotExist(err) {
		slog.Info("File doesn't exist in stow directory yet (new file)")

		return nil
	}

	if err != nil {
		return err
	}

	targetInfo, err := os.Stat(targetPath)
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
