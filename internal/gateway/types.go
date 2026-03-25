package gateway

// Types for OpenAI Models API
type openAIModelsEnvelope struct {
	Data []openAIModel `json:"data"`
}

type openAIModel struct {
	ID string `json:"id"`
}

// Types for error responses
type openAIError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
}