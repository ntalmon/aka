//go:build e2e

package e2e

import (
	"regexp"
	"strings"
	"testing"

	"github.com/ntalmon/aka/test/e2e/fakellm"
)

// secretLiterals are values that must never reach the LLM. Each is paired with
// the history command that contains it.
var secretLiterals = map[string]string{
	"AKIAIOSFODNN7EXAMPLE":                   `aws configure set aws_access_key_id AKIAIOSFODNN7EXAMPLE`,
	"ghp_1234567890abcdefghijklmnopqrstuvwx": `git remote set-url origin https://ghp_1234567890abcdefghijklmnopqrstuvwx@github.com/o/r.git`,
	"eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U": `curl -H "Authorization: Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U" https://api.example.com/v1/data`,
	"sk-ant-api03-SECRETVALUE1234567890abcdefXYZ":                                                                  "export ANTHROPIC_API_KEY=sk-ant-api03-SECRETVALUE1234567890abcdefXYZ",
	"hunter2supersecret": `mysql -u admin -phunter2supersecret -h db.internal`,
}

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
// REAL SECURITY FINDING — this test is intentionally left failing. Two of the
// values it asserts against genuinely reach the wire under `--censor trust`,
// confirmed by writing the raw recorded request body to disk and inspecting
// it (see task-9-report.md for the full byte-for-byte dump):
//
//  1. "10.1.2.3" from `ssh deploy@10.1.2.3`. internal/censor/censor.go only
//     masks IP addresses in Pass 2 (ParameterizeVars), and Pass 2 only
//     parameterizes a positional slot when the same (binary+flags) shape
//     appears with ≥2 *distinct* values in the batch (see the `len(vals) < 2`
//     guard in ParameterizeVars). secretHistory's history has exactly one
//     `ssh ...` command, so the slot never clusters and the IP is never
//     replaced — it is sent to the LLM completely unmasked. Any one-off IP
//     address (or hostname, or any other Pass-2-typed value) with no repeat
//     in the same session's history bypasses masking entirely.
//  2. "hunter2supersecret" from `mysql -u admin -phunter2supersecret -h
//     db.internal`. This is a real, if awkward, secret-bearing shell idiom:
//     mysql's `-p<password>` (no space, no `=`) concatenated flag. Pass 1's
//     regex pack has no pattern for it (the `(password|passwd|pass|pwd)=`
//     pattern requires a literal `=`), and it doesn't qualify for the
//     entropy heuristic either: isLikelySecret requires len(s) >= 20, and
//     "hunter2supersecret" is 18 characters — short enough to slip under
//     that floor despite being an obviously real secret.
//
// Per this task's explicit instructions: do not weaken this assertion to
// make it pass, and do not "fix" internal/censor/censor.go to match the
// test (that decision belongs to a human, and is a product change, not a
// test change). This is reported loudly in the task report instead.
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
	for _, secret := range []string{"s3cr3tpassw0rd", "10.1.2.3"} {
		if strings.Contains(body, secret) {
			t.Errorf("sensitive value %q was sent to the LLM", secret)
		}
	}
}

// placeholderRE matches the real placeholder format emitted by
// internal/censor/censor.go: "<LABEL_n>" where LABEL is one of the fixed
// tokens CensorSecrets/ParameterizeVars produce (TOKEN, PASSWORD, URL_CREDS,
// SECRET from pass 1; PATH, HOST, IP, VAR from pass 2 — though PATH is never
// actually emitted as a placeholder since inferVarType's PATH/PORT case is
// skipped before a placeholder is assigned). The brief's original assertion
// only checked for the presence of "<" and ">" anywhere in the body, which
// would also match template syntax or incidental angle brackets in an
// unrelated JSON field; this tightens it to the actual placeholder shape.
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
// to recognise workflow patterns.
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
