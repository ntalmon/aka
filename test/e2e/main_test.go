//go:build e2e

package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// akaBin is where TestMain installs the binary. Every scenario invokes `aka`
// off $PATH so the tests exercise exactly what a user would run.
const akaBin = "/usr/local/bin/aka"

// containerGuardEnv is set by the Makefile. Without it the suite refuses to
// run: TestMain installs to /usr/local/bin, which on a developer's machine
// would overwrite their real aka binary.
const containerGuardEnv = "AKA_E2E_INSIDE_CONTAINER"

func TestMain(m *testing.M) {
	if os.Getenv(containerGuardEnv) != "1" {
		fmt.Fprintf(os.Stderr,
			"refusing to run: %s is not set.\n"+
				"These tests install to %s and must run inside the e2e container.\n"+
				"Run `make e2e` instead.\n", containerGuardEnv, akaBin)
		os.Exit(1)
	}
	if err := buildAndInstall(); err != nil {
		fmt.Fprintln(os.Stderr, "e2e setup failed:", err)
		os.Exit(1)
	}
	os.Exit(m.Run())
}

// buildAndInstall compiles ./cmd/aka and installs it to akaBin, mirroring what
// a user gets from install.sh.
func buildAndInstall() error {
	tmp := filepath.Join(os.TempDir(), "aka-e2e-build")

	build := exec.Command("go", "build", "-o", tmp, "./cmd/aka")
	build.Dir = repoRoot()
	build.Stdout = os.Stderr
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		return fmt.Errorf("go build ./cmd/aka: %w", err)
	}

	install := exec.Command("install", "-m", "755", tmp, akaBin)
	install.Stdout = os.Stderr
	install.Stderr = os.Stderr
	if err := install.Run(); err != nil {
		return fmt.Errorf("install %s: %w", akaBin, err)
	}
	return nil
}

// repoRoot returns the module root. Tests run with the working directory set
// to test/e2e, so the root is two levels up.
func repoRoot() string {
	abs, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		panic(fmt.Sprintf("resolve repo root: %v", err))
	}
	return abs
}

func TestBinaryIsInstalledOnPath(t *testing.T) {
	t.Parallel()

	resolved, err := exec.LookPath("aka")
	if err != nil {
		t.Fatalf("aka not found on $PATH: %v", err)
	}
	if resolved != akaBin {
		t.Fatalf("aka resolved to %q, want %q", resolved, akaBin)
	}

	out, err := exec.Command("aka", "--version").CombinedOutput()
	if err != nil {
		t.Fatalf("aka --version failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "aka") {
		t.Fatalf("unexpected --version output: %q", out)
	}
}

func TestRequiredShellsArePresent(t *testing.T) {
	t.Parallel()

	for _, sh := range []string{"bash", "zsh"} {
		if _, err := exec.LookPath(sh); err != nil {
			t.Errorf("%s missing from the container image: %v", sh, err)
		}
	}
}
