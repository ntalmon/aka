package history

import (
	"os"
	"path/filepath"
)

// Entry is a single history record. Timestamp is a Unix epoch second; zero means unknown.
type Entry struct {
	Timestamp int64
	Command   string
}

// ReadAll reads history for the given shell. If shell is empty or unknown,
// it tries to merge both bash and zsh history. Fish is not supported in v0.1.
func ReadAll(shell string) ([]Entry, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}

	switch shell {
	case "bash":
		return ParseBash(filepath.Join(home, ".bash_history"))
	case "zsh":
		return ParseZsh(filepath.Join(home, ".zsh_history"))
	default:
		// Try both and merge.
		var all []Entry
		if entries, err := ParseBash(filepath.Join(home, ".bash_history")); err == nil {
			all = append(all, entries...)
		}
		if entries, err := ParseZsh(filepath.Join(home, ".zsh_history")); err == nil {
			all = append(all, entries...)
		}
		return all, nil
	}
}
