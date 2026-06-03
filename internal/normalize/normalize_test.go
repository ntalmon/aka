package normalize

import (
	"testing"

	"github.com/ntalmon/aka/internal/history"
)

func TestNormalizeDropsEmptyAndWhitespace(t *testing.T) {
	entries := []history.Entry{
		{Command: ""},
		{Command: "   "},
		{Command: "\t"},
		{Command: "git status"},
	}
	result := Normalize(entries)
	if len(result) != 1 {
		t.Errorf("expected 1 entry, got %d: %v", len(result), result)
	}
}

func TestNormalizePreservesOrderAndDuplicates(t *testing.T) {
	entries := []history.Entry{
		{Timestamp: 1, Command: "git status"},
		{Timestamp: 2, Command: "ls"},
		{Timestamp: 3, Command: "git status"},
	}
	result := Normalize(entries)
	if len(result) != 3 {
		t.Fatalf("expected 3 entries (duplicates kept), got %d", len(result))
	}
	if result[0].Command != "git status" || result[1].Command != "ls" || result[2].Command != "git status" {
		t.Errorf("order not preserved: %v", result)
	}
}

func TestNormalizeTrimsWhitespace(t *testing.T) {
	entries := []history.Entry{
		{Command: "  git status  "},
	}
	result := Normalize(entries)
	if len(result) != 1 || result[0].Command != "git status" {
		t.Errorf("expected trimmed 'git status', got %v", result)
	}
}

func TestNormalizeEmptyInput(t *testing.T) {
	if got := Normalize(nil); len(got) != 0 {
		t.Errorf("expected empty result for nil input, got %v", got)
	}
	if got := Normalize([]history.Entry{}); len(got) != 0 {
		t.Errorf("expected empty result for empty input, got %v", got)
	}
}

func TestNormalizePreservesTrivialCommands(t *testing.T) {
	entries := []history.Entry{
		{Command: "ls"},
		{Command: "cd ~/projects"},
		{Command: "git status"},
	}
	result := Normalize(entries)
	if len(result) != 3 {
		t.Errorf("expected all 3 entries kept, got %d: %v", len(result), result)
	}
}
