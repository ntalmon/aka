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
	DefaultGroqModel = "llama-3.3-70b-versatile"
	groqAPI          = "https://api.groq.com/openai/v1/chat/completions"
)

// ErrTokenLimit is returned when the request exceeds the model's TPM limit.
type ErrTokenLimit struct{ msg string }

func (e *ErrTokenLimit) Error() string { return e.msg }

// GroqProvider implements Provider using Groq's OpenAI-compatible API.
type GroqProvider struct {
	apiKey string
	model  string
	client *http.Client
}

// NewGroq creates a GroqProvider with the given API key.
func NewGroq(apiKey string) *GroqProvider {
	return &GroqProvider{
		apiKey: apiKey,
		model:  DefaultGroqModel,
		client: &http.Client{Timeout: 120 * time.Second},
	}
}

// WithModel overrides the default model.
func (g *GroqProvider) WithModel(model string) *GroqProvider {
	g.model = model
	return g
}

type groqRequest struct {
	Model      string        `json:"model"`
	MaxTokens  int           `json:"max_tokens"`
	Messages   []groqMessage `json:"messages"`
	Tools      []groqTool    `json:"tools"`
	ToolChoice any           `json:"tool_choice"`
}

type groqMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type groqTool struct {
	Type     string       `json:"type"`
	Function groqFunction `json:"function"`
}

type groqFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

type groqResponse struct {
	Choices []groqChoice `json:"choices"`
}

type groqChoice struct {
	Message groqChoiceMessage `json:"message"`
}

type groqChoiceMessage struct {
	ToolCalls []groqToolCall `json:"tool_calls"`
}

type groqToolCall struct {
	Function groqToolCallFunction `json:"function"`
}

type groqToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// Suggest calls the Groq API and returns alias/function suggestions.
func (g *GroqProvider) Suggest(ctx context.Context, censored []history.Entry) ([]Suggestion, error) {
	prompt := suggest.BuildPrompt(censored)
	toolSchema, err := suggest.ToolSchema()
	if err != nil {
		return nil, fmt.Errorf("build tool schema: %w", err)
	}

	reqBody := groqRequest{
		Model:     g.model,
		MaxTokens: 4096,
		Messages: []groqMessage{
			{Role: "system", Content: systemPromptText},
			{Role: "user", Content: prompt},
		},
		Tools: []groqTool{
			{
				Type: "function",
				Function: groqFunction{
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

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, groqAPI, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+g.apiKey)

	resp, err := g.client.Do(req)
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
			if errResp.Error.Type == "tokens" {
				return nil, &ErrTokenLimit{msg: errResp.Error.Message}
			}
			return nil, fmt.Errorf("groq API error (%s): %s", errResp.Error.Type, errResp.Error.Message)
		}
		return nil, fmt.Errorf("groq API returned status %d: %s", resp.StatusCode, string(respData))
	}

	var apiResp groqResponse
	if err := json.Unmarshal(respData, &apiResp); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	if len(apiResp.Choices) == 0 {
		return nil, fmt.Errorf("no choices in groq response")
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

	return nil, fmt.Errorf("no suggest_aliases tool call in groq response")
}
