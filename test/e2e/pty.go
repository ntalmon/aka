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
	// chunker is owned exclusively by this goroutine (readLoop is the only
	// caller of feed/flush), so it needs no locking of its own — only the
	// shared c.raw/c.stripped builders it writes into do.
	var chunker ansiChunker
	for {
		n, err := c.ptmx.Read(buf)
		if n > 0 {
			chunk := buf[:n]
			c.mu.Lock()
			c.raw.Write(chunk)
			c.stripped.WriteString(chunker.feed(chunk))
			c.mu.Unlock()
		}
		if err != nil {
			if s := chunker.flush(); s != "" {
				// Stream ended mid-sequence (or with a stray ESC that never
				// resolved). Flush best-effort rather than dropping it.
				c.mu.Lock()
				c.stripped.WriteString(s)
				c.mu.Unlock()
			}
			return // EIO on close is normal for a PTY
		}
	}
}

// maxPendingEscape bounds how many bytes ansiChunker.pending is allowed to
// hold back as a possibly-incomplete escape sequence before giving up and
// flushing it as literal text.
//
// Real CSI sequences (cursor movement, SGR colour codes) run well under 32
// bytes even with several numeric parameters. Real OSC sequences for a
// window title or a hyperlink (OSC 8) are usually well under a few hundred
// bytes. The one realistic outlier is OSC 52 (clipboard set/query): its
// payload is base64-encoded, and github.com/aymanbagabas/go-osc52 is already
// an indirect dependency of this module (pulled in for bubbletea/huh
// copy-to-clipboard support), so a live session under test could plausibly
// emit one for a non-trivial chunk of copied text — comfortably more than a
// few hundred bytes, but not unbounded. 8KiB gives that real case generous
// headroom while still bounding a stream of ESC bytes that never resolves
// into a recognised sequence at all (malformed or adversarial output) from
// growing pending without limit.
const maxPendingEscape = 8192

// ansiChunker incrementally strips ANSI escape sequences from a stream of
// arbitrarily-sized chunks, holding back a possibly-incomplete escape
// sequence at the end of each chunk so a Read() boundary that lands mid-
// sequence can't corrupt the stripped output. It is not safe for concurrent
// use. readLoop owns one exclusively; tests construct their own zero-value
// ansiChunker to exercise this exact chunking logic — the same code readLoop
// runs against a live PTY — without needing one.
type ansiChunker struct {
	pending []byte
}

// feed strips as much of chunk (plus any bytes carried over from previous
// feed calls) as is currently safe, and returns it. See splitTrailingEscape
// for what "safe" means and how malformed input is handled.
func (a *ansiChunker) feed(chunk []byte) string {
	data := append(a.pending, chunk...)
	safe, rest := splitTrailingEscape(data)
	if len(rest) > maxPendingEscape {
		// Never resolved into a recognised sequence within a generous
		// bound — stop waiting for a terminator that may never come and
		// flush the whole thing as literal text instead of holding it
		// (and everything appended after it) forever.
		safe, rest = data, nil
	}
	a.pending = rest
	return stripANSI(string(safe))
}

// flush strips and returns whatever is left in pending. Call this once the
// stream has ended and no more bytes are coming, so a still-incomplete
// sequence is rendered as literal text rather than silently dropped.
func (a *ansiChunker) flush() string {
	if len(a.pending) == 0 {
		return ""
	}
	s := stripANSI(string(a.pending))
	a.pending = nil
	return s
}

// splitTrailingEscape separates data into a safe-to-strip prefix and a
// possibly-incomplete escape sequence held back at the end. readLoop feeds
// output through stripANSI in whatever chunks Read() happens to return them,
// and a bubbletea/huh full-screen redraw can exceed one 4096-byte chunk, so
// a CSI/OSC sequence can be split across two reads. Stripping each chunk in
// isolation would let the truncated half leak into the stripped buffer as
// literal bytes, corrupting the exact buffer Expect/Screen assert against.
//
// The real invariant: only trust what ansiRE has *already fully matched*.
// Never assume anything about what a bare ESC byte means before checking —
// in particular, never assume every ESC *starts* a sequence. That
// assumption was this function's original design and it was wrong: an OSC
// sequence can terminate two ways, BEL (\a) or ST (ESC \), and an ST
// terminator's ESC *closes* a sequence, it doesn't open one. Pivoting on
// "the last ESC byte in data" treated an already-complete OSC sequence's own
// closing ST as "the start of a new, incomplete sequence" and held back (and
// once concatenated with the next chunk, leaked as raw literal bytes) an OSC
// opener that was, in fact, already fully resolved. See git history for the
// regression this produced and TestAnsiChunkerSplitInsideSTTerminatedOSC /
// TestNoSplitMatchesUnsplitStripANSI for the pinned fix.
//
// The correct approach:
//  1. Find every complete match in data, left to right (ansiRE.FindAllIndex)
//     — the same matches ReplaceAllString would find, in the same order, so
//     this function and stripANSI can never disagree about what "complete"
//     means.
//  2. Let end be the byte offset right after the last complete match (0 if
//     there were none). Everything in data[:end] is either plain text or one
//     of those already-resolved matches: safe, unconditionally.
//  3. Search data[end:] — the region past every known-complete match — for
//     the first ESC byte. By construction nothing before it can be part of
//     any sequence, complete or incomplete: an escape sequence must start
//     with an ESC, and this is the first one after end.
//  4. Everything before that ESC (or the whole of data[end:], if it contains
//     no ESC at all) is safe too. From that ESC onward might be a sequence
//     still accumulating bytes, so it's held back for the next call.
//
// This is exact for well-formed input, and intentionally terminal-accurate
// (not "safe") for malformed input: if an incomplete sequence in data[end:]
// is immediately followed by bytes that happen to complete a *different*
// valid sequence once concatenated with a later chunk (e.g. holding back
// "\x1b[3" and then receiving "randomtext" — "\x1b[3r" is a syntactically
// valid CSI sequence, since 'r' falls in the final-byte range [@-~], and
// gets stripped along with its leading digit, exactly as
// "\x1b[3rANDOMTEXT" would strip if it arrived as one write) — the result
// matches what a real terminal emulator would render for that same
// malformed byte stream. That is the desired behaviour for a harness
// asserting on terminal output: it is not this function's job to
// second-guess or repair a program that writes syntactically ambiguous
// escape sequences, only to strip exactly what a terminal would. See
// TestSplitTrailingEscapeMalformedInputMatchesTerminal.
func splitTrailingEscape(data []byte) (safe, rest []byte) {
	end := 0
	if matches := ansiRE.FindAllIndex(data, -1); len(matches) > 0 {
		end = matches[len(matches)-1][1]
	}
	tail := data[end:]
	if i := bytes.IndexByte(tail, 0x1b); i >= 0 {
		return data[:end+i], data[end+i:]
	}
	return data, nil
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
