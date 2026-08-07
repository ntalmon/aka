//go:build e2e

package e2e

import (
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/creack/pty"
)

// Keystrokes understood by the bubbletea/huh prompts under test.
const (
	Enter = "\r"
	Up    = "\x1b[A"
	Down  = "\x1b[B"
	Right = "\x1b[C"
	Left  = "\x1b[D"
	Space = " "
	Esc   = "\x1b"
	CtrlC = "\x03"
)

// defaultExpectTimeout bounds every Expect. There are no sleeps anywhere in the
// suite: synchronisation is always "wait for the prompt, then send".
const defaultExpectTimeout = 10 * time.Second

// ansiRE matches CSI sequences (colour, cursor movement, screen clears) and OSC
// sequences, which is everything lipgloss and bubbletea emit.
var ansiRE = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]|\x1b\][^\a]*(\a|\x1b\\)|\x1b[()][B0]`)

// Console drives a process attached to a real pseudo-terminal.
//
// Expect has consuming semantics: each call searches only the output produced
// after the previous match. bubbletea repaints the whole screen on every
// keystroke, so a non-consuming Expect would happily match a frame from several
// screens ago and pass for the wrong reason.
type Console struct {
	t    *testing.T
	cmd  *exec.Cmd
	ptmx *os.File

	mu       sync.Mutex
	raw      strings.Builder // everything received, escapes intact
	stripped strings.Builder // same, ANSI removed — what assertions match against
	offset   int             // consume point into stripped

	readDone chan struct{}
	waitOnce sync.Once
	exitCode int
}

// newConsole starts cmd attached to a PTY and streams its output into the
// console buffers. The process is killed at test cleanup if still running.
func newConsole(t *testing.T, cmd *exec.Cmd) *Console {
	t.Helper()

	ptmx, err := pty.Start(cmd)
	if err != nil {
		t.Fatalf("start %v under pty: %v", cmd.Args, err)
	}

	c := &Console{
		t:        t,
		cmd:      cmd,
		ptmx:     ptmx,
		readDone: make(chan struct{}),
		exitCode: -1,
	}

	go c.readLoop()

	t.Cleanup(func() {
		_ = ptmx.Close()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	})

	return c
}

func (c *Console) readLoop() {
	defer close(c.readDone)
	buf := make([]byte, 4096)
	// pending holds bytes carried over from the previous chunk that might be
	// the start of a CSI/OSC sequence Read() cut off mid-way. It is owned
	// exclusively by this goroutine, so it needs no locking of its own.
	var pending []byte
	for {
		n, err := c.ptmx.Read(buf)
		if n > 0 {
			chunk := buf[:n]
			c.mu.Lock()
			c.raw.Write(chunk)
			data := append(pending, chunk...)
			safe, rest := splitTrailingEscape(data)
			c.stripped.WriteString(stripANSI(string(safe)))
			c.mu.Unlock()
			pending = rest
		}
		if err != nil {
			if len(pending) > 0 {
				// Stream ended mid-sequence (or with a stray ESC that never
				// resolved). Flush best-effort rather than dropping it.
				c.mu.Lock()
				c.stripped.WriteString(stripANSI(string(pending)))
				c.mu.Unlock()
			}
			return // EIO on close is normal for a PTY
		}
	}
}

// splitTrailingEscape separates data into a safe-to-strip prefix and a
// possibly-incomplete escape sequence at the very end. readLoop feeds output
// through stripANSI in whatever chunks Read() happens to return them, and a
// bubbletea/huh full-screen redraw can exceed one 4096-byte chunk, so a
// CSI/OSC sequence can be split across two reads. Stripping each chunk in
// isolation would let the truncated half leak into the stripped buffer as
// literal bytes, corrupting the exact buffer Expect/Screen assert against.
//
// data's last ESC (0x1b) byte is used as the candidate cut point: if what
// follows it is already a complete, recognised escape sequence, everything
// is safe to strip (anything after a complete match is guaranteed plain text,
// since we picked the *last* ESC in data). Otherwise the sequence is still
// accumulating bytes, so everything from that ESC onward is held back for the
// next call.
func splitTrailingEscape(data []byte) (safe, rest []byte) {
	i := bytes.LastIndexByte(data, 0x1b)
	if i < 0 {
		return data, nil
	}
	tail := data[i:]
	if loc := ansiRE.FindIndex(tail); loc != nil && loc[0] == 0 {
		return data, nil
	}
	return data[:i], tail
}

// stripANSI removes escape sequences and normalises PTY line endings so
// assertions can match plain text.
func stripANSI(s string) string {
	s = ansiRE.ReplaceAllString(s, "")
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.ReplaceAll(s, "\r", "")
}

// Expect waits for substr to appear after the previous match, then consumes
// through it. Fatals with a screen dump on timeout.
func (c *Console) Expect(substr string) {
	c.t.Helper()
	c.expectFunc("substring "+strconv.Quote(substr), defaultExpectTimeout, func(pending string) int {
		if i := strings.Index(pending, substr); i >= 0 {
			return i + len(substr)
		}
		return -1
	})
}

// ExpectRe is Expect with a regular expression.
func (c *Console) ExpectRe(pattern string) {
	c.t.Helper()
	re, err := regexp.Compile(pattern)
	if err != nil {
		c.t.Fatalf("bad ExpectRe pattern %q: %v", pattern, err)
	}
	c.expectFunc("pattern "+strconv.Quote(pattern), defaultExpectTimeout, func(pending string) int {
		if loc := re.FindStringIndex(pending); loc != nil {
			return loc[1]
		}
		return -1
	})
}

// expectFunc polls the pending (unconsumed) output until match returns a
// non-negative end offset, then advances the consume point. timeout is a
// parameter (rather than always defaultExpectTimeout) so tests can exercise
// the timeout/dump() path deterministically and fast, instead of waiting out
// the real 10s production timeout.
func (c *Console) expectFunc(what string, timeout time.Duration, match func(pending string) int) {
	c.t.Helper()
	deadline := time.Now().Add(timeout)

	for {
		c.mu.Lock()
		pending := c.stripped.String()[c.offset:]
		if end := match(pending); end >= 0 {
			c.offset += end
			c.mu.Unlock()
			return
		}
		c.mu.Unlock()

		if time.Now().After(deadline) {
			c.t.Fatalf("timed out after %s waiting for %s\n\n%s",
				timeout, what, c.dump())
		}
		select {
		case <-c.readDone:
			// Process ended; one last check before giving up.
			c.mu.Lock()
			pending := c.stripped.String()[c.offset:]
			end := match(pending)
			if end >= 0 {
				c.offset += end
			}
			c.mu.Unlock()
			if end >= 0 {
				return
			}
			c.t.Fatalf("process exited before %s appeared\n\n%s", what, c.dump())
		case <-time.After(20 * time.Millisecond):
		}
	}
}

// NotExpect asserts substr never appeared in the session.
func (c *Console) NotExpect(substr string) {
	c.t.Helper()
	if strings.Contains(c.Screen(), substr) {
		c.t.Fatalf("did not expect %q in output\n\n%s", substr, c.dump())
	}
}

// CountOccurrences reports how many times substr appears in the whole session.
// Used to assert a menu was not rendered twice.
func (c *Console) CountOccurrences(substr string) int {
	return strings.Count(c.Screen(), substr)
}

// Send writes keystrokes to the terminal. Always Expect the prompt you are
// answering first — never send blind.
func (c *Console) Send(keys ...string) {
	c.t.Helper()
	for _, k := range keys {
		if _, err := io.WriteString(c.ptmx, k); err != nil {
			c.t.Fatalf("write %q to pty: %v\n\n%s", k, err, c.dump())
		}
	}
}

// SendLine sends s followed by Enter.
func (c *Console) SendLine(s string) {
	c.t.Helper()
	c.Send(s, Enter)
}

// Screen returns everything received so far, ANSI-stripped.
//
// strings.Builder.String() does not copy: it aliases the builder's internal
// backing array via an unsafe cast. readLoop's WriteString can later append
// into that same array in place (reusing spare capacity) and mutate bytes a
// caller here is still reading, once the lock is released — a real,
// race-detector-visible data race. strings.Clone forces an actual copy while
// the lock is still held, so the returned string is fully independent.
func (c *Console) Screen() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return strings.Clone(c.stripped.String())
}

// Wait blocks until the process exits, reaps it, and returns its exit code.
// It does not proactively close the PTY itself: it waits for c.readDone,
// which readLoop closes once it observes EOF/EIO — the natural consequence
// of the child exiting and the kernel tearing down the slave side. Closing
// the master here before that happens would risk delivering a still-running
// child a stray SIGHUP and returning a misleading exit code for a process
// that never actually finished — exactly the shape of bug Tasks 7-11 would
// hit against real bubbletea sessions that block on stdin until told to
// quit. The PTY is instead closed unconditionally at test cleanup (see
// newConsole).
func (c *Console) Wait() int {
	c.t.Helper()
	c.waitOnce.Do(func() {
		<-c.readDone
		err := c.cmd.Wait()
		var exitErr *exec.ExitError
		switch {
		case err == nil:
			c.exitCode = 0
		case errors.As(err, &exitErr):
			c.exitCode = exitErr.ExitCode()
		default:
			c.t.Fatalf("wait for %v: %v\n\n%s", c.cmd.Args, err, c.dump())
		}
	})
	return c.exitCode
}

// dump renders the tail of the session for failure messages. A PTY test that
// fails with only "timed out waiting for X" is unmaintainable.
//
// Both builders are cloned before the lock is released — see Screen for why
// a bare .String() here would be a data race against the concurrent
// readLoop goroutine.
func (c *Console) dump() string {
	c.mu.Lock()
	stripped := strings.Clone(c.stripped.String())
	raw := strings.Clone(c.raw.String())
	c.mu.Unlock()

	var sb strings.Builder
	sb.WriteString("--- screen (ANSI stripped, last 40 lines) ---\n")
	sb.WriteString(tailLines(stripped, 40))
	sb.WriteString("\n--- raw (last 2000 bytes) ---\n")
	sb.WriteString(tailBytes(raw, 2000))
	sb.WriteString("\n--- end ---")
	return sb.String()
}

func tailLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

func tailBytes(s string, n int) string {
	if len(s) > n {
		return s[len(s)-n:]
	}
	return s
}
