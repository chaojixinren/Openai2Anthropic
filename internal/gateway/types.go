package gateway

type anthropicRequest struct {
	Model         string           `json:"model"`
	System        any              `json:"system,omitempty"`
	Messages      []anthropicInput `json:"messages"`
	Tools         []anthropicTool  `json:"tools,omitempty"`
	ToolChoice    any              `json:"tool_choice,omitempty"`
	MaxTokens     int              `json:"max_tokens,omitempty"`
	Temperature   *float64         `json:"temperature,omitempty"`
	Stream        bool             `json:"stream,omitempty"`
	StopSequences []string         `json:"stop_sequences,omitempty"`
}

type anthropicInput struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

type anthropicTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	InputSchema map[string]any `json:"input_schema"`
}

type openAIChatRequest struct {
	Model         string                `json:"model"`
	Messages      []openAIMessage       `json:"messages"`
	Tools         []openAITool          `json:"tools,omitempty"`
	ToolChoice    any                   `json:"tool_choice,omitempty"`
	MaxTokens     int                   `json:"max_tokens,omitempty"`
	Temperature   *float64              `json:"temperature,omitempty"`
	Stream        bool                  `json:"stream,omitempty"`
	StreamOptions *openAIStreamOptions  `json:"stream_options,omitempty"`
	Stop          any                   `json:"stop,omitempty"`
}

type openAIMessage struct {
	Role       string           `json:"role"`
	Content    any              `json:"content,omitempty"`
	ToolCalls  []openAIToolCall `json:"tool_calls,omitempty"`
	ToolCallID string           `json:"tool_call_id,omitempty"`
}

type openAITool struct {
	Type     string                 `json:"type"`
	Function openAIToolFunctionSpec `json:"function"`
}

type openAIToolFunctionSpec struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters"`
}

type openAIToolCall struct {
	ID       string             `json:"id"`
	Type     string             `json:"type"`
	Function openAIToolFunction `json:"function"`
}

type openAIToolFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type openAIStreamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type openAIChatResponse struct {
	ID      string         `json:"id"`
	Object  string         `json:"object"`
	Model   string         `json:"model"`
	Choices []openAIChoice `json:"choices"`
	Usage   openAIUsage    `json:"usage"`
	Error   *openAIError   `json:"error,omitempty"`
}

type openAIChoice struct {
	Index        int           `json:"index"`
	Message      openAIMessage `json:"message"`
	Delta        openAIDelta   `json:"delta"`
	FinishReason string        `json:"finish_reason"`
}

type openAIDelta struct {
	Role      string                `json:"role,omitempty"`
	Content   string                `json:"content,omitempty"`
	ToolCalls []openAIStreamToolUse `json:"tool_calls,omitempty"`
}

type openAIStreamToolUse struct {
	Index    int                        `json:"index"`
	ID       string                     `json:"id,omitempty"`
	Type     string                     `json:"type,omitempty"`
	Function openAIStreamToolFunction   `json:"function"`
}

type openAIStreamToolFunction struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

type openAIUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type openAIError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
}

type openAIModelsEnvelope struct {
	Data []openAIModel `json:"data"`
}

type openAIModel struct {
	ID string `json:"id"`
}

