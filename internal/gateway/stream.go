package gateway

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type streamBlock struct {
	AnthropicIndex int
	OpenAIIndex    int
	Kind           string
	Started        bool
}

type streamState struct {
	RequestedModel string
	InputTokens    int
	OutputText     strings.Builder
	Current        *streamBlock
	TextStarted    bool
}

func (s *Server) streamAnthropicResponse(w http.ResponseWriter, resp *http.Response, requestedModel string, estimatedInputTokens int) error {
	defer resp.Body.Close()

	flusher, ok := w.(http.Flusher)
	if !ok {
		return fmt.Errorf("streaming not supported by response writer")
	}

	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)

	state := &streamState{
		RequestedModel: requestedModel,
		InputTokens:    estimatedInputTokens,
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 2*1024*1024)

	var event bytes.Buffer
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			if event.Len() > 0 {
				done, err := consumeOpenAIStreamEvent(w, flusher, state, event.Bytes())
				if err != nil {
					return err
				}
				event.Reset()
				if done {
					return nil
				}
			}
			continue
		}
		event.WriteString(line)
		event.WriteByte('\n')
	}

	if err := scanner.Err(); err != nil {
		return err
	}

	if event.Len() > 0 {
		_, err := consumeOpenAIStreamEvent(w, flusher, state, event.Bytes())
		return err
	}
	return nil
}

func consumeOpenAIStreamEvent(w http.ResponseWriter, flusher http.Flusher, state *streamState, raw []byte) (bool, error) {
	payload := strings.TrimSpace(extractSSEData(raw))
	if payload == "" {
		return false, nil
	}
	if payload == "[DONE]" {
		if err := finalizeAnthropicStream(w, flusher, state, mapFinishReason("stop"), 0); err != nil {
			return false, err
		}
		return true, nil
	}

	var chunk openAIChatResponse
	if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
		return false, err
	}
	if len(chunk.Choices) == 0 {
		return false, nil
	}

	choice := chunk.Choices[0]
	if !state.TextStarted && state.Current == nil {
		if err := writeSSE(w, "message_start", map[string]any{
			"type": "message_start",
			"message": map[string]any{
				"id":      firstNonEmpty(chunk.ID, randomID("msg")),
				"type":    "message",
				"role":    "assistant",
				"model":   firstNonEmpty(state.RequestedModel, chunk.Model),
				"content": []any{},
				"usage": map[string]any{
					"input_tokens":  state.InputTokens,
					"output_tokens": 0,
				},
			},
		}); err != nil {
			return false, err
		}
		flusher.Flush()
	}

	if text := choice.Delta.Content; text != "" {
		if err := ensureTextBlockStarted(w, flusher, state); err != nil {
			return false, err
		}
		state.OutputText.WriteString(text)
		if err := writeSSE(w, "content_block_delta", map[string]any{
			"type":  "content_block_delta",
			"index": state.Current.AnthropicIndex,
			"delta": map[string]any{
				"type": "text_delta",
				"text": text,
			},
		}); err != nil {
			return false, err
		}
		flusher.Flush()
	}

	for _, tool := range choice.Delta.ToolCalls {
		if err := ensureToolBlockStarted(w, flusher, state, tool); err != nil {
			return false, err
		}
		if tool.Function.Arguments != "" {
			if err := writeSSE(w, "content_block_delta", map[string]any{
				"type":  "content_block_delta",
				"index": state.Current.AnthropicIndex,
				"delta": map[string]any{
					"type":         "input_json_delta",
					"partial_json": tool.Function.Arguments,
				},
			}); err != nil {
				return false, err
			}
			flusher.Flush()
		}
	}

	if choice.FinishReason != "" {
		outputTokens := chunk.Usage.CompletionTokens
		if outputTokens == 0 {
			outputTokens = estimateTextTokens(state.OutputText.String())
		}
		if err := finalizeAnthropicStream(w, flusher, state, mapFinishReason(choice.FinishReason), outputTokens); err != nil {
			return false, err
		}
		return true, nil
	}

	return false, nil
}

func ensureTextBlockStarted(w http.ResponseWriter, flusher http.Flusher, state *streamState) error {
	if state.Current != nil && state.Current.Kind == "text" {
		return nil
	}
	if err := closeCurrentBlock(w, flusher, state); err != nil {
		return err
	}

	state.TextStarted = true
	state.Current = &streamBlock{
		AnthropicIndex: 0,
		OpenAIIndex:    0,
		Kind:           "text",
		Started:        true,
	}

	if err := writeSSE(w, "content_block_start", map[string]any{
		"type":  "content_block_start",
		"index": 0,
		"content_block": map[string]any{
			"type": "text",
			"text": "",
		},
	}); err != nil {
		return err
	}
	flusher.Flush()
	return nil
}

func ensureToolBlockStarted(w http.ResponseWriter, flusher http.Flusher, state *streamState, tool openAIStreamToolUse) error {
	anthropicIndex := tool.Index
	if state.TextStarted {
		anthropicIndex++
	}

	if state.Current != nil && state.Current.Kind == "tool_use" && state.Current.OpenAIIndex == tool.Index {
		return nil
	}
	if err := closeCurrentBlock(w, flusher, state); err != nil {
		return err
	}

	state.Current = &streamBlock{
		AnthropicIndex: anthropicIndex,
		OpenAIIndex:    tool.Index,
		Kind:           "tool_use",
		Started:        true,
	}

	if err := writeSSE(w, "content_block_start", map[string]any{
		"type":  "content_block_start",
		"index": anthropicIndex,
		"content_block": map[string]any{
			"type":  "tool_use",
			"id":    firstNonEmpty(tool.ID, randomID("toolu")),
			"name":  tool.Function.Name,
			"input": map[string]any{},
		},
	}); err != nil {
		return err
	}
	flusher.Flush()
	return nil
}

func closeCurrentBlock(w http.ResponseWriter, flusher http.Flusher, state *streamState) error {
	if state.Current == nil {
		return nil
	}
	if err := writeSSE(w, "content_block_stop", map[string]any{
		"type":  "content_block_stop",
		"index": state.Current.AnthropicIndex,
	}); err != nil {
		return err
	}
	flusher.Flush()
	state.Current = nil
	return nil
}

func finalizeAnthropicStream(w http.ResponseWriter, flusher http.Flusher, state *streamState, stopReason string, outputTokens int) error {
	if err := closeCurrentBlock(w, flusher, state); err != nil {
		return err
	}
	if err := writeSSE(w, "message_delta", map[string]any{
		"type": "message_delta",
		"delta": map[string]any{
			"stop_reason":   stopReason,
			"stop_sequence": nil,
		},
		"usage": map[string]any{
			"output_tokens": outputTokens,
		},
	}); err != nil {
		return err
	}
	flusher.Flush()
	if err := writeSSE(w, "message_stop", map[string]any{"type": "message_stop"}); err != nil {
		return err
	}
	flusher.Flush()
	return nil
}

func writeSSE(w io.Writer, event string, value any) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, payload)
	return err
}

func extractSSEData(raw []byte) string {
	lines := strings.Split(string(raw), "\n")
	chunks := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "data:") {
			chunks = append(chunks, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	return strings.Join(chunks, "\n")
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
