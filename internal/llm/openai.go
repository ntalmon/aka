package llm

const (
	DefaultOpenAIModel = "gpt-4o-mini"
	openAIChatURL      = "https://api.openai.com/v1/chat/completions"
)

// NewOpenAI creates an OpenAI provider.
func NewOpenAI(apiKey string) *OpenAICompatProvider {
	return newOpenAICompat(openAIChatURL, apiKey, DefaultOpenAIModel)
}
