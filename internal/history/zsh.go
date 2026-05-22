package history

import (
	"bufio"
	"os"
	"strconv"
	"strings"
)

// ParseZsh parses a .zsh_history file and returns a slice of Entry values.
// It handles:
//   - Plain newline-delimited commands (Timestamp = 0)
//   - Extended format: ": <timestamp>:<duration>;<command>" (Timestamp set)
//   - Multi-line commands escaped with "\"
func ParseZsh(path string) ([]Entry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	var entries []Entry
	var current strings.Builder
	var currentTS int64
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()

		// Extended format: ": 1234567890:0;command"
		if strings.HasPrefix(line, ": ") {
			// Flush any accumulated multi-line command.
			if current.Len() > 0 {
				if cmd := strings.TrimSpace(current.String()); cmd != "" {
					entries = append(entries, Entry{Timestamp: currentTS, Command: cmd})
				}
				current.Reset()
				currentTS = 0
			}
			// Parse ": TS:DURATION;CMD"
			rest := line[2:] // strip leading ": "
			semi := strings.Index(rest, ";")
			if semi < 0 {
				continue
			}
			tsField := rest[:semi]
			cmd := rest[semi+1:]
			// tsField is "TS:DURATION" — take the part before the colon.
			if colon := strings.Index(tsField, ":"); colon >= 0 {
				if ts, err := strconv.ParseInt(tsField[:colon], 10, 64); err == nil {
					currentTS = ts
				}
			}
			if strings.HasSuffix(cmd, "\\") {
				current.WriteString(cmd[:len(cmd)-1])
				current.WriteString("\n")
			} else {
				cmd = strings.TrimSpace(cmd)
				if cmd != "" {
					entries = append(entries, Entry{Timestamp: currentTS, Command: cmd})
				}
				currentTS = 0
			}
			continue
		}

		// Continuation of a multi-line command.
		if current.Len() > 0 {
			if strings.HasSuffix(line, "\\") {
				current.WriteString(line[:len(line)-1])
				current.WriteString("\n")
			} else {
				current.WriteString(line)
				if cmd := strings.TrimSpace(current.String()); cmd != "" {
					entries = append(entries, Entry{Timestamp: currentTS, Command: cmd})
				}
				current.Reset()
				currentTS = 0
			}
			continue
		}

		// Plain line — may start a multi-line command.
		if strings.HasSuffix(line, "\\") {
			current.WriteString(line[:len(line)-1])
			current.WriteString("\n")
			continue
		}

		line = strings.TrimSpace(line)
		if line != "" {
			entries = append(entries, Entry{Command: line})
		}
	}

	// Flush trailing continuation.
	if current.Len() > 0 {
		if cmd := strings.TrimSpace(current.String()); cmd != "" {
			entries = append(entries, Entry{Timestamp: currentTS, Command: cmd})
		}
	}

	return entries, scanner.Err()
}
