//go:build e2e

package e2e

import (
	"os/exec"
	"testing"
)

func TestConsoleReadsAndWrites(t *testing.T) {
	t.Parallel()

	c := newConsole(t, exec.Command("sh", "-c", `printf 'name? '; read x; printf 'hello %s\n' "$x"`))
	c.Expect("name? ")
	c.SendLine("world")
	c.Expect("hello world")

	if code := c.Wait(); code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
}

// Expect must consume: each call searches only after the previous match. Without
// this, a bubbletea redraw lets a later Expect match a stale frame and pass for
// the wrong reason.
func TestExpectConsumesPreviousMatch(t *testing.T) {
	t.Parallel()

	c := newConsole(t, exec.Command("sh", "-c", `printf 'ready\n'; printf 'ready\n'; printf 'done\n'`))
	c.Expect("ready") // first
	c.Expect("ready") // second — only passes if the first was consumed
	c.Expect("done")
	if code := c.Wait(); code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
}

func TestConsoleStripsANSI(t *testing.T) {
	t.Parallel()

	// Emits red "styled" surrounded by SGR escapes, plus a cursor-hide sequence.
	c := newConsole(t, exec.Command("sh", "-c", `printf '\033[?25l\033[31mstyled\033[0m\n'`))
	c.Expect("styled")
	if code := c.Wait(); code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if got := c.Screen(); got != "styled\n" {
		t.Fatalf("Screen() = %q, want %q", got, "styled\n")
	}
}

func TestConsolePropagatesExitCode(t *testing.T) {
	t.Parallel()

	c := newConsole(t, exec.Command("sh", "-c", "exit 3"))
	if code := c.Wait(); code != 3 {
		t.Fatalf("exit code = %d, want 3", code)
	}
}

func TestNotExpect(t *testing.T) {
	t.Parallel()

	c := newConsole(t, exec.Command("sh", "-c", `printf 'alpha\n'`))
	c.Expect("alpha")
	if code := c.Wait(); code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	c.NotExpect("beta")
}
