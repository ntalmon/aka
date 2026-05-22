package llm

const (
	DefaultGeminiModel = "gemini-2.0-flash"
	// Gemini exposes an OpenAI-compatible endpoint.
	geminiChatURL = "https://generativelanguage.googleapis.com/v1beta/openai/chat/completions"
)

// NewGemini creates a Google Gemini provider via the OpenAI-compatible endpoint.
func NewGemini(apiKey string) *OpenAICompatProvider {
	return newOpenAICompat(geminiChatURL, apiKey, DefaultGeminiModel)
}
