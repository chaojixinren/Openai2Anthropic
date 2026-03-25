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

type streamState struct {
	RequestedModel   string
	InputTokens      int
	OutputTokens     int
	MessageID        string
	MessageStarted   bool
	TextBlockStarted bool
	ToolBlockStarted bool
	TextIndex        int
	ToolIndex        int
	CurrentToolID    string
	CurrentToolName  string
	ToolArguments    string
	OutputText       strings.Builder
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
				done, err := consumeOpenAI2StreamEvent(w, flusher, state, event.Bytes())
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
		_, err := consumeOpenAI2StreamEvent(w, flusher, state, event.Bytes())
		return err
	}
	return nil
}

// OpenAI2 stream event types
type openAI2StreamEvent struct {
	Type         string `json:"type"`
	OutputIndex  int    `json:"output_index,omitempty"`
	ContentIndex int    `json:"content_index,omitempty"`
	Delta        string `json:"delta,omitempty"`
	Response     *struct {
		ID     string `json:"id"`
		Status string `json:"status"`
		Usage  struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
			TotalTokens  int `json:"total_tokens"`
		} `json:"usage"`
	} `json:"response,omitempty"`
	Item *struct {
		Type      string `json:"type"`
		ID        string `json:"id,omitempty"`
		Role      string `json:"role,omitempty"`
		CallID    string `json:"call_id,omitempty"`
		Name      string `json:"name,omitempty"`
		Arguments string `json:"arguments,omitempty"`
		Status    string `json:"status,omitempty"`
	} `json:"item,omitempty"`
}

func consumeOpenAI2StreamEvent(w http.ResponseWriter, flusher http.Flusher, state *streamState, raw []byte) (bool, error) {
	payload := strings.TrimSpace(extractSSEData(raw))
	if payload == "" {
		return false, nil
	}
	if payload == "[DONE]" {
		finalizeStream(w, flusher, state, "end_turn")
		return true, nil
	}

	var evt openAI2StreamEvent
	if err := json.Unmarshal([]byte(payload), &evt); err != nil {
		return false, nil
	}

	switch evt.Type {
	case "response.created":
		if evt.Response != nil {
			state.MessageID = evt.Response.ID
			state.MessageStarted = true
			if err := writeSSE(w, "message_start", map[string]any{
				"type": "message_start",
				"message": map[string]any{
					"id":      state.MessageID,
					"type":    "message",
					"role":    "assistant",
					"model":   state.RequestedModel,
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

	case "response.output_text.delta":
		if evt.Delta != "" {
			if !state.TextBlockStarted {
				state.TextBlockStarted = true
				state.TextIndex = 0
				if err := writeSSE(w, "content_block_start", map[string]any{
					"type":  "content_block_start",
					"index": 0,
					"content_block": map[string]any{
						"type": "text",
						"text": "",
					},
				}); err != nil {
					return false, err
				}
				flusher.Flush()
			}

			state.OutputText.WriteString(evt.Delta)
			if err := writeSSE(w, "content_block_delta", map[string]any{
				"type":  "content_block_delta",
				"index": 0,
				"delta": map[string]any{
					"type": "text_delta",
					"text": evt.Delta,
				},
			}); err != nil {
				return false, err
			}
			flusher.Flush()
		}

	case "response.output_item.added":
		if evt.Item != nil && evt.Item.Type == "function_call" {
			// Close text block if open
			if state.TextBlockStarted {
				if err := writeSSE(w, "content_block_stop", map[string]any{
					"type":  "content_block_stop",
					"index": state.TextIndex,
				}); err != nil {
					return false, err
				}
				flusher.Flush()
				state.TextBlockStarted = false
				state.ToolIndex = 1
			} else {
				state.ToolIndex = 0
			}

			state.ToolBlockStarted = true
			state.CurrentToolID = evt.Item.CallID
			if state.CurrentToolID == "" {
				state.CurrentToolID = evt.Item.ID
			}
			state.CurrentToolName = evt.Item.Name
			state.ToolArguments = ""

			if err := writeSSE(w, "content_block_start", map[string]any{
				"type":  "content_block_start",
				"index": state.ToolIndex,
				"content_block": map[string]any{
					"type":  "tool_use",
					"id":    state.CurrentToolID,
					"name":  state.CurrentToolName,
					"input": map[string]any{},
				},
			}); err != nil {
				return false, err
			}
			flusher.Flush()
		}

	case "response.function_call_arguments.delta":
		if state.ToolBlockStarted && evt.Delta != "" {
			state.ToolArguments += evt.Delta
			if err := writeSSE(w, "content_block_delta", map[string]any{
				"type":  "content_block_delta",
				"index": state.ToolIndex,
				"delta": map[string]any{
					"type":         "input_json_delta",
					"partial_json": evt.Delta,
				},
			}); err != nil {
				return false, err
			}
			flusher.Flush()
		}

	case "response.output_item.done":
		if evt.Item != nil && evt.Item.Type == "function_call" && state.ToolBlockStarted {
			if err := writeSSE(w, "content_block_stop", map[string]any{
				"type":  "content_block_stop",
				"index": state.ToolIndex,
			}); err != nil {
				return false, err
			}
			flusher.Flush()
			state.ToolBlockStarted = false
		}

	case "response.completed":
		if evt.Response != nil {
			state.OutputTokens = evt.Response.Usage.OutputTokens
			if evt.Response.Usage.InputTokens > 0 {
				state.InputTokens = evt.Response.Usage.InputTokens
			}
		}

		stopReason := "end_turn"
		if state.ToolBlockStarted || state.CurrentToolID != "" {
			stopReason = "tool_use"
		}
		finalizeStream(w, flusher, state, stopReason)
		return true, nil

	case "error":
		// Handle error events
		var errResp struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(payload), &errResp); err == nil && errResp.Error.Message != "" {
			return false, fmt.Errorf("upstream error: %s", errResp.Error.Message)
		}
	}

	return false, nil
}

func finalizeStream(w http.ResponseWriter, flusher http.Flusher, state *streamState, stopReason string) error {
	// Close any open blocks
	if state.TextBlockStarted {
		if err := writeSSE(w, "content_block_stop", map[string]any{
			"type":  "content_block_stop",
			"index": state.TextIndex,
		}); err != nil {
			return err
		}
		flusher.Flush()
	}

	if state.ToolBlockStarted {
		if err := writeSSE(w, "content_block_stop", map[string]any{
			"type":  "content_block_stop",
			"index": state.ToolIndex,
		}); err != nil {
			return err
		}
		flusher.Flush()
	}

	outputTokens := state.OutputTokens
	if outputTokens == 0 {
		outputTokens = estimateTextTokens(state.OutputText.String())
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