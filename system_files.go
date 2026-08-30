package fest

import "github.com/emad-elsaid/types"

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/manifoldco/promptui"
	"github.com/samber/lo"
)

var (
	systemFilesDirs = []string{"system"}
)

// SystemFilesDir adds a directory to scan for system files to deploy.
// Files in this directory are copied to the system, mirroring the directory structure.
// For example, a file at "system/etc/hosts" will be copied to "/etc/hosts".
//
// The framework tracks changes and can restore original files when uninstalling.
//
// Example:
//
//	fest.SystemFilesDir("system")  // Default
//	fest.SystemFilesDir("custom-system-files")  // Additional directory
func SystemFilesDir(dir string) {
	// Deduplicate: only add if not already present
	for _, existing := range systemFilesDirs {
		if existing == dir {
			return
		}
	}
	systemFilesDirs = append(systemFilesDirs, dir)
}

// readFileWithSudo attempts to read a file, falling back to sudo if permission is denied
func readFileWithSudo(path string) ([]byte, error) {
	content, err := os.ReadFile(path)
	if err != nil && os.IsPermission(err) {
		output, err := types.Sudo("cat", path).StdoutErr()
		return []byte(output), err
	}
	return content, err
}

// listSystemDirFiles returns all files in the system files directories and their target paths
func listSystemDirFiles() (map[string]string, error) {
	files := make(map[string]string)         // srcPath -> targetPath
	targetToSources := make(map[string][]string) // targetPath -> []srcPath (for collision detection)

	for _, systemFilesDir := range systemFilesDirs {
		if systemFilesDir == "" {
			continue
		}

		err := filepath.Walk(systemFilesDir, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() {
				return nil
			}

			// Get relative path from systemFilesDir
			relPath, err := filepath.Rel(systemFilesDir, path)
			if err != nil {
				return err
			}

			// Target path is / + relPath
			targetPath := "/" + relPath
			
			// Track potential collision
			targetToSources[targetPath] = append(targetToSources[targetPath], path)
			
			// Later directories override earlier ones if same target path
			files[path] = targetPath
			return nil
		})

		if err != nil {
			return files, err
		}
	}

	// Warn about collisions
	for target, sources := range targetToSources {
		if len(sources) > 1 {
			slog.Warn("Multiple source files target same destination (last one wins)",
				"target", target, "sources", sources)
		}
	}

	return files, nil
}

type systemFiles struct{}

func (s systemFiles) ResourceName() string { return "system-files" }
func (s systemFiles) Wanted() []string {
	var wanted []string

	// Add files from system directory
	dirFiles, err := listSystemDirFiles()
	if err != nil {
		slog.Warn("Failed to list system files directory", "error", err)
	}
	for _, targetPath := range dirFiles {
		wanted = append(wanted, targetPath)
	}

	return wanted
}
func (s systemFiles) Match(want, have string) bool { return want == have }
func (s systemFiles) ListInstalled() ([]string, error) {
	var installed []string

	// Check directory-based files
	dirFiles, err := listSystemDirFiles()
	if err != nil {
		return installed, err
	}
	for srcPath, targetPath := range dirFiles {
		// Read source file content
		srcContent, err := os.ReadFile(srcPath)
		if err != nil {
			continue
		}

		// Read target file content
		targetContent, err := readFileWithSudo(targetPath)
		if err != nil {
			continue
		}

		// Compare contents
		if strings.TrimSpace(string(srcContent)) == strings.TrimSpace(string(targetContent)) {
			installed = append(installed, targetPath)
		}
	}

	return installed, nil
}
func (s systemFiles) ListExplicit() ([]string, error) { return s.ListInstalled() }
func (s systemFiles) Install(ops []string) error {
	state, err := loadState()
	if err != nil {
		slog.Warn("Failed to load state, starting fresh", "error", err)
		state = &systemFilesState{Version: 1, Files: make(map[string]fileState)}
	}

	dirFiles, err := listSystemDirFiles()
	if err != nil {
		return err
	}

	for _, targetPath := range ops {
		srcPath, ok := lo.FindKey(dirFiles, targetPath)
		if !ok {
			return fmt.Errorf("source file not found for target: %s", targetPath)
		}

		srcInfo, err := os.Stat(srcPath)
		if err != nil {
			return fmt.Errorf("failed to stat source file %s: %w", srcPath, err)
		}

		content, err := os.ReadFile(srcPath)
		if err != nil {
			return fmt.Errorf("failed to read source file %s: %w", srcPath, err)
		}

		// Track state before modification
		entry, tracked := state.Files[targetPath]
		if !tracked {
			// First time managing this file
			entry = fileState{}
			if currentContent, readErr := readFileWithSudo(targetPath); readErr == nil {
				// File exists - backup it
				entry.OriginalExists = true
				entry.OriginalHash = hashBytes(currentContent)
				if backupPath, backupErr := backupFile(targetPath, currentContent); backupErr == nil {
					entry.BackupPath = backupPath
				} else {
					slog.Warn("Failed to backup", "path", targetPath, "error", backupErr)
				}
			}
		} else {
			// Check for external modifications
			if currentContent, readErr := readFileWithSudo(targetPath); readErr == nil {
				currentHash := hashBytes(currentContent)
				if currentHash != entry.InstalledHash && currentHash != entry.OriginalHash {
					action := askExternalModification(targetPath)
					switch action {
					case "skip":
						continue
					case "backup":
						extBackup := targetPath[1:] + ".external.backup"
						extPath := filepath.Join(backupsDir(), extBackup)
						os.MkdirAll(filepath.Dir(extPath), 0755)
						os.WriteFile(extPath, currentContent, 0644)
						slog.Info("Backed up external changes", "path", extPath)
					}
				}
			}
		}

		// Create parent directories
		dir := filepath.Dir(targetPath)
		if err := os.MkdirAll(dir, 0755); err != nil {
			if os.IsPermission(err) {
				if mkdirErr := types.Sudo("mkdir", "-p", dir).Interactive().Error(); mkdirErr != nil {
					return mkdirErr
				}
			} else {
				return err
			}
		}

		slog.Debug("Copying " + srcPath + " → " + targetPath)
		if err := types.Sudo("tee", targetPath).Input(string(content)).Error(); err != nil {
			return err
		}

		perm := srcInfo.Mode().Perm()
		if err := types.Sudo("chmod", fmt.Sprintf("%o", perm), targetPath).Error(); err != nil {
			return fmt.Errorf("failed to set permissions on %s: %w", targetPath, err)
		}

		// Update state
		entry.InstalledHash = hashBytes(content)
		entry.InstalledAt = nowRFC3339()
		state.Files[targetPath] = entry
	}

	if err := saveState(state); err != nil {
		slog.Warn("Failed to save state", "error", err)
	}
	return nil
}

func askExternalModification(path string) string {
	prompt := promptui.Select{
		Label: fmt.Sprintf("File %s was modified externally", path),
		Items: []string{"Overwrite", "Skip", "Backup external then overwrite"},
	}
	idx, _, _ := prompt.Run()
	switch idx {
	case 1:
		return "skip"
	case 2:
		return "backup"
	default:
		return "overwrite"
	}
}
func (s systemFiles) Uninstall(ops []string) error {
	state, err := loadState()
	if err != nil {
		slog.Warn("Cannot load state - skipping uninstall", "error", err)
		return nil
	}

	for _, targetPath := range ops {
		entry, tracked := state.Files[targetPath]
		if !tracked {
			slog.Warn("File not tracked, skipping", "path", targetPath)
			continue
		}

		if entry.OriginalExists && entry.BackupPath != "" {
			// Restore original
			if err := restoreFile(targetPath, entry.BackupPath); err != nil {
				slog.Warn("Failed to restore", "path", targetPath, "error", err)
				continue
			}
			slog.Info("Restored original", "path", targetPath)
			deleteBackup(entry.BackupPath)
		} else {
			// Delete (we created it)
			if err := types.Sudo("rm", "-f", targetPath).Error(); err != nil {
				slog.Warn("Failed to delete", "path", targetPath, "error", err)
				continue
			}
			slog.Info("Deleted", "path", targetPath)
		}

		delete(state.Files, targetPath)
	}

	if err := saveState(state); err != nil {
		slog.Warn("Failed to save state", "error", err)
	}
	return nil
}
func (s systemFiles) MarkExplicit(ops []string) error { return nil }
func (s systemFiles) Update() error                   { return nil }

// UninstallPreview returns what would happen for each file to uninstall
func (s systemFiles) UninstallPreview(ops []string) map[string]string {
	result := make(map[string]string)
	state, err := loadState()
	if err != nil {
		return result
	}

	for _, targetPath := range ops {
		entry, tracked := state.Files[targetPath]
		if !tracked {
			result[targetPath] = "not tracked"
		} else if entry.OriginalExists && entry.BackupPath != "" {
			result[targetPath] = "would restore original"
		} else {
			result[targetPath] = "would delete"
		}
	}
	return result
}
func (s systemFiles) GetDependencies() (map[string][]string, error) {
	return make(map[string][]string), nil
}
func (s systemFiles) SaveAsGo(wanted []string) error { return nil }
