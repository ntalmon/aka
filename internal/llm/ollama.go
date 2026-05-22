package llm

const (
	DefaultOllamaModel   = "llama3.3"
	DefaultOllamaBaseURL = "http://localhost:11434"
)

// NewOllama creates an Ollama provider using the local OpenAI-compatible endpoint.
// No API key is required.
func NewOllama(model string) *OpenAICompatProvider {
	return newOpenAICompat(DefaultOllamaBaseURL+"/v1/chat/completions", "", model)
}
