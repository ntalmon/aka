package aliases

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestInitIdempotent(t *testing.T) {
	// Use a temp dir as HOME.
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	rcFile := filepath.Join(tmpHome, ".zshrc")
	// Create empty rc file.
	if err := os.WriteFile(rcFile, []byte("# existing content\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// First init.
	if err := Init(rcFile); err != nil {
		t.Fatalf("first Init: %v", err)
	}

	data1, _ := os.ReadFile(rcFile)

	// Second init (idempotent).
	if err := Init(rcFile); err != nil {
		t.Fatalf("second Init: %v", err)
	}

	data2, _ := os.ReadFile(rcFile)

	if string(data1) != string(data2) {
		t.Errorf("Init is not idempotent: rc file changed on second run")
	}

	sourceLine := `[ -f "$HOME/.config/aka/aliases.sh" ] && . "$HOME/.config/aka/aliases.sh"`
	count := strings.Count(string(data2), sourceLine)
	if count != 1 {
		t.Errorf("expected exactly 1 source line, got %d", count)
	}
}

func TestWriteAliasesFileSortedAndDeterministic(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	entries := []InstalledEntry{
		{Name: "zfoo", Kind: "alias", Template: "echo z", CreatedAt: time.Now()},
		{Name: "abar", Kind: "alias", Template: "echo a", CreatedAt: time.Now()},
		{Name: "mfunc", Kind: "function", Template: `git checkout "$1"`, Params: []Param{{Name: "branch", Type: "string"}}, CreatedAt: time.Now()},
	}

	if err := WriteAliasesFile(entries); err != nil {
		t.Fatalf("WriteAliasesFile: %v", err)
	}

	path, _ := AliasesFilePath()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)

	// abar should come before mfunc, mfunc before zfoo.
	iAbar := strings.Index(content, "abar")
	iMfunc := strings.Index(content, "mfunc")
	iZfoo := strings.Index(content, "zfoo")
	if !(iAbar < iMfunc && iMfunc < iZfoo) {
		t.Errorf("entries not sorted: abar=%d mfunc=%d zfoo=%d", iAbar, iMfunc, iZfoo)
	}

	// Write again — should be same content (modulo timestamp).
	if err := WriteAliasesFile(entries); err != nil {
		t.Fatalf("second WriteAliasesFile: %v", err)
	}
	data2, _ := os.ReadFile(path)
	// Strip the timestamp line for comparison.
	strip := func(s string) string {
		var lines []string
		for _, l := range strings.Split(s, "\n") {
			if strings.HasPrefix(l, "# Last updated:") {
				continue
			}
			lines = append(lines, l)
		}
		return strings.Join(lines, "\n")
	}
	if strip(string(data)) != strip(string(data2)) {
		t.Errorf("WriteAliasesFile is not deterministic")
	}
}

func TestLoadSaveInstalled(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	entries := []InstalledEntry{
		{Name: "gst", Kind: "alias", Template: "git status", Source: "analyze", CreatedAt: time.Now()},
	}
	if err := SaveInstalled(entries); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadInstalled()
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 1 || loaded[0].Name != "gst" {
		t.Errorf("unexpected loaded entries: %v", loaded)
	}
}
