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
	if err := Init(rcFile, "zsh"); err != nil {
		t.Fatalf("first Init: %v", err)
	}

	data1, _ := os.ReadFile(rcFile)

	// Second init (idempotent).
	if err := Init(rcFile, "zsh"); err != nil {
		t.Fatalf("second Init: %v", err)
	}

	data2, _ := os.ReadFile(rcFile)

	if string(data1) != string(data2) {
		t.Errorf("Init is not idempotent: rc file changed on second run")
	}

	sourceLine := `[ -f "$HOME/.config/aka/zsh/aliases.sh" ] && . "$HOME/.config/aka/zsh/aliases.sh"`
	count := strings.Count(string(data2), sourceLine)
	if count != 1 {
		t.Errorf("expected exactly 1 source line, got %d", count)
	}
}

func TestInitWrapperContainsShell(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	for _, shell := range []string{"bash", "zsh"} {
		rcFile := filepath.Join(tmpHome, "."+shell+"rc")
		if err := os.WriteFile(rcFile, []byte("# existing\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := Init(rcFile, shell); err != nil {
			t.Fatalf("Init(%s): %v", shell, err)
		}
		data, _ := os.ReadFile(rcFile)
		content := string(data)
		if !strings.Contains(content, "AKA_SHELL="+shell) {
			t.Errorf("shell=%s: wrapper missing AKA_SHELL=%s", shell, shell)
		}
		expectedPath := "$HOME/.config/aka/" + shell + "/aliases.sh"
		if !strings.Contains(content, expectedPath) {
			t.Errorf("shell=%s: wrapper missing path %s", shell, expectedPath)
		}
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

	if err := WriteAliasesFile(entries, "zsh"); err != nil {
		t.Fatalf("WriteAliasesFile: %v", err)
	}

	path, _ := AliasesFilePath("zsh")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content := string(data)

	// Aliases section (abar, zfoo) should precede the functions section (mfunc).
	// Within the aliases section, abar < zfoo alphabetically.
	iAbar := strings.Index(content, "abar")
	iMfunc := strings.Index(content, "mfunc")
	iZfoo := strings.Index(content, "zfoo")
	if iAbar >= iZfoo || iZfoo >= iMfunc {
		t.Errorf("unexpected section order: abar=%d zfoo=%d mfunc=%d", iAbar, iZfoo, iMfunc)
	}

	// Write again — should be same content (modulo timestamp).
	if err := WriteAliasesFile(entries, "zsh"); err != nil {
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
		{Name: "gst", Kind: "alias", Template: "git status", Source: "scan", CreatedAt: time.Now()},
	}
	if err := SaveInstalled(entries, "zsh"); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadInstalled("zsh")
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 1 || loaded[0].Name != "gst" {
		t.Errorf("unexpected loaded entries: %v", loaded)
	}
}

func TestMigrateFromLegacy(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	// Create legacy flat layout.
	legacyDir := filepath.Join(tmpHome, ".config", "aka")
	if err := os.MkdirAll(legacyDir, 0o700); err != nil {
		t.Fatal(err)
	}
	legacyAliases := "alias gs='git status'\n"
	if err := os.WriteFile(filepath.Join(legacyDir, "aliases.sh"), []byte(legacyAliases), 0o600); err != nil {
		t.Fatal(err)
	}
	legacyInstalled := `[{"name":"gs","kind":"alias","template":"git status","params":null,"created_at":"2024-01-01T00:00:00Z","source":"scan"}]`
	if err := os.WriteFile(filepath.Join(legacyDir, "installed.json"), []byte(legacyInstalled), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := migrateFromLegacy("zsh"); err != nil {
		t.Fatalf("migrateFromLegacy: %v", err)
	}

	// aliases.sh should be in ~/.config/aka/zsh/
	migratedPath := filepath.Join(legacyDir, "zsh", "aliases.sh")
	data, err := os.ReadFile(migratedPath)
	if err != nil {
		t.Fatalf("migrated aliases.sh not found: %v", err)
	}
	if string(data) != legacyAliases {
		t.Errorf("migrated aliases.sh content mismatch")
	}

	// Running again should be a no-op (installed.json already present in shell dir).
	if err := migrateFromLegacy("zsh"); err != nil {
		t.Fatalf("second migrateFromLegacy: %v", err)
	}
}
