// Package normalize deduplicates and cleans shell history commands.
package normalize

import (
	"strings"
	"unicode"

	"github.com/ntalmon/aka/aka-cli/internal/history"
)

// trivialCommands is the set of single-token commands that are never useful as aliases.
var trivialCommands = map[string]bool{
	"ls":      true,
	"cd":      true,
	"clear":   true,
	"pwd":     true,
	"exit":    true,
	"history": true,
	"q":       true,
	"quit":    true,
}

// Normalize deduplicates (keeping the last occurrence), drops trivial commands,
// trims whitespace, and drops empty lines. The timestamp of the last occurrence
// is preserved on each retained entry.
func Normalize(entries []history.Entry) []history.Entry {
	// Track the index of the last occurrence of each command string.
	lastIdx := make(map[string]int)
	for i, e := range entries {
		cmd := strings.TrimSpace(e.Command)
		if cmd != "" {
			lastIdx[cmd] = i
		}
	}

	result := make([]history.Entry, 0, len(entries))
	emitted := make(map[string]bool)

	for i, e := range entries {
		cmd := strings.TrimSpace(e.Command)
		if cmd == "" {
			continue
		}
		if lastIdx[cmd] != i {
			continue
		}
		if emitted[cmd] {
			continue
		}
		if isTrivial(cmd) {
			continue
		}
		result = append(result, history.Entry{Timestamp: e.Timestamp, Command: cmd})
		emitted[cmd] = true
	}
	return result
}

// isTrivial returns true for commands that shouldn't become aliases.
func isTrivial(cmd string) bool {
	tokens := splitTokens(cmd)
	if len(tokens) == 0 {
		return true
	}
	bin := tokens[0]
	if trivialCommands[bin] {
		return true
	}
	if len(tokens) == 1 {
		return true
	}
	return false
}

// splitTokens splits a command string into tokens by whitespace.
func splitTokens(cmd string) []string {
	return strings.FieldsFunc(cmd, unicode.IsSpace)
}
