// Package normalize cleans shell history commands.
package normalize

import (
	"strings"

	"github.com/ntalmon/aka/aka-cli/internal/history"
)

// Normalize trims whitespace and drops empty lines. Order and duplicates are preserved.
func Normalize(entries []history.Entry) []history.Entry {
	result := make([]history.Entry, 0, len(entries))
	for _, e := range entries {
		cmd := strings.TrimSpace(e.Command)
		if cmd == "" {
			continue
		}
		result = append(result, history.Entry{Timestamp: e.Timestamp, Command: cmd})
	}
	return result
}
