//go:build e2e

package fakellm

import (
	"context"
	"errors"
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
