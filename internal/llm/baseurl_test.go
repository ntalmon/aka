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
				t.Fatalf("got %q, want %q", got, anthropicDefault)
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
