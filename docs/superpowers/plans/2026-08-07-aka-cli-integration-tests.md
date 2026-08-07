# aka-cli Integration Tests Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a robust, mock-free integration test suite for `aka-cli` using Go's testing framework and `go-expect` to simulate TUI interactions.

**Architecture:** We will create an `integration/` directory containing standard Go tests. A `TestMain` function will compile the `aka-cli` binary. Individual tests will create isolated temporary directories, setup dummy histories, spawn the CLI via a pseudo-terminal (PTY) using `go-expect`, interact with it, and assert the output and side-effects.

**Tech Stack:** Go, `testing`, `github.com/Netflix/go-expect`, `os/exec`

## Global Constraints
- Do not use any mocks.
- The compiled binary must be run in an isolated environment (temporary `$HOME` / `$XDG_CONFIG_HOME`).
- Tests must pass cleanly when run via `go test ./integration/...`.

---

### Task 1: Test Setup and Binary Compilation

**Files:**
- Create: `integration/main_test.go`
- Create: `integration/helpers_test.go`

**Interfaces:**
- Produces: `binPath` (global string pointing to compiled binary)
- Produces: `setupTestEnv(t *testing.T) (env []string, homeDir string)`

- [ ] **Step 1: Write TestMain to build binary**
```go
// integration/main_test.go
package integration

import (
	"fmt"
	"os"
	"os/exec"
	"testing"
)

var binPath string

func TestMain(m *testing.M) {
	tmpDir, err := os.MkdirTemp("", "aka-integration-build-*")
	if err != nil {
		fmt.Printf("failed to create temp dir: %v\n", err)
		os.Exit(1)
	}
	defer os.RemoveAll(tmpDir)

	binPath = tmpDir + "/aka-cli"
	
	// Build the binary
	cmd := exec.Command("go", "build", "-o", binPath, "../cmd/aka")
	if err := cmd.Run(); err != nil {
		fmt.Printf("failed to build binary: %v\n", err)
		os.Exit(1)
	}

	os.Exit(m.Run())
}
```

- [ ] **Step 2: Write test environment helper**
```go
// integration/helpers_test.go
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
	)
	return env, homeDir
}
```

- [ ] **Step 3: Verify setup with a dummy test**
Write a temporary test in `main_test.go` to ensure `TestMain` runs without panics, then run `go test ./integration/...`.
Expected: PASS

- [ ] **Step 4: Commit**
```bash
git add integration/
git commit -m "test(integration): setup TestMain and helpers"
```

---

### Task 2: Implement `aka init` Test

**Files:**
- Create: `integration/init_test.go`

**Interfaces:**
- Consumes: `binPath`, `setupTestEnv`

- [ ] **Step 1: Write failing expect sequence for init**
```go
// integration/init_test.go
package integration

import (
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	expect "github.com/Netflix/go-expect"
)

func TestInitCommand(t *testing.T) {
	env, homeDir := setupTestEnv(t)

	c, err := expect.NewConsole(expect.WithStdout(os.Stdout))
	if err != nil {
		t.Fatalf("failed to create console: %v", err)
	}
	defer c.Close()

	cmd := exec.Command(binPath, "init")
	cmd.Env = env
	cmd.Stdin = c.Tty()
	cmd.Stdout = c.Tty()
	cmd.Stderr = c.Tty()

	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start cmd: %v", err)
	}

	// This is a test of an existing CLI, so we write the expected behavior
	_, err = c.ExpectString("Choose an AI provider")
	if err != nil {
		t.Errorf("expected prompt for provider: %v", err)
	}

	// Send enter to select default (Anthropic)
	c.SendLine("")
	
	// Expect API key prompt
	c.ExpectString("Enter your API key")
	c.SendLine("dummy-key")

	err = cmd.Wait()
	if err != nil {
		t.Errorf("cmd failed: %v", err)
	}
	
	// Verify aliases.sh created
	if _, err := os.Stat(filepath.Join(homeDir, ".config", "aka", "zsh", "aliases.sh")); os.IsNotExist(err) {
		t.Error("aliases.sh was not created")
	}
}
```

- [ ] **Step 2: Run test and observe pass/fail**
Run: `go test ./integration/init_test.go -v`
Fix any timing/expect string mismatches (e.g., adding timeouts or adjusting exact string prompts based on actual CLI behavior).
Expected: PASS

- [ ] **Step 3: Commit**
```bash
git add integration/init_test.go
git commit -m "test(integration): add test for aka init"
```

---

### Task 3: Implement `aka scan` Network Failure Test

**Files:**
- Create: `integration/scan_test.go`

**Interfaces:**
- Consumes: `binPath`, `setupTestEnv`

- [ ] **Step 1: Write test for scan failure**
```go
// integration/scan_test.go
package integration

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	expect "github.com/Netflix/go-expect"
)

func TestScanCommand_AuthFailure(t *testing.T) {
	env, homeDir := setupTestEnv(t)

	// Seed config with dummy key to bypass init
	configDir := filepath.Join(homeDir, ".config", "aka")
	os.MkdirAll(configDir, 0755)
	os.WriteFile(filepath.Join(configDir, "config.toml"), []byte(`
provider = "anthropic"
api_key = "dummy-key-for-testing"
model = "claude-haiku-4-5"
`), 0644)

	// Seed dummy history
	historyFile := filepath.Join(homeDir, ".zsh_history")
	os.WriteFile(historyFile, []byte("ls -la\ncat file.txt\n"), 0644)

	c, err := expect.NewConsole(expect.WithStdout(os.Stdout))
	if err != nil {
		t.Fatalf("failed to create console: %v", err)
	}
	defer c.Close()

	// Use --history full to avoid interactive history selection for simplicity
	cmd := exec.Command(binPath, "scan", "--history", "full")
	cmd.Env = env
	cmd.Stdin = c.Tty()
	cmd.Stdout = c.Tty()
	cmd.Stderr = c.Tty()

	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start cmd: %v", err)
	}

	// Expect censoring phase review
	_, err = c.ExpectString("Press Enter to send")
	if err != nil {
		t.Errorf("expected censor review prompt: %v", err)
	}
	c.SendLine("")

	// Wait for LLM API failure (invalid dummy key)
	_, err = c.ExpectString("authentication error") // or appropriate error text
	if err != nil {
		t.Errorf("expected auth error: %v", err)
	}

	cmd.Wait() // Don't check err because exit status might be 1 on API failure
}
```

- [ ] **Step 2: Run test and observe**
Run: `go test ./integration/scan_test.go -v`
Iterate on the exact prompt matches and API error strings until it passes reliably.
Expected: PASS

- [ ] **Step 3: Commit**
```bash
git add integration/scan_test.go
git commit -m "test(integration): add test for aka scan API failure"
```
