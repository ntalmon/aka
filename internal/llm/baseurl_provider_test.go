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
