package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/ntalmon/aka/aka-cli/internal/history"
	"github.com/ntalmon/aka/aka-cli/internal/suggest"
)

const (
	defaultModel     = "claude-haiku-4-5-20251001"
	anthropicAPI     = "https://api.anthropic.com/v1/messages"
	anthropicVersion = "2023-06-01"
)

// AnthropicProvider implements Provider using the Anthropic Messages API.
type AnthropicProvider struct {
	apiKey string
	model  string
	client *http.Client
}

// New creates an AnthropicProvider with the given API key.
func New(apiKey string) *AnthropicProvider {
	return &AnthropicProvider{
		apiKey: apiKey,
		model:  defaultModel,
		client: &http.Client{Timeout: 120 * time.Second},
	}
}

// WithModel overrides the default model.
func (a *AnthropicProvider) WithModel(model string) *AnthropicProvider {
	a.model = model
	return a
}

// anthropicRequest is the payload sent to the Anthropic API.
type anthropicRequest struct {
	Model      string          `json:"model"`
	MaxTokens  int             `json:"max_tokens"`
	System     []systemBlock   `json:"system"`
	Tools      []anthropicTool `json:"tools"`
	ToolChoice toolChoice      `json:"tool_choice"`
	Messages   []message       `json:"messages"`
}

type systemBlock struct {
	Type         string        `json:"type"`
	Text         string        `json:"text"`
	CacheControl *cacheControl `json:"cache_control,omitempty"`
}

type cacheControl struct {
	Type string `json:"type"`
}

type anthropicTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type toolChoice struct {
	Type string `json:"type"`
	Name string `json:"name"`
}

type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// anthropicResponse holds the API response.
type anthropicResponse struct {
	Content []contentBlock `json:"content"`
	Error   *apiError      `json:"error,omitempty"`
}

type contentBlock struct {
	Type  string          `json:"type"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
	Text  string          `json:"text,omitempty"`
}

type apiError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

// toolOutput matches the suggest_aliases tool output schema.
type toolOutput struct {
	Suggestions []Suggestion `json:"suggestions"`
}

// systemPrompt is the cached system prompt.
const systemPromptText = `You are an expert shell alias and function suggester.
You analyze shell command history and suggest useful aliases and shell functions.

Rules:
1. Prefer shell FUNCTIONS over aliases whenever a command has variable parts (placeholders like <VAR_n>, <PATH_n>, <BRANCH_n>).
2. For functions, use $1, $2, etc. for parameters — never hardcode placeholder values.
3. Suggest short, memorable names following common conventions (e.g., "gco" for "git checkout").
4. Only suggest aliases/functions that would genuinely save keystrokes and are used repeatedly.
5. Provide a concise rationale and example usages.
6. The template for an alias should be the full command body (without the alias definition syntax).
7. The template for a function should be the function body using $1, $2 for parameters.
8. Do NOT include 'alias name=' or 'name() {' in the template — just the body.
9. Params array must be populated for functions; empty for pure aliases.
10. Avoid suggesting aliases for trivial commands.

Prioritization — rank suggestions by descending impact:
1. HIGHEST: Multi-step workflow functions — commands that appear in sequence (close timestamps, seconds apart) and can be combined into one function (e.g. git add + commit + push → a single "gacp" function).
2. HIGH: Long commands (>25 chars) with variable parts — verbose flag combinations where the user swaps one argument.
3. MEDIUM: Long verbatim commands (>25 chars) that repeat identically.
4. LOWER: Short-but-conventional aliases (e.g. "dcu" for "docker compose up") — only include these if the savings are obvious and the name is idiomatic.

Return at most 15 suggestions, ordered highest-impact first. Do not pad with low-value suggestions just to reach 15.`

// Suggest calls the Anthropic API and returns alias/function suggestions.
func (a *AnthropicProvider) Suggest(ctx context.Context, censored []history.Entry) ([]Suggestion, error) {
	prompt := suggest.BuildPrompt(censored)
	toolSchema, err := suggest.ToolSchema()
	if err != nil {
		return nil, fmt.Errorf("build tool schema: %w", err)
	}

	reqBody := anthropicRequest{
		Model:     a.model,
		MaxTokens: 4096,
		System: []systemBlock{
			{
				Type:         "text",
				Text:         systemPromptText,
				CacheControl: &cacheControl{Type: "ephemeral"},
			},
		},
		Tools: []anthropicTool{
			{
				Name:        "suggest_aliases",
				Description: "Suggest shell aliases and functions based on command history patterns.",
				InputSchema: toolSchema,
			},
		},
		ToolChoice: toolChoice{
			Type: "tool",
			Name: "suggest_aliases",
		},
		Messages: []message{
			{
				Role:    "user",
				Content: prompt,
			},
		},
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, anthropicAPI, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", a.apiKey)
	req.Header.Set("anthropic-version", anthropicVersion)
	req.Header.Set("anthropic-beta", "prompt-caching-2024-07-31")

	resp, err := a.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer resp.Body.Close()

	respData, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		var errResp struct {
			Error struct {
				Message string `json:"message"`
				Type    string `json:"type"`
			} `json:"error"`
		}
		if jsonErr := json.Unmarshal(respData, &errResp); jsonErr == nil && errResp.Error.Message != "" {
			return nil, fmt.Errorf("anthropic API error (%s): %s", errResp.Error.Type, errResp.Error.Message)
		}
		return nil, fmt.Errorf("anthropic API returned status %d: %s", resp.StatusCode, string(respData))
	}

	var apiResp anthropicResponse
	if err := json.Unmarshal(respData, &apiResp); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	// Find the tool_use content block.
	for _, block := range apiResp.Content {
		if block.Type == "tool_use" && block.Name == "suggest_aliases" {
			var output toolOutput
			if err := json.Unmarshal(block.Input, &output); err != nil {
				return nil, fmt.Errorf("parse tool output: %w", err)
			}
			return output.Suggestions, nil
		}
	}

	return nil, fmt.Errorf("no tool_use block in response")
}
