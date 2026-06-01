package llm

import "strings"

// ModelOption is a selectable model with a human-readable label.
type ModelOption struct {
	ID    string
	Label string
}

// ProviderOption is a selectable LLM provider.
type ProviderOption struct {
	ID    string
	Label string
}

// SupportedProviders lists all providers aka knows about.
var SupportedProviders = []ProviderOption{
	{ID: "anthropic", Label: "Anthropic (Claude)"},
	{ID: "gemini", Label: "Google Gemini"},
	{ID: "openai", Label: "OpenAI (GPT-4o)"},
	{ID: "groq", Label: "Groq (Llama, Mixtral)"},
	{ID: "ollama", Label: "Ollama (local, no API key)"},
}

// FriendlyModelName returns a short human-readable model name (e.g. "Haiku 4.5")
// without tier/speed suffixes. Falls back to the raw model ID if not found.
func FriendlyModelName(provider, model string) string {
	for _, opt := range ModelsForProvider(provider) {
		if opt.ID == model {
			label := opt.Label
			if i := strings.Index(label, " —"); i >= 0 {
				return label[:i]
			}
			return label
		}
	}
	return model
}

// ModelsForProvider returns the supported models for the given provider,
// with the recommended default first.
func ModelsForProvider(provider string) []ModelOption {
	switch provider {
	case "groq":
		return []ModelOption{
			{ID: "llama-3.3-70b-versatile", Label: "Llama 3.3 70B — best overall (recommended)"},
			{ID: "llama-3.1-70b-versatile", Label: "Llama 3.1 70B"},
			{ID: "llama-3.1-8b-instant", Label: "Llama 3.1 8B — fastest"},
			{ID: "mixtral-8x7b-32768", Label: "Mixtral 8x7B — long context"},
			{ID: "gemma2-9b-it", Label: "Gemma 2 9B"},
		}
	case "openai":
		return []ModelOption{
			{ID: "gpt-4o-mini", Label: "GPT-4o mini — fast, cheap (recommended)"},
			{ID: "gpt-4o", Label: "GPT-4o — most capable"},
			{ID: "gpt-4-turbo", Label: "GPT-4 Turbo"},
		}
	case "gemini":
		return []ModelOption{
			{ID: "gemini-2.0-flash", Label: "Gemini 2.0 Flash — fast, free tier (recommended)"},
			{ID: "gemini-1.5-pro", Label: "Gemini 1.5 Pro — most capable"},
			{ID: "gemini-1.5-flash", Label: "Gemini 1.5 Flash — fast"},
		}
	case "ollama":
		return []ModelOption{
			{ID: "llama3.3", Label: "Llama 3.3 (recommended)"},
			{ID: "llama3.2", Label: "Llama 3.2"},
			{ID: "mistral", Label: "Mistral 7B"},
			{ID: "qwen2.5-coder", Label: "Qwen 2.5 Coder"},
		}
	default: // anthropic
		return []ModelOption{
			{ID: "claude-haiku-4-5-20251001", Label: "Haiku 4.5 — fastest, cheapest (recommended)"},
			{ID: "claude-sonnet-4-6", Label: "Sonnet 4.6 — balanced"},
			{ID: "claude-opus-4-8", Label: "Opus 4.8 — most capable"},
		}
	}
}
