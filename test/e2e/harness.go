//go:build e2e

package e2e

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/ntalmon/aka/internal/aliases"
	"github.com/ntalmon/aka/test/e2e/fakellm"
)

// Env is one isolated aka installation: a throwaway $HOME plus helpers to seed
// fixtures, run the binary, and inspect what it wrote.
//
// Everything crosses the process boundary — argv, PTY, exit code, and files
// under Home. No product function is ever called in-process.
type Env struct {
	t        *testing.T
	Home     string
	Shell    string // "zsh" or "bash"
	LLM      *fakellm.Server
	extraEnv []string
}

// NewEnv creates an isolated environment for the given shell.
func NewEnv(t *testing.T, shell string) *Env {
	t.Helper()
	if shell != "zsh" && shell != "bash" {
		t.Fatalf("unsupported shell %q", shell)
	}
	e := &Env{t: t, Home: t.TempDir(), Shell: shell}
	e.LLM = fakellm.New(t)
	e.SetEnv("AKA_LLM_BASE_URL=" + e.LLM.URL())
	return e
}

// SetEnv adds variables to every subsequent aka invocation.
func (e *Env) SetEnv(kv ...string) {
	e.extraEnv = append(e.extraEnv, kv...)
}

// ConfigTOML is the subset of config.toml the tests seed.
type ConfigTOML struct {
	Provider string
	Model    string
	APIKey   string
}

// SeedConfig writes ~/.config/aka/config.toml so aka does not prompt for a key.
func (e *Env) SeedConfig(c ConfigTOML) {
	e.t.Helper()

	dir := e.Path(".config", "aka")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		e.t.Fatalf("mkdir %s: %v", dir, err)
	}

	keyField := "anthropic_api_key"
	switch c.Provider {
	case "groq":
		keyField = "groq_api_key"
	case "openai":
		keyField = "openai_api_key"
	case "gemini":
		keyField = "gemini_api_key"
	}

	body := fmt.Sprintf("provider = %q\nmodel = %q\nmax_history = 500\n%s = %q\n",
		c.Provider, c.Model, keyField, c.APIKey)

	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		e.t.Fatalf("write config.toml: %v", err)
	}
}

// SeedHistory writes the commands to the shell's history file, spacing
// synthetic timestamps 60 seconds apart so they read as one work session.
func (e *Env) SeedHistory(cmds ...string) {
	e.t.Helper()

	var sb strings.Builder
	if e.Shell == "zsh" {
		ts := time.Now().Add(-time.Duration(len(cmds)) * time.Minute).Unix()
		for _, cmd := range cmds {
			fmt.Fprintf(&sb, ": %d:0;%s\n", ts, cmd)
			ts += 60
		}
		e.writeHome(".zsh_history", sb.String(), 0o600)
		return
	}
	for _, cmd := range cmds {
		fmt.Fprintf(&sb, "%s\n", cmd)
	}
	e.writeHome(".bash_history", sb.String(), 0o600)
}

// SeedRC writes the shell's rc file with pre-existing content, so tests can
// prove `aka init` appends rather than clobbers.
func (e *Env) SeedRC(content string) {
	e.t.Helper()
	e.writeHome(e.rcName(), content, 0o644)
}

func (e *Env) rcName() string {
	if e.Shell == "zsh" {
		return ".zshrc"
	}
	return ".bashrc"
}

func (e *Env) writeHome(name, content string, mode os.FileMode) {
	e.t.Helper()
	if err := os.WriteFile(e.Path(name), []byte(content), mode); err != nil {
		e.t.Fatalf("write %s: %v", name, err)
	}
}

// environ returns the process environment for an aka invocation.
func (e *Env) environ(extra ...string) []string {
	env := []string{
		"HOME=" + e.Home,
		"AKA_SHELL=" + e.Shell,
		"TERM=xterm-256color",
		"PATH=" + os.Getenv("PATH"),
		"LANG=C.UTF-8",
	}
	return append(env, extra...)
}

// Spawn runs aka attached to a PTY, for interactive flows.
func (e *Env) Spawn(args ...string) *Console {
	e.t.Helper()
	cmd := exec.Command(akaBin, args...)
	cmd.Env = e.environ(e.extraEnv...)
	cmd.Dir = e.Home
	return newConsole(e.t, cmd)
}

// Run executes aka without a terminal and returns combined output and the exit
// code. Use it only for flows that never prompt.
func (e *Env) Run(args ...string) (string, int) {
	e.t.Helper()
	cmd := exec.Command(akaBin, args...)
	cmd.Env = e.environ(e.extraEnv...)
	cmd.Dir = e.Home
	out, err := cmd.CombinedOutput()
	return string(out), exitCodeOf(e.t, err, out)
}

// RunShell runs script in a real interactive shell with the isolated HOME, so
// the rc file and any sourced aliases are actually loaded.
func (e *Env) RunShell(script string) (string, int) {
	e.t.Helper()
	cmd := exec.Command(e.Shell, "-i", "-c", script)
	cmd.Env = e.environ(e.extraEnv...)
	cmd.Dir = e.Home
	out, err := cmd.CombinedOutput()
	return string(out), exitCodeOf(e.t, err, out)
}

func exitCodeOf(t *testing.T, err error, out []byte) int {
	t.Helper()
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	t.Fatalf("command failed to run: %v\n%s", err, out)
	return -1
}

// Path joins rel under the isolated HOME.
func (e *Env) Path(rel ...string) string {
	return filepath.Join(append([]string{e.Home}, rel...)...)
}

// ShellPath joins rel under ~/.config/aka/<shell>/.
func (e *Env) ShellPath(rel ...string) string {
	return e.Path(append([]string{".config", "aka", e.Shell}, rel...)...)
}

// ReadFile reads a file under HOME, failing the test if it is missing.
func (e *Env) ReadFile(rel ...string) string {
	e.t.Helper()
	data, err := os.ReadFile(e.Path(rel...))
	if err != nil {
		e.t.Fatalf("read %s: %v", filepath.Join(rel...), err)
	}
	return string(data)
}

// FileMode returns the mode of a file under HOME.
func (e *Env) FileMode(rel ...string) os.FileMode {
	e.t.Helper()
	info, err := os.Stat(e.Path(rel...))
	if err != nil {
		e.t.Fatalf("stat %s: %v", filepath.Join(rel...), err)
	}
	return info.Mode()
}

// AliasesFile returns the contents of the managed aliases.sh.
func (e *Env) AliasesFile() string {
	e.t.Helper()
	data, err := os.ReadFile(e.ShellPath("aliases.sh"))
	if err != nil {
		e.t.Fatalf("read aliases.sh: %v", err)
	}
	return string(data)
}

// Installed unmarshals installed.json. Returns nil when the file is absent.
func (e *Env) Installed() []aliases.InstalledEntry {
	e.t.Helper()
	data, err := os.ReadFile(e.ShellPath("installed.json"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		e.t.Fatalf("read installed.json: %v", err)
	}
	var entries []aliases.InstalledEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		e.t.Fatalf("unmarshal installed.json: %v\n%s", err, data)
	}
	return entries
}

// runCommand runs an arbitrary command in the isolated environment (same HOME,
// PATH, and extra env as aka invocations) and returns combined output and exit
// code. Used to shell out to `zsh -n`/`bash -n` etc. against files under e.Home.
func runCommand(t *testing.T, e *Env, name string, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Env = e.environ(e.extraEnv...)
	cmd.Dir = e.Home
	out, err := cmd.CombinedOutput()
	return string(out), exitCodeOf(t, err, out)
}

// Backups lists the timestamped snapshot filenames, sorted.
func (e *Env) Backups() []string {
	e.t.Helper()
	dirEntries, err := os.ReadDir(e.ShellPath("backups"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		e.t.Fatalf("read backups dir: %v", err)
	}
	var names []string
	for _, d := range dirEntries {
		names = append(names, d.Name())
	}
	sort.Strings(names)
	return names
}
