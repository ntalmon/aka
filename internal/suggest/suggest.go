// Package suggest builds LLM prompts and tool schemas for alias suggestions.
package suggest

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ntalmon/aka/aka-cli/internal/history"
)

// BuildPrompt formats the censored entries as an LLM user message.
// When timestamps are present, each line includes a relative time delta
// from the previous command so the model can identify workflow sequences.
func BuildPrompt(entries []history.Entry) string {
	var sb strings.Builder
	sb.WriteString("Here are my recent shell commands (sensitive values have been replaced with placeholders):\n\n")

	hasTS := false
	for _, e := range entries {
		if e.Timestamp != 0 {
			hasTS = true
			break
		}
	}

	var prevTS int64
	for i, e := range entries {
		if hasTS {
			delta := formatDelta(e.Timestamp, prevTS, i)
			sb.WriteString(fmt.Sprintf("%3d. %-8s %s\n", i+1, delta, e.Command))
			prevTS = e.Timestamp
		} else {
			sb.WriteString(fmt.Sprintf("%3d. %s\n", i+1, e.Command))
		}
	}

	sb.WriteString("\nPlease analyze these commands and suggest useful shell aliases and functions.")
	if hasTS {
		sb.WriteString(" Commands with small time deltas (seconds apart) ran in the same workflow session — pay special attention to these sequences as candidates for combined shell functions.")
	}
	return sb.String()
}

// formatDelta returns a short string like "[start]", "[+4s]", "[+3m]", "[+2h]"
// describing the gap between the current and previous timestamp.
// On the first entry (i==0) or when timestamps are zero, it returns "[start]".
func formatDelta(ts, prevTS int64, i int) string {
	if i == 0 || ts == 0 || prevTS == 0 {
		return "[start]"
	}
	diff := ts - prevTS
	if diff < 0 {
		diff = 0
	}
	switch {
	case diff < 60:
		return fmt.Sprintf("[+%ds]", diff)
	case diff < 3600:
		return fmt.Sprintf("[+%dm]", diff/60)
	case diff < 86400:
		return fmt.Sprintf("[+%dh]", diff/3600)
	default:
		return fmt.Sprintf("[+%dd]", diff/86400)
	}
}

// paramSchema defines the JSON schema for a single parameter.
var paramSchema = map[string]interface{}{
	"type": "object",
	"properties": map[string]interface{}{
		"name": map[string]interface{}{
			"type":        "string",
			"description": "Parameter name, e.g. 'branch', 'path', 'host'",
		},
		"type": map[string]interface{}{
			"type":        "string",
			"description": "Parameter type: string | path | host | token | branch | number",
		},
		"description": map[string]interface{}{
			"type":        "string",
			"description": "Human-readable description of what this parameter represents",
		},
	},
	"required": []string{"name", "type", "description"},
}

// suggestionSchema defines the JSON schema for a single suggestion.
var suggestionSchema = map[string]interface{}{
	"type": "object",
	"properties": map[string]interface{}{
		"name": map[string]interface{}{
			"type":        "string",
			"description": "Short alias or function name, e.g. 'gco', 'drun'",
		},
		"kind": map[string]interface{}{
			"type":        "string",
			"enum":        []string{"alias", "function"},
			"description": "Use 'function' when the command has variable parts ($1, $2, etc.)",
		},
		"template": map[string]interface{}{
			"type":        "string",
			"description": "The command body. For aliases: the full command. For functions: use $1, $2 for parameters. Do NOT include the alias/function definition wrapper.",
		},
		"params": map[string]interface{}{
			"type":        "array",
			"items":       paramSchema,
			"description": "List of parameters, in order. Empty array for aliases.",
		},
		"rationale": map[string]interface{}{
			"type":        "string",
			"description": "Brief explanation of why this alias/function is useful",
		},
		"example_uses": map[string]interface{}{
			"type":        "array",
			"items":       map[string]interface{}{"type": "string"},
			"description": "2-3 example invocations showing how to use the alias/function",
		},
	},
	"required": []string{"name", "kind", "template", "params", "rationale", "example_uses"},
}

// toolInputSchema is the full input schema for the suggest_aliases tool.
var toolInputSchema = map[string]interface{}{
	"type": "object",
	"properties": map[string]interface{}{
		"suggestions": map[string]interface{}{
			"type":        "array",
			"items":       suggestionSchema,
			"description": "List of suggested aliases and functions, ordered by descending impact",
		},
	},
	"required": []string{"suggestions"},
}

// ToolSchema returns the JSON-encoded input schema for the suggest_aliases tool.
func ToolSchema() (json.RawMessage, error) {
	data, err := json.Marshal(toolInputSchema)
	if err != nil {
		return nil, fmt.Errorf("marshal tool schema: %w", err)
	}
	return json.RawMessage(data), nil
}
