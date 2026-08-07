//go:build e2e

package e2e

import (
	"regexp"
	"strings"
	"testing"

	"github.com/ntalmon/aka/test/e2e/fakellm"
)

// secretLiterals are values that must never reach the LLM. Each is paired with
// the history command that contains it. All four are caught today by
// internal/censor/censor.go's Pass 1 regex pack or entropy heuristic — this
// set is deliberately strict and must keep passing.
//
// Two values that were originally asserted here were removed after product
// review (see task-9-report.md addendum for the full ruling):
//
//   - A bare IP address (10.1.2.3, from `ssh deploy@10.1.2.3`) is not a
//     secret-detection case at all. ParameterizeVars skips IPs
//     unconditionally, before any clustering/repetition logic ever runs:
//     `if peekTyp == "PATH" || peekTyp == "PORT" || peekTyp == "IP" {
//     continue }` (internal/censor/censor.go, in the per-slot loop that
//     decides what to parameterize). This is not "IPs only get masked when
//     they repeat" — IPs are never parameterized at all, full stop, no
//     matter how many distinct or repeated occurrences appear in the batch.
//     The repo's own internal/censor/clustering_test.go
//     (TestParameterizeVarsIPVaryingNotCensored) proves this directly with
//     three distinct ssh IPs that all stay unmasked. It was never a
//     secret-redaction rule, so an unmasked IP — one-off or repeated — is
//     working as designed, not a gap. There is intentionally no assertion
//     for it anywhere in this file.
//   - "hunter2supersecret" (mysql's `-phunter2supersecret` flag, from
//     `mysql -u admin -phunter2supersecret -h db.internal`) is a real censor
//     gap the product owner has chosen not to fix yet. It is pinned on its
//     own in TestKnownCensorGapPasswordFlagWithoutEquals below instead of
//     living here, so this test documents only currently-guaranteed
//     behaviour and stays green.
var secretLiterals = map[string]string{
	"AKIAIOSFODNN7EXAMPLE":                   `aws configure set aws_access_key_id AKIAIOSFODNN7EXAMPLE`,
	"ghp_1234567890abcdefghijklmnopqrstuvwx": `git remote set-url origin https://ghp_1234567890abcdefghijklmnopqrstuvwx@github.com/o/r.git`,
	"eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U": `curl -H "Authorization: Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U" https://api.example.com/v1/data`,
	"sk-ant-api03-SECRETVALUE1234567890abcdefXYZ":                                                                  "export ANTHROPIC_API_KEY=sk-ant-api03-SECRETVALUE1234567890abcdefXYZ",
}

// mysqlPasswordFlagCmd is the known-uncensored command used by
// TestKnownCensorGapPasswordFlagWithoutEquals. It lives in secretHistory's
// fixed prefix (like the ssh/curl/git commands) rather than in
// secretLiterals, since it must appear in history without being covered by
// TestSecretsNeverReachTheWire's strict loop.
const mysqlPasswordFlagCmd = `mysql -u admin -phunter2supersecret -h db.internal`

// commitHashLiteral is a real 40-hex-char SHA-1 (sha1sum of a fixed string,
// not derived from any actual repo object) used by
// TestCommitMessagesSurviveCensoring to assert that commit hashes — not just
// commit messages — survive both censor passes, per CLAUDE.md's documented
// "git commit hashes and commit messages are intentionally not censored"
// invariant.
const commitHashLiteral = "331ea0017b8e3d4d49d61902238be50bf71b3067"

// secretHistory builds a history containing every secret plus enough ordinary
// commands that the scan proceeds normally (full history is sent in one shot
// regardless of size, but a small history reads as unrealistic and risks
// tripping other size-dependent behaviour, so pad it out like the other
// suites do).
func secretHistory() []string {
	cmds := []string{
		"ssh deploy@10.1.2.3",
		"curl https://admin:s3cr3tpassw0rd@internal.example.com/health",
		"git commit -m 'fix the login redirect'",
		"git log --oneline -5",
		"git show " + commitHashLiteral,
		mysqlPasswordFlagCmd,
	}
	for _, cmd := range secretLiterals {
		cmds = append(cmds, cmd)
	}
	for i := 0; i < 40; i++ {
		cmds = append(cmds, "git status", "git diff --stat")
	}
	return cmds
}

func censorEnv(t *testing.T) *Env {
	t.Helper()
	env := seededEnv(t, "zsh")
	if _, code := env.Run("init", "--shell", "zsh"); code != 0 {
		t.Fatal("init failed")
	}
	env.SeedHistory(secretHistory()...)
	env.LLM.Respond(fakellm.Normal(workflowSuggestions()...))
	return env
}

// The strongest form of the censor test: assert on the bytes that actually
// crossed the socket, not on a function's return value.
//
// This asserts the strict set only: the AWS key, GitHub token, JWT,
// Anthropic key (all regex/entropy catches in Pass 1), and the URL-embedded
// password from the https://admin:s3cr3tpassw0rd@... credential. All five
// are caught deterministically today and must keep being caught — do not
// soften any of these checks. See the doc comment on secretLiterals above
// for why a bare IP address is deliberately not asserted here, and
// TestKnownCensorGapPasswordFlagWithoutEquals below for the one known gap
// that is pinned separately rather than failing this test.
func TestSecretsNeverReachTheWire(t *testing.T) {
	t.Parallel()

	env := censorEnv(t)

	c := env.Spawn("scan", "--history", "full", "--censor", "trust")
	c.Expect("AKA FOUND")
	c.Send(CtrlC)
	_ = c.Wait()

	reqs := env.LLM.Requests()
	if len(reqs) == 0 {
		t.Fatal("no request reached the server")
	}
	body := string(reqs[0].Body)

	for secret := range secretLiterals {
		if strings.Contains(body, secret) {
			t.Errorf("secret %q was sent to the LLM", secret)
		}
	}
	if strings.Contains(body, "s3cr3tpassw0rd") {
		t.Error(`sensitive value "s3cr3tpassw0rd" was sent to the LLM`)
	}
}

// TestKnownCensorGapPasswordFlagWithoutEquals pins a real, currently
// unfixed censor gap: mysql's `-p<password>` flag syntax (no space, no `=`)
// is not caught by either censor pass, so the raw password reaches the LLM
// under `--censor trust`.
//
//   - Pass 1's password regex is `(?i)(password|passwd|pass|pwd)=\S+`
//     (internal/censor/censor.go) — it requires a literal `=` between the
//     keyword and the value. mysql's `-phunter2supersecret` has no
//     separator at all, so the regex never matches.
//   - The entropy heuristic does NOT reject this on length, and lowering
//     the length floor would NOT close this gap — that first guess is
//     wrong, so it's worth spelling out precisely what actually happens.
//     CensorSecrets tokenizes each command by splitting on whitespace
//     (`strings.FieldsFunc(cmd, unicode.IsSpace)`) before running the
//     entropy check, so the token isLikelySecret actually evaluates is not
//     the bare password "hunter2supersecret" (18 chars) — it's the whole
//     whitespace-delimited flag, "-phunter2supersecret" (20 chars, `-p`
//     glued directly onto the value with no separator to split on). That
//     clears isLikelySecret's `len(s) >= 20` floor and its 2-of-3
//     char-class check, so it DOES reach shannonEntropy — and fails there:
//     "-phunter2supersecret" scores ~3.284 bits/char (verified by running
//     the real shannonEntropy against it), well under the 4.5 bits/char
//     threshold, because it reads as ordinary pronounceable words
//     ("hunter", "super", "secret") rather than random-looking data.
//
// So the value passes both passes untouched and is sent to the LLM in the
// clear — not because it's too short, but because it's linguistically
// low-entropy once you look at the token the code actually sees. A real fix
// needs either a pattern for glued `-p<value>`-style flags (mysql, curl
// `-u user:pass`, etc.) or a lower entropy threshold — and a lower threshold
// would trade this false negative for false positives on ordinary
// multi-word text, so it's not a free win. This is a real finding, reported
// to and reviewed by the product owner, who has chosen NOT to fix it for
// now — this test documents that decision, it does not endorse the
// behaviour. It intentionally asserts the leak (rather than the absence of
// one) so that if someone later closes this gap in
// internal/censor/censor.go, this test starts failing loudly instead of
// silently going stale. When that happens, deleting this test (and folding
// the mysql command into TestSecretsNeverReachTheWire's strict set instead)
// is the correct response — do not "fix" this test to keep passing against
// improved censoring.
func TestKnownCensorGapPasswordFlagWithoutEquals(t *testing.T) {
	t.Parallel()

	env := censorEnv(t)

	c := env.Spawn("scan", "--history", "full", "--censor", "trust")
	c.Expect("AKA FOUND")
	c.Send(CtrlC)
	_ = c.Wait()

	reqs := env.LLM.Requests()
	if len(reqs) == 0 {
		t.Fatal("no request reached the server")
	}
	body := string(reqs[0].Body)

	if !strings.Contains(body, "hunter2supersecret") {
		t.Error("known gap appears to be fixed: \"hunter2supersecret\" no longer reached the LLM. " +
			"If internal/censor/censor.go now catches mysql's -p<password> flag syntax, delete this test " +
			"and add mysqlPasswordFlagCmd's secret to TestSecretsNeverReachTheWire's strict set instead.")
	}
}

// placeholderRE matches the real placeholder format emitted by
// internal/censor/censor.go: "<LABEL_n>" where LABEL is one of the fixed
// tokens CensorSecrets/ParameterizeVars produce (TOKEN, PASSWORD, URL_CREDS,
// SECRET from pass 1; HOST, VAR from pass 2). PATH and IP are included in the
// alternation below for completeness with inferVarType's full label set, but
// neither is ever actually emitted as a placeholder: ParameterizeVars's
// per-slot loop skips `peekTyp == "PATH" || peekTyp == "PORT" ||
// peekTyp == "IP"` unconditionally, before a placeholder is ever assigned —
// this is not conditional on repetition/clustering, IPs (and paths, and
// ports) are excluded outright regardless of how many distinct or repeated
// occurrences appear in a batch. See secretLiterals's doc comment above for
// the same point, with the corroborating unit test reference. The brief's
// original assertion only checked for the presence of "<" and ">" anywhere
// in the body, which would also match template syntax or incidental angle
// brackets in an unrelated JSON field; this tightens it to the actual
// placeholder shape.
//
// One more layer matters for "the actual bytes that crossed the socket":
// internal/llm/anthropic.go and openaicompat.go both build the request body
// with plain encoding/json.Marshal, which HTML-escapes the angle-bracket
// runes by default (json.Encoder.SetEscapeHTML(false) is never called), so
// each bracket becomes a six-byte backslash-u-0-0-3-c / backslash-u-0-0-3-e
// unicode escape in the marshaled bytes. Confirmed by writing
// env.LLM.Requests()[0].Body to disk and grepping the raw file: the wire
// never contains a literal angle-bracket byte, only that escaped form. A
// regex anchored on literal '<'/'>' bytes therefore never matches the real
// wire bytes, even though it matches what a human (or Python's json.load,
// which unescapes on parse) would read back after decoding. This regex
// matches the literal escaped form instead, since that is what genuinely
// crossed the socket.
var placeholderRE = regexp.MustCompile(`\\u003c(TOKEN|PASSWORD|URL_CREDS|SECRET|HOST|IP|VAR)_\d+\\u003e`)

func TestPlaceholdersAppearOnTheWire(t *testing.T) {
	t.Parallel()

	env := censorEnv(t)

	c := env.Spawn("scan", "--history", "full", "--censor", "trust")
	c.Expect("AKA FOUND")
	c.Send(CtrlC)
	_ = c.Wait()

	body := string(env.LLM.Requests()[0].Body)
	if !placeholderRE.MatchString(body) {
		t.Fatalf("no censor placeholders (e.g. <TOKEN_1>, <IP_1>) found in the request body:\n%s", truncate(body, 4000))
	}
}

// Commit messages and hashes are deliberately not censored — the LLM needs them
// to recognise workflow patterns. secretHistory includes both a commit
// message (`git commit -m 'fix the login redirect'`) and a real 40-hex-char
// SHA (`git show `+commitHashLiteral), so this test asserts both survive
// rather than just the message half.
func TestCommitMessagesSurviveCensoring(t *testing.T) {
	t.Parallel()

	env := censorEnv(t)

	c := env.Spawn("scan", "--history", "full", "--censor", "trust")
	c.Expect("AKA FOUND")
	c.Send(CtrlC)
	_ = c.Wait()

	body := string(env.LLM.Requests()[0].Body)
	if !strings.Contains(body, "fix the login redirect") {
		t.Errorf("commit message was censored; it should survive:\n%s", truncate(body, 4000))
	}
	if !strings.Contains(body, commitHashLiteral) {
		t.Errorf("commit hash %q was censored; it should survive:\n%s", commitHashLiteral, truncate(body, 4000))
	}
}

// --censor none is documented as sending raw commands. Pin that contract so a
// future change cannot silently alter what the flag means.
func TestCensorNoneSendsRawSecrets(t *testing.T) {
	t.Parallel()

	env := censorEnv(t)

	c := env.Spawn("scan", "--history", "full", "--censor", "none")
	c.Expect("AKA FOUND")
	c.Send(CtrlC)
	_ = c.Wait()

	body := string(env.LLM.Requests()[0].Body)
	if !strings.Contains(body, "AKIAIOSFODNN7EXAMPLE") {
		t.Error("--censor none did not send the raw value; the flag's contract changed")
	}
}

func TestCensorNoneWarnsTheUser(t *testing.T) {
	t.Parallel()

	env := censorEnv(t)

	c := env.Spawn("scan", "--history", "full", "--censor", "none")
	c.Expect("No censoring")
	c.Expect("AKA FOUND")
	c.Send(CtrlC)
	_ = c.Wait()
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "\n… (truncated)"
}
