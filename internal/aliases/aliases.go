// Package aliases manages the AKA-owned aliases file and installed-entry registry.
package aliases

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Param describes a single parameter in an alias/function template.
type Param struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Description string `json:"description"`
}

// InstalledEntry is one alias or function managed by AKA.
type InstalledEntry struct {
	Name        string    `json:"name"`
	Kind        string    `json:"kind"` // "alias" | "function"
	Template    string    `json:"template"`
	Params      []Param   `json:"params"`
	CreatedAt   time.Time `json:"created_at"`
	LastUsedAt  time.Time `json:"last_used_at,omitempty"`
	UseCount30d int       `json:"use_count_30d"`
	Source      string    `json:"source"` // "analyze" | "manual"
}

// configDir returns ~/.config/aka, creating it if needed.
func configDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".config", "aka")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

// AliasesFilePath returns the path to the managed aliases file.
func AliasesFilePath() (string, error) {
	dir, err := configDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "aliases.sh"), nil
}

// InstalledJSONPath returns the path to the installed.json registry.
func InstalledJSONPath() (string, error) {
	dir, err := configDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "installed.json"), nil
}

// BackupDir returns (and creates) the backup directory.
func BackupDir() (string, error) {
	dir, err := configDir()
	if err != nil {
		return "", err
	}
	bd := filepath.Join(dir, "backups")
	if err := os.MkdirAll(bd, 0o755); err != nil {
		return "", err
	}
	return bd, nil
}

// Init creates the aliases file and appends the source line to rcFile idempotently.
// It takes a backup of rcFile before modifying it.
func Init(rcFile string) error {
	dir, err := configDir()
	if err != nil {
		return fmt.Errorf("config dir: %w", err)
	}

	// Create aliases.sh if it doesn't exist.
	aliasesPath := filepath.Join(dir, "aliases.sh")
	if _, err := os.Stat(aliasesPath); os.IsNotExist(err) {
		header := "# Managed by AKA (https://github.com/ntalmon/aka) — do not edit manually\n" +
			"# Last updated: " + time.Now().Format(time.RFC3339) + "\n"
		if err := os.WriteFile(aliasesPath, []byte(header), 0o644); err != nil {
			return fmt.Errorf("create aliases.sh: %w", err)
		}
	}

	// Source line to append.
	sourceLine := `[ -f "$HOME/.config/aka/aliases.sh" ] && . "$HOME/.config/aka/aliases.sh"`

	// Read rc file.
	data, err := os.ReadFile(rcFile)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read rc file: %w", err)
	}

	// Check idempotency.
	if strings.Contains(string(data), sourceLine) {
		return nil // already installed
	}

	// Backup rc file.
	bd, err := BackupDir()
	if err != nil {
		return err
	}
	backupName := filepath.Base(rcFile) + "." + fmt.Sprintf("%d", time.Now().Unix())
	if len(data) > 0 {
		if err := os.WriteFile(filepath.Join(bd, backupName), data, 0o644); err != nil {
			return fmt.Errorf("backup rc file: %w", err)
		}
	}

	// Append source line.
	f, err := os.OpenFile(rcFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open rc file: %w", err)
	}
	defer f.Close()

	addition := "\n# Added by `aka init` — single source line for AKA-managed aliases & functions\n" +
		sourceLine + "\n"
	if _, err := f.WriteString(addition); err != nil {
		return fmt.Errorf("write source line: %w", err)
	}
	return nil
}

// LoadInstalled reads installed.json and returns the entries.
func LoadInstalled() ([]InstalledEntry, error) {
	path, err := InstalledJSONPath()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return []InstalledEntry{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read installed.json: %w", err)
	}
	var entries []InstalledEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, fmt.Errorf("parse installed.json: %w", err)
	}
	return entries, nil
}

// SaveInstalled writes entries to installed.json.
func SaveInstalled(entries []InstalledEntry) error {
	path, err := InstalledJSONPath()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal installed.json: %w", err)
	}
	return os.WriteFile(path, data, 0o644)
}

// Backup takes a timestamped backup of aliases.sh (and installed.json if present).
func Backup() error {
	aliasesPath, err := AliasesFilePath()
	if err != nil {
		return err
	}
	bd, err := BackupDir()
	if err != nil {
		return err
	}
	ts := fmt.Sprintf("%d", time.Now().Unix())

	// Backup aliases.sh.
	if data, err := os.ReadFile(aliasesPath); err == nil {
		dst := filepath.Join(bd, "aliases.sh."+ts)
		if err := os.WriteFile(dst, data, 0o644); err != nil {
			return fmt.Errorf("backup aliases.sh: %w", err)
		}
	}

	// Backup installed.json.
	installedPath, err := InstalledJSONPath()
	if err != nil {
		return err
	}
	if data, err := os.ReadFile(installedPath); err == nil {
		dst := filepath.Join(bd, "installed.json."+ts)
		if err := os.WriteFile(dst, data, 0o644); err != nil {
			return fmt.Errorf("backup installed.json: %w", err)
		}
	}
	return nil
}

// WriteAliasesFile takes a backup and does a full rewrite of aliases.sh from entries.
func WriteAliasesFile(entries []InstalledEntry) error {
	// Backup first.
	if err := Backup(); err != nil {
		return fmt.Errorf("backup before write: %w", err)
	}

	// Sort entries by name for deterministic output.
	sorted := make([]InstalledEntry, len(entries))
	copy(sorted, entries)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Name < sorted[j].Name
	})

	var sb strings.Builder
	sb.WriteString("# Managed by AKA (https://github.com/ntalmon/aka) — do not edit manually\n")
	sb.WriteString("# Last updated: " + time.Now().Format(time.RFC3339) + "\n\n")

	for _, e := range sorted {
		switch e.Kind {
		case "alias":
			sb.WriteString(fmt.Sprintf("alias %s='%s'\n", e.Name, escapeAlias(e.Template)))
		case "function":
			sb.WriteString(renderFunction(e))
		}
	}

	aliasesPath, err := AliasesFilePath()
	if err != nil {
		return err
	}
	return os.WriteFile(aliasesPath, []byte(sb.String()), 0o644)
}

// escapeAlias escapes single quotes in alias body.
func escapeAlias(s string) string {
	return strings.ReplaceAll(s, "'", `'\''`)
}

// renderFunction emits a POSIX-compatible shell function.
func renderFunction(e InstalledEntry) string {
	var sb strings.Builder
	sb.WriteString(e.Name + "() {\n")
	sb.WriteString("  " + e.Template + "\n")
	sb.WriteString("}\n")
	return sb.String()
}
