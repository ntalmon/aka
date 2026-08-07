package integration

import (
	"os"
	"path/filepath"
	"testing"
)

func setupTestEnv(t *testing.T) (env []string, homeDir string) {
	t.Helper()
	homeDir = t.TempDir()

	env = append(os.Environ(),
		"HOME="+homeDir,
		"XDG_CONFIG_HOME="+filepath.Join(homeDir, ".config"),
		"SHELL=zsh",
		"TERM=dumb",
		"NO_COLOR=1",
	)
	return env, homeDir
}
