package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/ntalmon/aka/internal/history"
	"github.com/ntalmon/aka/internal/suggest"
)

// ErrTokenLimit is returned when the request exceeds the model's context or rate limit.
type ErrTokenLimit struct{ msg string }

func (e *ErrTokenLimit) Error() string { return e.msg }

// OpenAICompatProvider implements Provider using the OpenAI-compatible chat completions API.
// Works with OpenAI, Groq, Gemini (via compat endpoint), Ollama, and similar providers.
type OpenAICompatProvider struct {
	apiURL string
	apiKey string
	model  string
	client *http.Client
}

func newOpenAICompat(apiURL, apiKey, model string) *OpenAICompatProvider {
	return &OpenAICompatProvider{
		apiURL: apiURL,
		apiKey: apiKey,
		model:  model,
		client: &http.Client{Timeout: 120 * time.Second},
	}
}

// WithModel returns a shallow copy with the model overridden.
func (p *OpenAICompatProvider) WithModel(model string) *OpenAICompatProvider {
	cp := *p
	cp.model = model
	return &cp
}

type oaiRequest struct {
	Model      string       `json:"model"`
	MaxTokens  int          `json:"max_tokens"`
	Messages   []oaiMessage `json:"messages"`
	Tools      []oaiTool    `json:"tools"`
	ToolChoice any          `json:"tool_choice"`
}

type oaiMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type oaiTool struct {
	Type     string      `json:"type"`
	Function oaiFunction `json:"function"`
}

type oaiFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

type oaiResponse struct {
	Choices []oaiChoice `json:"choices"`
}

type oaiChoice struct {
	Message oaiChoiceMessage `json:"message"`
}

type oaiChoiceMessage struct {
	ToolCalls []oaiToolCall `json:"tool_calls"`
}

type oaiToolCall struct {
	Function oaiToolCallFunction `json:"function"`
}

type oaiToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// Suggest calls the OpenAI-compatible API and returns alias/function suggestions.
func (p *OpenAICompatProvider) Suggest(ctx context.Context, censored []history.Entry) ([]Suggestion, error) {
	prompt := suggest.BuildPrompt(censored)
	toolSchema, err := suggest.ToolSchema()
	if err != nil {
		return nil, fmt.Errorf("build tool schema: %w", err)
	}

	reqBody := oaiRequest{
		Model:     p.model,
		MaxTokens: 4096,
		Messages: []oaiMessage{
			{Role: "system", Content: systemPromptText},
			{Role: "user", Content: prompt},
		},
		Tools: []oaiTool{
			{
				Type: "function",
				Function: oaiFunction{
					Name:        "suggest_aliases",
					Description: "Suggest shell aliases and functions based on command history patterns.",
					Parameters:  toolSchema,
				},
			},
		},
		ToolChoice: map[string]any{
			"type":     "function",
			"function": map[string]string{"name": "suggest_aliases"},
		},
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.apiURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if p.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.apiKey)
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respData, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		var errResp struct {
			Error struct {
				Message string `json:"message"`
				Type    string `json:"type"`
				Code    string `json:"code"`
			} `json:"error"`
		}
		if jsonErr := json.Unmarshal(respData, &errResp); jsonErr == nil && errResp.Error.Message != "" {
			if errResp.Error.Type == "tokens" || errResp.Error.Code == "context_length_exceeded" {
				return nil, &ErrTokenLimit{msg: errResp.Error.Message}
			}
			return nil, fmt.Errorf("%s API error (%s): %s", p.model, errResp.Error.Type, errResp.Error.Message)
		}
		return nil, fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(respData))
	}

	var apiResp oaiResponse
	if err := json.Unmarshal(respData, &apiResp); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	if len(apiResp.Choices) == 0 {
		return nil, fmt.Errorf("no choices in API response")
	}
	for _, tc := range apiResp.Choices[0].Message.ToolCalls {
		if tc.Function.Name == "suggest_aliases" {
			var output toolOutput
			if err := json.Unmarshal([]byte(tc.Function.Arguments), &output); err != nil {
				return nil, fmt.Errorf("parse tool output: %w", err)
			}
			return output.Suggestions, nil
		}
	}

	return nil, fmt.Errorf("no suggest_aliases tool call in API response")
}
