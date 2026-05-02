package history

import (
	"path/filepath"
	"testing"
)

func TestParseBashPlain(t *testing.T) {
	entries, err := ParseBash(filepath.Join("..", "..", "testdata", "history", "bash_plain.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("expected commands, got none")
	}
	for _, e := range entries {
		if len(e.Command) > 0 && e.Command[0] == '#' {
			t.Errorf("comment line leaked through: %q", e.Command)
		}
	}
}

func TestParseZshExtended(t *testing.T) {
	entries, err := ParseZsh(filepath.Join("..", "..", "testdata", "history", "zsh_extended.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("expected commands, got none")
	}
	for _, e := range entries {
		if len(e.Command) > 2 && e.Command[0] == ':' && e.Command[1] == ' ' {
			t.Errorf("raw extended line leaked through: %q", e.Command)
		}
	}
}

func TestParseZshTimestamps(t *testing.T) {
	entries, err := ParseZsh(filepath.Join("..", "..", "testdata", "history", "zsh_extended.txt"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Timestamp == 0 {
			t.Errorf("expected non-zero timestamp for %q", e.Command)
		}
	}
	// Verify ordering: timestamps should be monotonically non-decreasing.
	for i := 1; i < len(entries); i++ {
		if entries[i].Timestamp < entries[i-1].Timestamp {
			t.Errorf("timestamps out of order at index %d: %d < %d", i, entries[i].Timestamp, entries[i-1].Timestamp)
		}
	}
}
