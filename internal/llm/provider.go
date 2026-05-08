// Package llm defines the Provider interface and Suggestion types.
package llm

import (
	"context"

	"github.com/ntalmon/aka/aka-cli/internal/aliases"
	"github.com/ntalmon/aka/aka-cli/internal/history"
)

// Suggestion is a single alias or shell function suggested by the LLM.
type Suggestion struct {
	Name        string          `json:"name"`
	Kind        string          `json:"kind"`     // "alias" | "function"
	Template    string          `json:"template"` // e.g. `git checkout "$1"`
	Params      []aliases.Param `json:"params"`
	Rationale   string          `json:"rationale"`
	ExampleUses []string        `json:"example_uses"`
}

// Provider can suggest aliases/functions from a list of censored history entries.
type Provider interface {
	Suggest(ctx context.Context, censored []history.Entry) ([]Suggestion, error)
}
