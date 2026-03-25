package gateway

import (
	"encoding/json"
	"fmt"
	"strings"
)

func buildOpenAIRequest(req anthropicRequest, model string) (openAIChatRequest, int, error) {
	messages := make([]openAIMessage, 0, len(req.Messages)+1)
	estimatedInputTokens := 0

	if systemText := extractSystemText(req.System); systemText != "" {
		messages = append(messages, openAIMessage{
			Role:    "system",
			Content: systemText,
		})
		estimatedInputTokens += estimateTextTokens(systemText)
	}

	for _, message := range req.Messages {
		converted, tokens, err := convertAnthropicMessage(message)
		if err != nil {
			return openAIChatRequest{}, 0, err
		}
		messages = append(messages, converted...)
		estimatedInputTokens += tokens
	}

	payload := openAIChatRequest{
		Model:    model,
		Messages: messages,
		Stream:   req.Stream,
	}
	if req.MaxTokens > 0 {
		payload.MaxTokens = req.MaxTokens
	}
	if req.Temperature != nil {
		payload.Temperature = req.Temperature
	}
	if len(req.StopSequences) == 1 {
		payload.Stop = req.StopSequences[0]
	} else if len(req.StopSequences) > 1 {
		payload.Stop = req.StopSequences
	}
	if req.Stream {
		payload.StreamOptions = &openAIStreamOptions{IncludeUsage: true}
	}

	if len(req.Tools) > 0 {
		payload.Tools = make([]openAITool, 0, len(req.Tools))
		for _, tool := range req.Tools {
			payload.Tools = append(payload.Tools, openAITool{
				Type: "function",
				Function: openAIToolFunctionSpec{
					Name:        tool.Name,
					Description: tool.Description,
					Parameters:  tool.InputSchema,
				},
			})
		}

		switch choice := req.ToolChoice.(type) {
		case string:
			switch choice {
			case "auto":
				payload.ToolChoice = "auto"
			case "any":
				payload.ToolChoice = "required"
			}
		case map[string]any:
			if choiceType, _ := choice["type"].(string); choiceType == "tool" {
				if name, _ := choice["name"].(string); name != "" {
					payload.ToolChoice = map[string]any{
						"type": "function",
						"function": map[string]any{
							"name": name,
						},
					}
				}
			}
		}
		if payload.ToolChoice == nil {
			payload.ToolChoice = "auto"
		}
	}

	return payload, estimatedInputTokens, nil
}

func convertAnthropicMessage(message anthropicInput) ([]openAIMessage, int, error) {
	switch content := message.Content.(type) {
	case string:
		return []openAIMessage{{
			Role:    message.Role,
			Content: content,
		}}, estimateTextTokens(content), nil
	case []any:
		return convertAnthropicBlocks(message.Role, content)
	default:
		return nil, 0, fmt.Errorf("unsupported message content type for role %s", message.Role)
	}
}

func convertAnthropicBlocks(role string, blocks []any) ([]openAIMessage, int, error) {
	var (
		textParts    []string
		richParts    []map[string]any
		toolCalls    []openAIToolCall
		toolMessages []openAIMessage
		tokens       int
	)

	for _, block := range blocks {
		current, ok := block.(map[string]any)
		if !ok {
			continue
		}

		switch current["type"] {
		case "text":
			text, _ := current["text"].(string)
			if text == "" {
				continue
			}
			textParts = append(textParts, text)
			richParts = append(richParts, map[string]any{
				"type": "text",
				"text": text,
			})
			tokens += estimateTextTokens(text)
		case "image":
			part, ok := convertImageBlock(current)
			if ok {
				richParts = append(richParts, part)
			}
		case "tool_use":
			id, _ := current["id"].(string)
			name, _ := current["name"].(string)
			input := current["input"]
			args, err := json.Marshal(input)
			if err != nil {
				return nil, 0, err
			}
			toolCalls = append(toolCalls, openAIToolCall{
				ID:   id,
				Type: "function",
				Function: openAIToolFunction{
					Name:      name,
					Arguments: string(args),
				},
			})
			tokens += estimateTextTokens(string(args))
		case "tool_result":
			id, _ := current["tool_use_id"].(string)
			content := extractToolResultText(current["content"])
			tokens += estimateTextTokens(content)
			toolMessages = append(toolMessages, openAIMessage{
				Role:       "tool",
				ToolCallID: id,
				Content:    content,
			})
		case "thinking":
			continue
		}
	}

	converted := make([]openAIMessage, 0, 1+len(toolMessages))
	if len(richParts) > 0 || len(toolCalls) > 0 {
		message := openAIMessage{Role: role}
		switch {
		case len(richParts) > 0 && containsNonTextPart(richParts):
			message.Content = richParts
		case len(textParts) > 0:
			message.Content = strings.Join(textParts, "\n")
		}
		if len(toolCalls) > 0 {
			message.ToolCalls = toolCalls
		}
		converted = append(converted, message)
	}
	converted = append(converted, toolMessages...)
	return converted, tokens, nil
}

func convertImageBlock(block map[string]any) (map[string]any, bool) {
	source, ok := block["source"].(map[string]any)
	if !ok {
		return nil, false
	}
	if sourceType, _ := source["type"].(string); sourceType == "base64" {
		mediaType, _ := source["media_type"].(string)
		data, _ := source["data"].(string)
		if data == "" {
			return nil, false
		}
		if mediaType == "" {
			mediaType = "image/png"
		}
		return map[string]any{
			"type": "image_url",
			"image_url": map[string]any{
				"url": fmt.Sprintf("data:%s;base64,%s", mediaType, data),
			},
		}, true
	}
	if sourceType, _ := source["type"].(string); sourceType == "url" {
		value, _ := source["url"].(string)
		if value == "" {
			return nil, false
		}
		return map[string]any{
			"type": "image_url",
			"image_url": map[string]any{
				"url": value,
			},
		}, true
	}
	return nil, false
}

func containsNonTextPart(parts []map[string]any) bool {
	for _, part := range parts {
		if value, _ := part["type"].(string); value != "text" {
			return true
		}
	}
	return false
}

func extractSystemText(system any) string {
	switch value := system.(type) {
	case string:
		return strings.TrimSpace(value)
	case []any:
		lines := make([]string, 0, len(value))
		for _, item := range value {
			part, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if text, _ := part["text"].(string); text != "" {
				lines = append(lines, text)
			}
		}
		return strings.TrimSpace(strings.Join(lines, "\n"))
	default:
		return ""
	}
}

func extractToolResultText(content any) string {
	switch value := content.(type) {
	case string:
		return value
	case []any:
		lines := make([]string, 0, len(value))
		for _, item := range value {
			block, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if text, _ := block["text"].(string); text != "" {
				lines = append(lines, text)
			}
		}
		return strings.Join(lines, "\n")
	default:
		raw, _ := json.Marshal(value)
		return string(raw)
	}
}

func transformOpenAIResponse(raw []byte, requestedModel string) (map[string]any, error) {
	var response openAIChatResponse
	if err := json.Unmarshal(raw, &response); err != nil {
		return nil, err
	}

	if response.Error != nil && response.Error.Message != "" {
		return nil, fmt.Errorf("%s", response.Error.Message)
	}
	if len(response.Choices) == 0 {
		return nil, fmt.Errorf("upstream response contained no choices")
	}

	choice := response.Choices[0]
	blocks := make([]map[string]any, 0)

	switch content := choice.Message.Content.(type) {
	case string:
		if strings.TrimSpace(content) != "" {
			blocks = append(blocks, map[string]any{
				"type": "text",
				"text": content,
			})
		}
	case []any:
		for _, item := range content {
			block, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if blockType, _ := block["type"].(string); blockType == "text" {
				if text, _ := block["text"].(string); text != "" {
					blocks = append(blocks, map[string]any{
						"type": "text",
						"text": text,
					})
				}
			}
		}
	}

	for _, toolCall := range choice.Message.ToolCalls {
		var input map[string]any
		if err := json.Unmarshal([]byte(toolCall.Function.Arguments), &input); err != nil {
			input = map[string]any{}
		}
		blocks = append(blocks, map[string]any{
			"type":  "tool_use",
			"id":    toolCall.ID,
			"name":  toolCall.Function.Name,
			"input": input,
		})
	}

	model := requestedModel
	if model == "" {
		model = response.Model
	}

	return map[string]any{
		"id":            response.ID,
		"type":          "message",
		"role":          "assistant",
		"model":         model,
		"content":       blocks,
		"stop_reason":   mapFinishReason(choice.FinishReason),
		"stop_sequence": nil,
		"usage": map[string]any{
			"input_tokens":  response.Usage.PromptTokens,
			"output_tokens": response.Usage.CompletionTokens,
		},
	}, nil
}

func resolveModel(requested string, available []string) string {
	requested = strings.TrimSpace(requested)
	if requested == "" {
		if len(available) > 0 {
			return available[0]
		}
		return "gpt-4.1-mini"
	}

	set := make(map[string]struct{}, len(available))
	for _, item := range available {
		set[item] = struct{}{}
	}
	if _, ok := set[requested]; ok {
		return requested
	}

	requestLower := strings.ToLower(requested)
	preferred := []string{}
	switch {
	case strings.Contains(requestLower, "haiku"):
		preferred = []string{"mini", "small", "haiku"}
	case strings.Contains(requestLower, "sonnet"):
		preferred = []string{"4.1", "4o", "sonnet", "turbo"}
	case strings.Contains(requestLower, "opus"):
		preferred = []string{"o1", "o3", "4.1", "4o"}
	}

	for _, keyword := range preferred {
		for _, item := range available {
			if strings.Contains(strings.ToLower(item), keyword) {
				return item
			}
		}
	}
	if len(available) > 0 {
		return available[0]
	}
	return requested
}

func estimateAnthropicInputTokens(req anthropicRequest) int {
	total := estimateTextTokens(extractSystemText(req.System))
	for _, message := range req.Messages {
		switch content := message.Content.(type) {
		case string:
			total += estimateTextTokens(content)
		case []any:
			for _, item := range content {
				block, ok := item.(map[string]any)
				if !ok {
					continue
				}
				switch block["type"] {
				case "text":
					if text, _ := block["text"].(string); text != "" {
						total += estimateTextTokens(text)
					}
				case "tool_result":
					total += estimateTextTokens(extractToolResultText(block["content"]))
				case "tool_use":
					raw, _ := json.Marshal(block["input"])
					total += estimateTextTokens(string(raw))
				}
			}
		}
	}
	return total
}
