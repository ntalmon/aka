package llm

const (
	DefaultGroqModel = "llama-3.3-70b-versatile"
	groqChatURL      = "https://api.groq.com/openai/v1/chat/completions"
)

// NewGroq creates a Groq provider using the OpenAI-compatible API.
func NewGroq(apiKey string) *OpenAICompatProvider {
	return newOpenAICompat(groqChatURL, apiKey, DefaultGroqModel)
}
