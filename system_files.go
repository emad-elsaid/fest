package fest

import "github.com/emad-elsaid/types"

import (
	"bytes"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/manifoldco/promptui"
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
	files := make(map[string]string)             // srcPath -> targetPath
	targetToSources := make(map[string][]string) // targetPath -> []srcPath (for collision detection)

	for _, systemFilesDir := range systemFilesDirs {
		if systemFilesDir == "" {
			continue
		}

		err := filepath.WalkDir(systemFilesDir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
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
	dirFiles, err := listSystemDirFiles()
	if err != nil {
		slog.Warn("Failed to list system files directory", "error", err)
	}
	wanted := make([]string, 0, len(dirFiles))
	for _, targetPath := range dirFiles {
		wanted = append(wanted, targetPath)
	}

	return wanted
}
func (s systemFiles) Match(want, have string) bool { return want == have }
func (s systemFiles) ListInstalled() ([]string, error) {
	// Check directory-based files

	dirFiles, err := listSystemDirFiles()
	if err != nil {
		return nil, err
	}

	installed := make([]string, 0, len(dirFiles))

	// Files we deployed whose source was since deleted from the system dirs
	// can't be seen by the walk above. Read them back from state so they're
	// reported as installed and offered for uninstall, instead of being
	// orphaned on the system forever.
	if state, stateErr := loadState(); stateErr == nil {
		current := make(map[string]struct{}, len(dirFiles))
		for _, targetPath := range dirFiles {
			current[targetPath] = struct{}{}
		}
		for targetPath, entry := range state.Files {
			if _, ok := current[targetPath]; ok {
				continue
			}
			// Only still ours while our exact content is deployed. If a
			// package reinstall or manual edit replaced it, the file belongs
			// to something else now and must not be offered for uninstall.
			content, readErr := readFileWithSudo(targetPath)
			if readErr != nil || hashBytes(content) != entry.InstalledHash {
				continue
			}
			installed = append(installed, targetPath)
		}
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

		// Compare contents exactly
		if bytes.Equal(srcContent, targetContent) {
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
	// Persist tracked state even if a later operation fails, so files already
	// deployed in this run stay restorable/uninstallable.
	defer func() {
		if err := saveState(state); err != nil {
			slog.Warn("Failed to save state", "error", err)
		}
	}()

	dirFiles, err := listSystemDirFiles()
	if err != nil {
		return err
	}

	targetToSrc := make(map[string]string, len(dirFiles))
	for src, dst := range dirFiles {
		targetToSrc[dst] = src
	}

	for _, targetPath := range ops {
		srcPath, ok := targetToSrc[targetPath]
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
		slog.Debug("Copying " + srcPath + " → " + targetPath)
		perm := fmt.Sprintf("%o", srcInfo.Mode().Perm())
		dir := filepath.Dir(targetPath)

		if err := os.MkdirAll(dir, 0755); err != nil {
			if !os.IsPermission(err) {
				return fmt.Errorf("failed to create parent dir %s: %w", dir, err)
			}
			if err := types.Sudo("mkdir", "-p", dir).Error(); err != nil {
				return fmt.Errorf("failed to create parent dir %s: %w", dir, err)
			}
		}
		if err := types.Sudo("install", "-m", perm, srcPath, targetPath).Error(); err != nil {
			return fmt.Errorf("failed to install %s: %w", targetPath, err)
		}

		if installed, readErr := readFileWithSudo(targetPath); readErr == nil {
			entry.InstalledHash = hashBytes(installed)
		} else {
			slog.Warn("Could not verify installed content; recording pre-install hash",
				"path", targetPath, "error", readErr)
			entry.InstalledHash = hashBytes(content)
		}
		// Update state
		entry.InstalledAt = nowRFC3339()
		state.Files[targetPath] = entry
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

	toDelete := make([]string, 0, len(ops))

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
			delete(state.Files, targetPath)
		} else {
			toDelete = append(toDelete, targetPath)
		}
	}

	if len(toDelete) > 0 {
		const rmBatch = 100
		for i := 0; i < len(toDelete); i += rmBatch {
			end := i + rmBatch
			if end > len(toDelete) {
				end = len(toDelete)
			}
			batch := toDelete[i:end]
			// Delete (we created it)
			if err := types.Sudo("rm", append([]string{"-f"}, batch...)...).Error(); err != nil {
				slog.Warn("Failed to delete files", "error", err, "count", len(batch))
				continue
			}
			for _, path := range batch {
				slog.Info("Deleted", "path", path)
				delete(state.Files, path)
			}
		}
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
