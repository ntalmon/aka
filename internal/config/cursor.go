package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// HistoryCursor records how many raw history entries were seen at the last
// successful analysis run, so subsequent runs only process new commands.
type HistoryCursor struct {
	Total int `json:"total"`
}

func cursorFilePath(shell string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".config", "aka", shell)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return filepath.Join(dir, "history_cursor.json"), nil
}

// LoadCursor reads the stored cursor for shell. Returns a zero-value cursor
// (Total=0) if the file does not exist yet, which means "no prior run, use full history".
func LoadCursor(shell string) (HistoryCursor, error) {
	path, err := cursorFilePath(shell)
	if err != nil {
		return HistoryCursor{}, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return HistoryCursor{}, nil
	}
	if err != nil {
		return HistoryCursor{}, err
	}
	var c HistoryCursor
	if err := json.Unmarshal(data, &c); err != nil {
		return HistoryCursor{}, err
	}
	return c, nil
}

// SaveCursor persists the cursor for the next run.
func SaveCursor(c HistoryCursor, shell string) error {
	path, err := cursorFilePath(shell)
	if err != nil {
		return err
	}
	data, err := json.Marshal(c)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
