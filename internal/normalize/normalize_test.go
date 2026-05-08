package normalize

import (
	"testing"

	"github.com/ntalmon/aka/aka-cli/internal/history"
)

func TestNormalizeDeduplicatesKeepingLast(t *testing.T) {
	entries := []history.Entry{
		{Timestamp: 1, Command: "git status"},
		{Timestamp: 2, Command: "git log"},
		{Timestamp: 3, Command: "git status"},
	}
	result := Normalize(entries)
	count := 0
	var ts int64
	for _, e := range result {
		if e.Command == "git status" {
			count++
			ts = e.Timestamp
		}
	}
	if count != 1 {
		t.Errorf("expected 1 'git status', got %d", count)
	}
	if ts != 3 {
		t.Errorf("expected timestamp 3 (last occurrence), got %d", ts)
	}
}

func TestNormalizeDropsKnownTrivialCommands(t *testing.T) {
	trivial := []string{"ls", "cd", "clear", "pwd", "exit", "history", "q", "quit"}
	var entries []history.Entry
	for _, cmd := range trivial {
		entries = append(entries, history.Entry{Command: cmd})
	}
	entries = append(entries, history.Entry{Command: "git status"})

	result := Normalize(entries)
	for _, e := range result {
		for _, t2 := range trivial {
			if e.Command == t2 {
				t.Errorf("trivial command %q should have been dropped", e.Command)
			}
		}
	}
	if len(result) != 1 || result[0].Command != "git status" {
		t.Errorf("expected only 'git status', got %v", result)
	}
}

func TestNormalizeDropsSingleTokenCommands(t *testing.T) {
	entries := []history.Entry{
		{Command: "vim"},
		{Command: "make"},
		{Command: "top"},
		{Command: "git status"},
	}
	result := Normalize(entries)
	for _, e := range result {
		if e.Command == "vim" || e.Command == "make" || e.Command == "top" {
			t.Errorf("single-token command %q should have been dropped", e.Command)
		}
	}
	if len(result) != 1 || result[0].Command != "git status" {
		t.Errorf("expected only 'git status', got %v", result)
	}
}

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

func TestNormalizePreservesOriginalOrder(t *testing.T) {
	entries := []history.Entry{
		{Timestamp: 1, Command: "git status"},
		{Timestamp: 2, Command: "go test ./..."},
		{Timestamp: 3, Command: "docker ps"},
	}
	result := Normalize(entries)
	if len(result) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(result))
	}
	if result[0].Command != "git status" || result[1].Command != "go test ./..." || result[2].Command != "docker ps" {
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

func TestNormalizeDuplicateTimestampCarriesLast(t *testing.T) {
	// When a command appears multiple times, the last occurrence's timestamp is kept.
	entries := []history.Entry{
		{Timestamp: 10, Command: "go build ./..."},
		{Timestamp: 99, Command: "git diff"},
		{Timestamp: 50, Command: "go build ./..."},
	}
	result := Normalize(entries)
	for _, e := range result {
		if e.Command == "go build ./..." && e.Timestamp != 50 {
			t.Errorf("expected timestamp 50 (last occurrence), got %d", e.Timestamp)
		}
	}
}
