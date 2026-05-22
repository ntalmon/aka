// Package apply adds new suggestions to installed.json and rewrites aliases.sh.
package apply

import (
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/ntalmon/aka/aka-cli/internal/aliases"
	"github.com/ntalmon/aka/aka-cli/internal/llm"
)

// validNameRE matches shell-safe alias/function names (POSIX identifier + length cap).
var validNameRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,31}$`)

// ConflictAction describes what to do when a name conflict is found.
type ConflictAction int

const (
	ConflictSkip   ConflictAction = iota
	ConflictRename                // Not used automatically; caller should rename before passing.
)

// NameExistsInShell checks whether a name is already defined in the current shell
// by running `type <name>` in a child shell. The name is passed as a positional
// argument to avoid shell injection.
func NameExistsInShell(name string) bool {
	cmd := exec.Command("sh", "-c", `type "$1" 2>/dev/null`, "--", name)
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) != ""
}

// Apply adds the accepted suggestions to installed.json and rewrites aliases.sh.
// For each suggestion, it checks for name conflicts (in installed.json and in the shell).
// It returns a list of names that were skipped due to conflicts or validation failures.
func Apply(newSuggestions []llm.Suggestion) (skipped []string, err error) {
	existing, err := aliases.LoadInstalled()
	if err != nil {
		return nil, fmt.Errorf("load installed: %w", err)
	}

	// Build a set of existing names.
	installedNames := make(map[string]bool)
	for _, e := range existing {
		installedNames[e.Name] = true
	}

	now := time.Now()
	for _, s := range newSuggestions {
		// Reject names that don't match the safe shell identifier pattern.
		if !validNameRE.MatchString(s.Name) {
			skipped = append(skipped, s.Name)
			continue
		}
		// For function entries, validate the template won't escape the function body.
		if s.Kind == "function" {
			if err := aliases.ValidateFunctionTemplate(s.Template); err != nil {
				skipped = append(skipped, s.Name)
				continue
			}
		}
		// Check for conflict in installed.json.
		if installedNames[s.Name] {
			skipped = append(skipped, s.Name)
			continue
		}
		// Check for conflict in the shell.
		if NameExistsInShell(s.Name) {
			skipped = append(skipped, s.Name)
			continue
		}

		entry := aliases.InstalledEntry{
			Name:      s.Name,
			Kind:      s.Kind,
			Template:  s.Template,
			Params:    s.Params,
			CreatedAt: now,
			Source:    "scan",
		}
		existing = append(existing, entry)
		installedNames[s.Name] = true
	}

	// Save updated installed.json.
	if err := aliases.SaveInstalled(existing); err != nil {
		return skipped, fmt.Errorf("save installed: %w", err)
	}

	// Rewrite aliases.sh.
	if err := aliases.WriteAliasesFile(existing); err != nil {
		return skipped, fmt.Errorf("write aliases file: %w", err)
	}

	return skipped, nil
}
