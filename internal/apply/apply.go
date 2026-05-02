// Package apply adds new suggestions to installed.json and rewrites aliases.sh.
package apply

import (
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/ntalmon/aka/aka-cli/internal/aliases"
	"github.com/ntalmon/aka/aka-cli/internal/llm"
)

// ConflictAction describes what to do when a name conflict is found.
type ConflictAction int

const (
	ConflictSkip   ConflictAction = iota
	ConflictRename                // Not used automatically; caller should rename before passing.
)

// NameExistsInShell checks whether a name is already defined in the current shell
// by running `type <name>` in a child shell.
func NameExistsInShell(name string) bool {
	cmd := exec.Command("sh", "-c", fmt.Sprintf("type %s 2>/dev/null", name))
	out, err := cmd.Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) != ""
}

// Apply adds the accepted suggestions to installed.json and rewrites aliases.sh.
// For each suggestion, it checks for name conflicts (in installed.json and in the shell).
// It returns a list of names that were skipped due to conflicts.
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
			Source:    "analyze",
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
