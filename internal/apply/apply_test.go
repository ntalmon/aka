package apply

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ntalmon/aka/aka-cli/internal/aliases"
	"github.com/ntalmon/aka/aka-cli/internal/llm"
)

// setTempHome redirects all config paths to an isolated temp dir.
func setTempHome(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
}

func TestApplyAddsNewEntries(t *testing.T) {
	setTempHome(t)

	suggestions := []llm.Suggestion{
		{Name: "gst", Kind: "alias", Template: "git status"},
		{Name: "glo", Kind: "alias", Template: "git log --oneline"},
	}
	skipped, err := Apply(suggestions)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(skipped) != 0 {
		t.Errorf("expected no skips, got %v", skipped)
	}

	installed, err := aliases.LoadInstalled()
	if err != nil {
		t.Fatalf("LoadInstalled: %v", err)
	}
	if len(installed) != 2 {
		t.Fatalf("expected 2 installed entries, got %d", len(installed))
	}
	names := map[string]bool{}
	for _, e := range installed {
		names[e.Name] = true
	}
	if !names["gst"] || !names["glo"] {
		t.Errorf("unexpected installed names: %v", names)
	}
}

func TestApplySkipsInstalledNameConflict(t *testing.T) {
	setTempHome(t)

	existing := []aliases.InstalledEntry{
		{Name: "gst", Kind: "alias", Template: "git status", CreatedAt: time.Now(), Source: "analyze"},
	}
	if err := aliases.SaveInstalled(existing); err != nil {
		t.Fatalf("SaveInstalled: %v", err)
	}
	if err := aliases.WriteAliasesFile(existing); err != nil {
		t.Fatalf("WriteAliasesFile: %v", err)
	}

	suggestions := []llm.Suggestion{
		{Name: "gst", Kind: "alias", Template: "git status --short"},
		{Name: "glo", Kind: "alias", Template: "git log --oneline"},
	}
	skipped, err := Apply(suggestions)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(skipped) != 1 || skipped[0] != "gst" {
		t.Errorf("expected ['gst'] skipped, got %v", skipped)
	}

	installed, _ := aliases.LoadInstalled()
	if len(installed) != 2 {
		t.Errorf("expected 2 total entries (1 pre-existing + 1 new), got %d", len(installed))
	}
}

func TestApplyNilSuggestions(t *testing.T) {
	setTempHome(t)

	skipped, err := Apply(nil)
	if err != nil {
		t.Fatalf("Apply(nil): %v", err)
	}
	if len(skipped) != 0 {
		t.Errorf("expected no skips, got %v", skipped)
	}
	installed, _ := aliases.LoadInstalled()
	if len(installed) != 0 {
		t.Errorf("expected 0 entries after empty apply, got %d", len(installed))
	}
}

func TestApplyWritesAliasesFile(t *testing.T) {
	setTempHome(t)

	suggestions := []llm.Suggestion{
		{Name: "dps", Kind: "alias", Template: "docker ps"},
	}
	if _, err := Apply(suggestions); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	path, err := aliases.AliasesFilePath()
	if err != nil {
		t.Fatalf("AliasesFilePath: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read aliases.sh: %v", err)
	}
	if !strings.Contains(string(data), "dps") {
		t.Errorf("aliases.sh missing 'dps':\n%s", data)
	}
}

func TestApplySetsSourceToAnalyze(t *testing.T) {
	setTempHome(t)

	suggestions := []llm.Suggestion{
		{Name: "gco", Kind: "function", Template: `git checkout "$1"`},
	}
	if _, err := Apply(suggestions); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	installed, _ := aliases.LoadInstalled()
	if len(installed) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(installed))
	}
	if installed[0].Source != "analyze" {
		t.Errorf("expected source='analyze', got %q", installed[0].Source)
	}
}

func TestApplyPreventsDuplicateWithinBatch(t *testing.T) {
	setTempHome(t)

	// Two suggestions with the same name in one batch: only the first should be applied.
	suggestions := []llm.Suggestion{
		{Name: "gst", Kind: "alias", Template: "git status"},
		{Name: "gst", Kind: "alias", Template: "git status -s"},
	}
	skipped, err := Apply(suggestions)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(skipped) != 1 || skipped[0] != "gst" {
		t.Errorf("expected second 'gst' to be skipped, got %v", skipped)
	}
	installed, _ := aliases.LoadInstalled()
	if len(installed) != 1 {
		t.Errorf("expected 1 entry (first wins), got %d", len(installed))
	}
}
