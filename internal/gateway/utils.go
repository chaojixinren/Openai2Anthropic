package gateway

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeAnthropicError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]any{
		"type": "error",
		"error": map[string]any{
			"type":    "api_error",
			"message": message,
		},
	})
}

func normalizeIncomingBearer(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(trimmed, "Bearer "))
}

func randomID(prefix string) string {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("%s_%d", prefix, time.Now().UnixNano())
	}
	return fmt.Sprintf("%s_%s", prefix, hex.EncodeToString(buf))
}

func estimateTextTokens(value string) int {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return 0
	}
	runes := len([]rune(trimmed))
	tokens := runes / 4
	if runes%4 != 0 {
		tokens++
	}
	if tokens == 0 {
		tokens = 1
	}
	return tokens
}

func upstreamURL(baseURL, route string) string {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return strings.TrimRight(baseURL, "/") + route
	}

	path := strings.TrimRight(parsed.Path, "/")
	switch {
	case path == "":
		parsed.Path = route
	case strings.HasSuffix(path, "/v1") && strings.HasPrefix(route, "/v1/"):
		parsed.Path = path + strings.TrimPrefix(route, "/v1")
	default:
		parsed.Path = path + route
	}
	return parsed.String()
}

func extractUpstreamError(raw []byte) string {
	var envelope openAIChatResponse
	if err := json.Unmarshal(raw, &envelope); err == nil && envelope.Error != nil && envelope.Error.Message != "" {
		return envelope.Error.Message
	}

	var fallback map[string]any
	if err := json.Unmarshal(raw, &fallback); err != nil {
		return strings.TrimSpace(string(raw))
	}

	if errValue, ok := fallback["error"].(map[string]any); ok {
		if message, ok := errValue["message"].(string); ok && strings.TrimSpace(message) != "" {
			return message
		}
	}
	if message, ok := fallback["message"].(string); ok {
		return strings.TrimSpace(message)
	}
	return strings.TrimSpace(string(raw))
}

func mapFinishReason(value string) string {
	switch value {
	case "tool_calls":
		return "tool_use"
	case "length":
		return "max_tokens"
	default:
		return "end_turn"
	}
}
