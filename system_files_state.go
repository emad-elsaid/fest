package fest

import "github.com/emad-elsaid/types"

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"time"
)

// fileState tracks a single managed system file
type fileState struct {
	OriginalExists bool   `json:"originalExists"`
	OriginalHash   string `json:"originalHash,omitempty"`
	BackupPath     string `json:"backupPath,omitempty"`
	InstalledHash  string `json:"installedHash"`
	InstalledAt    string `json:"installedAt"`
}

// systemFilesState tracks all managed system files
type systemFilesState struct {
	Version int                  `json:"version"`
	Files   map[string]fileState `json:"files"`
}

func stateDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		// Fall back to the account's home directory so the state path stays
		// absolute even when $HOME is unavailable.
		if u, uerr := user.Current(); uerr == nil {
			home = u.HomeDir
		}
	}
	return filepath.Join(home, ".local", "share", "dotfiles")
}

func statePath() string {
	return filepath.Join(stateDir(), "system-files-state.json")
}

func backupsDir() string {
	return filepath.Join(stateDir(), "backups")
}

func loadState() (*systemFilesState, error) {
	data, err := os.ReadFile(statePath())
	if err != nil {
		if os.IsNotExist(err) {
			return &systemFilesState{Version: 1, Files: make(map[string]fileState)}, nil
		}
		return nil, err
	}

	var state systemFilesState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("failed to parse state file %s: %w", statePath(), err)
	}
	if state.Files == nil {
		state.Files = make(map[string]fileState)
	}
	return &state, nil
}

func saveState(state *systemFilesState) error {
	if err := os.MkdirAll(stateDir(), 0755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(statePath(), data, 0644)
}

func hashBytes(data []byte) string {
	h := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(h[:])
}

// backupFile saves original content to backup directory, returns relative backup path
func backupFile(targetPath string, content []byte) (string, error) {
	// Create relative path under backups dir (e.g., /etc/hosts -> backups/etc/hosts.backup)
	relPath := targetPath[1:] + ".backup" // Strip leading / and add .backup
	backupPath := filepath.Join(backupsDir(), relPath)

	if err := os.MkdirAll(filepath.Dir(backupPath), 0755); err != nil {
		return "", err
	}

	if err := os.WriteFile(backupPath, content, 0644); err != nil {
		return "", err
	}

	// Return path relative to state dir for storage
	return filepath.Join("backups", relPath), nil
}

// restoreFile restores content from backup to target using sudo
func restoreFile(targetPath, relBackupPath string) error {
	backupPath := filepath.Join(stateDir(), relBackupPath)
	content, err := os.ReadFile(backupPath)
	if err != nil {
		return err
	}

	// Create parent directories if needed
	dir := filepath.Dir(targetPath)
	if err := types.Sudo("mkdir", "-p", dir).Error(); err != nil {
		return err
	}

	return types.Sudo("tee", targetPath).Input(string(content)).Error()
}

// deleteBackup removes the backup file for a target path
func deleteBackup(relBackupPath string) error {
	if relBackupPath == "" {
		return nil
	}
	backupPath := filepath.Join(stateDir(), relBackupPath)
	return os.Remove(backupPath)
}

func nowRFC3339() string {
	return time.Now().Format(time.RFC3339)
}
