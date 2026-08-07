//go:build e2e

package e2e

import (
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
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
//
// The three Expect calls alone do NOT prove this: a completely non-consuming
// Expect (e.g. `strings.Contains(c.Screen(), substr)` in a loop, never
// touching an offset) would pass all three just as well, since "ready" stays
// present in the ever-growing buffer regardless of whether it was "already
// matched". It only catches over-aggressive offset advancement, not the
// missing-advancement case this test exists to guard.
//
// So after the three calls, we additionally assert on the *unconsumed tail*
// of the stream by reading c.offset/c.stripped directly (this file is in the
// same package, so that's an ordinary white-box check, not a hack). The
// output is exactly "ready\nready\ndone\n" (17 bytes); a correctly-consuming
// Expect leaves the offset sitting right after the "done" match, i.e. with
// only the trailing "\n" left unconsumed. A non-consuming implementation
// leaves offset at (or near) 0, so the unconsumed tail would still be most
// or all of the 17-byte buffer — a easy, deterministic distinction that
// needs no timeout.
//
// Verified by temporarily stubbing Expect to
// `strings.Contains(c.Screen(), substr)` (dropping the offset update
// entirely): with that stub, this test's tail-length assertion fails as
// expected (17 bytes unconsumed vs. the required 1), while it still failed
// to distinguish anything using only the original three bare Expect calls.
func TestExpectConsumesPreviousMatch(t *testing.T) {
	t.Parallel()

	c := newConsole(t, exec.Command("sh", "-c", `printf 'ready\n'; printf 'ready\n'; printf 'done\n'`))
	c.Expect("ready") // first
	c.Expect("ready") // second — only passes if the first was consumed
	c.Expect("done")
	if code := c.Wait(); code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}

	c.mu.Lock()
	unconsumed := strings.Clone(c.stripped.String()[c.offset:])
	c.mu.Unlock()
	if unconsumed != "\n" {
		t.Fatalf("unconsumed tail after 3 Expects = %q, want %q (only the trailing newline); "+
			"a longer tail means Expect is not consuming through its match", unconsumed, "\n")
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

// readLoop strips each Read() chunk independently, so a CSI/OSC escape
// sequence split across two 4096-byte reads (a full-screen bubbletea/huh
// redraw can exceed that in one write) must not leak raw escape bytes into
// the stripped buffer. This tests the chunking logic (splitTrailingEscape)
// directly with a deliberately pre-split sequence, rather than relying on
// real PTY read() boundaries lining up a particular way — the kernel's
// chunking of PTY output isn't something a test can reliably force, so a
// direct unit test is the only deterministic way to prove this.
func TestSplitTrailingEscapeHoldsBackPartialSequence(t *testing.T) {
	t.Parallel()

	// "\x1b[31m" (SGR red) split right after the CSI introducer, as if Read()
	// returned "plain\x1b[3" in one chunk and "1mstyled\x1b[0m\n" in the next.
	first := []byte("plain\x1b[3")
	second := []byte("1mstyled\x1b[0m\n")

	safe1, pending1 := splitTrailingEscape(first)
	if string(safe1) != "plain" {
		t.Fatalf("safe1 = %q, want %q", safe1, "plain")
	}
	if string(pending1) != "\x1b[3" {
		t.Fatalf("pending1 = %q, want %q", pending1, "\x1b[3")
	}
	// Stripping the safe prefix alone must not corrupt or half-consume the
	// held-back bytes.
	if got := stripANSI(string(safe1)); got != "plain" {
		t.Fatalf("stripANSI(safe1) = %q, want %q", got, "plain")
	}

	combined := append(append([]byte{}, pending1...), second...)
	safe2, pending2 := splitTrailingEscape(combined)
	if len(pending2) != 0 {
		t.Fatalf("pending2 = %q, want empty: the sequence completes and only a "+
			"trailing newline (guaranteed plain, since it's the last ESC) follows", pending2)
	}
	if got := stripANSI(string(safe1)) + stripANSI(string(safe2)); got != "plainstyled\n" {
		t.Fatalf("reassembled stripped output = %q, want %q", got, "plainstyled\n")
	}

	// Sanity check: a chunk with no ESC at all is entirely safe and produces
	// no pending remainder.
	if safe, pending := splitTrailingEscape([]byte("no escapes here")); string(safe) != "no escapes here" || pending != nil {
		t.Fatalf("splitTrailingEscape(no ESC) = (%q, %q), want (%q, nil)", safe, pending, "no escapes here")
	}
}

// This is the regression test for the Screen()/dump() data race: readLoop
// mutates the shared strings.Builders while a still-running child keeps
// producing output, and c.Screen()/c.dump() must be safe to call
// concurrently with that — in particular on the timeout path, since that's
// exactly when Tasks 7-11 will call dump() against a live bubbletea session
// that hasn't reached the expected screen yet.
//
// It has to run the actual failing case (Expect timing out mid-stream) in a
// SUBPROCESS rather than a subtest. A t.Run subtest's bool return tells the
// parent whether the subtest passed, but Go's testing package still
// unconditionally propagates a failing descendant's status to every
// ancestor test and to the whole binary's exit code — there is no way to
// "expect a failure" from a subtest and have this test, or `go test
// ./test/e2e/...` as a whole, still report success. Re-executing this same
// test binary (filtered to just the helper test below via -test.run) is the
// standard pattern for testing code that is expected to call t.Fatal. When
// the outer `go test` invocation was built with -race, os.Executable() is
// that very race-instrumented binary, so the subprocess is race-checked too.
func TestExpectTimeoutDumpIsRaceFree(t *testing.T) {
	t.Parallel()

	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}

	cmd := exec.Command(exe, "-test.v", "-test.run", "^TestHelperExpectTimesOutWhileWriting$", "-test.timeout=30s")
	cmd.Env = append(os.Environ(), "AKA_E2E_DUMP_RACE_HELPER=1")
	out, runErr := cmd.CombinedOutput()

	if runErr == nil {
		t.Fatalf("expected the helper subprocess to fail (its Expect should time out), but it exited 0:\n%s", out)
	}
	if strings.Contains(string(out), "DATA RACE") {
		t.Fatalf("helper subprocess reported a data race in dump()/Screen() while readLoop was still writing:\n%s", out)
	}
	if !strings.Contains(string(out), "timed out after") || !strings.Contains(string(out), "tick 0") {
		t.Fatalf("helper subprocess output is missing the expected timeout/dump() content "+
			"(the assertion may not have reached the code path it's meant to test):\n%s", out)
	}
}

// TestHelperExpectTimesOutWhileWriting is not a real test: it only runs when
// invoked as the subprocess spawned by TestExpectTimeoutDumpIsRaceFree
// (guarded by AKA_E2E_DUMP_RACE_HELPER), so a normal `go test
// ./test/e2e/...` run always skips it and never reports its expected
// failure as a suite failure. See TestExpectTimeoutDumpIsRaceFree for why
// this indirection exists.
//
// The child process paces its own output (one line every 20ms) specifically
// so it is still mid-stream when the short Expect timeout below fires — an
// unpaced tight printf loop could finish faster than the timeout and leave
// readLoop idle by the time dump() runs, which would defeat the whole point:
// this needs readLoop to be actively appending to the same builders dump()
// is reading, at the exact moment it reads them.
func TestHelperExpectTimesOutWhileWriting(t *testing.T) {
	if os.Getenv("AKA_E2E_DUMP_RACE_HELPER") != "1" {
		t.Skip("only meant to run as a subprocess of TestExpectTimeoutDumpIsRaceFree")
	}

	c := newConsole(t, exec.Command("sh", "-c",
		`i=0; while [ $i -lt 100 ]; do printf 'tick %d\n' "$i"; sleep 0.02; i=$((i+1)); done`))
	c.Expect("tick 0")

	// Ask for something that will never appear, with a short timeout, so this
	// hits c.dump() (and therefore Screen()'s cloning) quickly while the
	// readLoop goroutine is still actively appending to the very builders
	// dump() reads.
	c.expectFunc(`substring "this substring never appears"`, 300*time.Millisecond, func(pending string) int {
		return strings.Index(pending, "this substring never appears")
	})
}
