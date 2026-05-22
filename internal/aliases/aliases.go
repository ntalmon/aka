// Package aliases manages the AKA-owned aliases file and installed-entry registry.
package aliases

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
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
	Source      string    `json:"source"` // "scan" | "manual"
}

// configDir returns ~/.config/aka, creating it if needed with 0700 permissions.
func configDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".config", "aka")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	// Tighten permissions even if the directory already existed.
	if err := os.Chmod(dir, 0o700); err != nil {
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

// CompletionFilePath returns the path to the managed shell completion script.
func CompletionFilePath() (string, error) {
	dir, err := configDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "completion.sh"), nil
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
	if err := os.MkdirAll(bd, 0o700); err != nil {
		return "", err
	}
	return bd, nil
}

// checkNotSymlink returns an error if path exists and is a symlink, preventing
// TOCTOU attacks where an attacker pre-places a symlink to redirect writes.
func checkNotSymlink(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s is a symlink — refusing to overwrite", path)
	}
	return nil
}

// atomicWriteFile writes data to path atomically via a temp-file rename and
// refuses to write if path is already a symlink.
func atomicWriteFile(path string, data []byte, perm os.FileMode) error {
	if err := checkNotSymlink(path); err != nil {
		return err
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".aka-tmp-")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }() // no-op after a successful rename
	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// ValidateFunctionTemplate returns an error if the template contains a
// line-leading '}' that would close the enclosing shell function body early.
func ValidateFunctionTemplate(template string) error {
	for line := range strings.SplitSeq(template, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "}") {
			return fmt.Errorf("template contains '}' that would escape the function body")
		}
	}
	return nil
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
		if err := atomicWriteFile(aliasesPath, []byte(header), 0o600); err != nil {
			return fmt.Errorf("create aliases.sh: %w", err)
		}
	}

	sourceLine := `[ -f "$HOME/.config/aka/aliases.sh" ] && . "$HOME/.config/aka/aliases.sh"`
	wrapperMarker := "# Added by aka init — shell wrapper"

	// Read rc file.
	data, err := os.ReadFile(rcFile)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read rc file: %w", err)
	}
	content := string(data)

	hasSourceLine := strings.Contains(content, sourceLine)
	hasWrapper := strings.Contains(content, wrapperMarker)

	if hasSourceLine && hasWrapper {
		return nil // fully installed
	}

	// Backup rc file before modifying.
	bd, err := BackupDir()
	if err != nil {
		return err
	}
	backupName := filepath.Base(rcFile) + "." + fmt.Sprintf("%d", time.Now().Unix())
	if len(data) > 0 {
		if err := os.WriteFile(filepath.Join(bd, backupName), data, 0o600); err != nil {
			return fmt.Errorf("backup rc file: %w", err)
		}
	}

	f, err := os.OpenFile(rcFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open rc file: %w", err)
	}
	defer func() { _ = f.Close() }()

	if !hasWrapper {
		wrapper := "\n" + wrapperMarker + " — auto-reloads aliases after 'aka scan'\n" +
			"aka() {\n" +
			"    command aka \"$@\"\n" +
			"    local _exit_code=$?\n" +
			"    if [[ \"$1\" == \"scan\" ]] && [[ $_exit_code -eq 0 ]]; then\n" +
			"        . \"$HOME/.config/aka/aliases.sh\" 2>/dev/null\n" +
			"    fi\n" +
			"    return $_exit_code\n" +
			"}\n"
		if _, err := f.WriteString(wrapper); err != nil {
			return fmt.Errorf("write shell wrapper: %w", err)
		}
	}

	if !hasSourceLine {
		addition := "\n# Added by `aka init` — single source line for AKA-managed aliases & functions\n" +
			sourceLine + "\n"
		if _, err := f.WriteString(addition); err != nil {
			return fmt.Errorf("write source line: %w", err)
		}
	}

	return nil
}

// InitCompletion generates a shell completion script for `aka` and appends a
// source line to rcFile, idempotently. shell must be "zsh" or "bash".
func InitCompletion(shell, rcFile string) error {
	dir, err := configDir()
	if err != nil {
		return fmt.Errorf("config dir: %w", err)
	}

	completionPath := filepath.Join(dir, "completion.sh")

	// Generate the completion script body.
	var script string
	switch shell {
	case "zsh":
		script = `# AKA shell completion for zsh — generated by 'aka init'
# Managed by AKA (https://github.com/ntalmon/aka) — do not edit manually

_aka_completion() {
  local -a completions
  local cur="${words[${#words}]}"
  local prev="${words[$((${#words}-1))]}"

  # Top-level subcommands
  local subcommands=(scan init list undo config completion help)

  if [[ ${#words[@]} -eq 2 ]]; then
    completions=(${subcommands})
  else
    case "${words[2]}" in
      config)
        completions=(set-key set-max-history show)
        ;;
      scan)
        completions=(--dry-run --history --full-history)
        ;;
    esac
  fi

  compadd -a completions
}

compdef _aka_completion aka
`
	case "bash":
		script = `# AKA shell completion for bash — generated by 'aka init'
# Managed by AKA (https://github.com/ntalmon/aka) — do not edit manually

_aka_completion() {
  local cur prev subcommands
  cur="${COMP_WORDS[COMP_CWORD]}"
  prev="${COMP_WORDS[COMP_CWORD-1]}"
  subcommands="scan init list undo config completion help"

  if [[ $COMP_CWORD -eq 1 ]]; then
    COMPREPLY=( $(compgen -W "${subcommands}" -- "${cur}") )
  else
    case "${prev}" in
      config)
        COMPREPLY=( $(compgen -W "set-key set-max-history show" -- "${cur}") )
        ;;
      scan)
        COMPREPLY=( $(compgen -W "--dry-run --history --full-history" -- "${cur}") )
        ;;
      *)
        COMPREPLY=()
        ;;
    esac
  fi
}

complete -F _aka_completion aka
`
	default:
		return fmt.Errorf("unsupported shell %q for completion", shell)
	}

	if err := atomicWriteFile(completionPath, []byte(script), 0o600); err != nil {
		return fmt.Errorf("write completion.sh: %w", err)
	}

	// Source line to append.
	sourceLine := `[ -f "$HOME/.config/aka/completion.sh" ] && . "$HOME/.config/aka/completion.sh"`

	// Read rc file.
	data, err := os.ReadFile(rcFile)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("read rc file: %w", err)
	}

	// Idempotency check.
	if strings.Contains(string(data), sourceLine) {
		return nil
	}

	// Append source line.
	f, err := os.OpenFile(rcFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open rc file: %w", err)
	}
	defer func() { _ = f.Close() }()

	addition := "\n# Added by `aka init` — tab completion for the `aka` command\n" +
		sourceLine + "\n"
	if _, err := f.WriteString(addition); err != nil {
		return fmt.Errorf("write completion source line: %w", err)
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

// SaveInstalled writes entries to installed.json atomically at 0600.
func SaveInstalled(entries []InstalledEntry) error {
	path, err := InstalledJSONPath()
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal installed.json: %w", err)
	}
	return atomicWriteFile(path, data, 0o600)
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
		if err := atomicWriteFile(dst, data, 0o600); err != nil {
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
		if err := atomicWriteFile(dst, data, 0o600); err != nil {
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

	var aliasEntries, functions []InstalledEntry
	for _, e := range sorted {
		if e.Kind == "function" {
			functions = append(functions, e)
		} else {
			aliasEntries = append(aliasEntries, e)
		}
	}

	var sb strings.Builder
	sb.WriteString("# Managed by AKA (https://github.com/ntalmon/aka) — do not edit manually\n")
	sb.WriteString("# Last updated: " + time.Now().Format(time.RFC3339) + "\n\n")

	for _, e := range aliasEntries {
		fmt.Fprintf(&sb, "alias %s='%s'\n", e.Name, escapeAlias(e.Template))
	}

	if len(functions) > 0 {
		sb.WriteString("\n")
		written := 0
		for _, e := range functions {
			rendered, err := renderFunction(e)
			if err != nil {
				fmt.Printf("Warning: skipping unsafe function %q: %v\n", e.Name, err)
				continue
			}
			if written > 0 {
				sb.WriteString("\n")
			}
			sb.WriteString(rendered)
			written++
		}
	}

	aliasesPath, err := AliasesFilePath()
	if err != nil {
		return err
	}
	return atomicWriteFile(aliasesPath, []byte(sb.String()), 0o600)
}

// escapeAlias escapes single quotes in alias body.
func escapeAlias(s string) string {
	return strings.ReplaceAll(s, "'", `'\''`)
}

// renderFunction emits a shell function, returning an error if the template
// would escape the function body.
func renderFunction(e InstalledEntry) (string, error) {
	if err := ValidateFunctionTemplate(e.Template); err != nil {
		return "", err
	}
	var sb strings.Builder
	sb.WriteString("function " + e.Name + " () {\n")
	sb.WriteString("  " + e.Template + "\n")
	sb.WriteString("}\n")
	return sb.String(), nil
}

var (
	reShellAlias = regexp.MustCompile(`(?m)^alias\s+([A-Za-z_][A-Za-z0-9_.+\-]*)=`)
	reShellFunc1 = regexp.MustCompile(`(?m)^([A-Za-z_][A-Za-z0-9_.+\-]*)\s*\(\s*\)`)
	reShellFunc2 = regexp.MustCompile(`(?m)^function\s+([A-Za-z_][A-Za-z0-9_.+\-]*)`)
)

// LoadShellDefinedNames parses common shell config files and returns the set of
// alias and function names already defined there.
func LoadShellDefinedNames() map[string]bool {
	home, err := os.UserHomeDir()
	if err != nil {
		return map[string]bool{}
	}
	candidates := []string{
		filepath.Join(home, ".zshrc"),
		filepath.Join(home, ".bashrc"),
		filepath.Join(home, ".bash_aliases"),
		filepath.Join(home, ".bash_profile"),
		filepath.Join(home, ".profile"),
	}
	names := make(map[string]bool)
	for _, f := range candidates {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		content := string(data)
		for _, m := range reShellAlias.FindAllStringSubmatch(content, -1) {
			names[m[1]] = true
		}
		for _, m := range reShellFunc1.FindAllStringSubmatch(content, -1) {
			names[m[1]] = true
		}
		for _, m := range reShellFunc2.FindAllStringSubmatch(content, -1) {
			names[m[1]] = true
		}
	}
	return names
}
