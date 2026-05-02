// Package history provides shell history file parsers.
package history

import (
	"bufio"
	"os"
	"strconv"
	"strings"
)

// ParseBash parses a .bash_history file and returns a slice of Entry values.
// It handles:
//   - Plain command lines (Timestamp = 0)
//   - Lines starting with "# <timestamp>" from HISTTIMEFORMAT (Timestamp set for next command)
//   - Other comment lines (skipped)
func ParseBash(path string) ([]Entry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var entries []Entry
	var pendingTS int64
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "#") {
			// HISTTIMEFORMAT lines look like "# 1711234567" — pure digits after "# ".
			rest := strings.TrimSpace(line[1:])
			if ts, err := strconv.ParseInt(rest, 10, 64); err == nil {
				pendingTS = ts
			}
			// Other comment lines are ignored; pendingTS stays zero.
			continue
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		entries = append(entries, Entry{Timestamp: pendingTS, Command: line})
		pendingTS = 0
	}
	return entries, scanner.Err()
}
