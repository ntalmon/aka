//go:build e2e

package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnvIsolatesHome(t *testing.T) {
	t.Parallel()

	env := NewEnv(t, "zsh")

	if !strings.HasPrefix(env.Home, os.TempDir()) {
		t.Fatalf("Home = %q, want a path under %q", env.Home, os.TempDir())
	}
	if env.Home == os.Getenv("HOME") {
		t.Fatal("Home must not be the real HOME")
	}
}

// An uninitialised shell must be reported as such — this proves $HOME really is
// the temp dir, since the developer's own HOME would already be initialised.
func TestUninitialisedShellIsReported(t *testing.T) {
	t.Parallel()

	env := NewEnv(t, "zsh")
	out, code := env.Run("list")

	if code != 0 {
		t.Fatalf("exit code = %d, want 0\n%s", code, out)
	}
	if !strings.Contains(out, "Shell 'zsh' is not set up with AKA.") {
		t.Fatalf("missing the not-set-up message:\n%s", out)
	}
}

func TestSeedHistoryWritesZshExtendedFormat(t *testing.T) {
	t.Parallel()

	env := NewEnv(t, "zsh")
	env.SeedHistory("git status", "git commit -m 'x'")

	content := env.ReadFile(".zsh_history")
	if !strings.Contains(content, ";git status") {
		t.Fatalf("history missing seeded command:\n%s", content)
	}
	if !strings.HasPrefix(content, ": ") {
		t.Fatalf("history is not in zsh extended format:\n%s", content)
	}
}

func TestSeedHistoryWritesBashPlainFormat(t *testing.T) {
	t.Parallel()

	env := NewEnv(t, "bash")
	env.SeedHistory("ls -la", "cd /tmp")

	content := env.ReadFile(".bash_history")
	if content != "ls -la\ncd /tmp\n" {
		t.Fatalf("bash history = %q", content)
	}
}

func TestSeedConfigWritesRestrictivePermissions(t *testing.T) {
	t.Parallel()

	env := NewEnv(t, "zsh")
	env.SeedConfig(ConfigTOML{Provider: "anthropic", Model: "claude-haiku-4-5-20251001", APIKey: "e2e-key"})

	if mode := env.FileMode(".config", "aka", "config.toml"); mode.Perm() != 0o600 {
		t.Fatalf("config.toml mode = %o, want 600", mode.Perm())
	}
	if !strings.Contains(env.ReadFile(".config", "aka", "config.toml"), "e2e-key") {
		t.Fatal("config.toml missing the seeded key")
	}
}

func TestRunShellExecutesInIsolatedHome(t *testing.T) {
	t.Parallel()

	env := NewEnv(t, "zsh")
	out, code := env.RunShell("echo $HOME")

	if code != 0 {
		t.Fatalf("exit code = %d\n%s", code, out)
	}
	if strings.TrimSpace(out) != env.Home {
		t.Fatalf("shell $HOME = %q, want %q", strings.TrimSpace(out), env.Home)
	}
}

func TestSpawnDrivesAkaOverPTY(t *testing.T) {
	t.Parallel()

	env := NewEnv(t, "zsh")
	c := env.Spawn("--help")
	c.Expect("reads your shell history, censors sensitive data")
	if code := c.Wait(); code != 0 {
		t.Fatalf("exit code = %d", code)
	}
}

func TestPathJoinsUnderHome(t *testing.T) {
	t.Parallel()

	env := NewEnv(t, "zsh")
	want := filepath.Join(env.Home, ".config", "aka", "zsh")
	if got := env.Path(".config", "aka", "zsh"); got != want {
		t.Fatalf("Path = %q, want %q", got, want)
	}
}
