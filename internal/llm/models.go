package llm

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
	{ID: "groq", Label: "Groq (Llama, Mixtral, Gemma)"},
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
	default: // anthropic
		return []ModelOption{
			{ID: "claude-haiku-4-5-20251001", Label: "Haiku 4.5 — fastest, cheapest (recommended)"},
			{ID: "claude-sonnet-4-6", Label: "Sonnet 4.6 — balanced"},
			{ID: "claude-opus-4-7", Label: "Opus 4.7 — most capable"},
		}
	}
}
