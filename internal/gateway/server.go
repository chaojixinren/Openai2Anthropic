package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/LangQi99/Openai2Anthropic/internal/config"
)

type upstreamState struct {
	Failures       int
	UnhealthyUntil time.Time
}

type modelCacheEntry struct {
	Models    []string
	ExpiresAt time.Time
}

type Server struct {
	store *config.Store

	httpServer *http.Server
	client     *http.Client
	rr         atomic.Uint64

	mu            sync.Mutex
	upstreamState map[string]*upstreamState
	modelCache    map[string]modelCacheEntry
}

func NewServer(store *config.Store) *Server {
	transport := &http.Transport{
		MaxIdleConns:          64,
		MaxIdleConnsPerHost:   16,
		IdleConnTimeout:       90 * time.Second,
		ResponseHeaderTimeout: 90 * time.Second,
	}

	return &Server{
		store:         store,
		client:        &http.Client{Transport: transport},
		upstreamState: map[string]*upstreamState{},
		modelCache:    map[string]modelCacheEntry{},
	}
}

func (s *Server) ListenAndServe() error {
	cfg := s.store.Get()

	mux := http.NewServeMux()
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/api/config", s.handleConfig)
	mux.HandleFunc("/api/models", s.handleAdminModels)
	mux.HandleFunc("/v1/messages", s.handleMessages)
	mux.HandleFunc("/v1/messages/count_tokens", s.handleCountTokens)
	mux.HandleFunc("/v1/models", s.handleAnthropicModels)

	s.httpServer = &http.Server{
		Addr:              cfg.Bind,
		Handler:           s.withCORS(mux),
		ReadHeaderTimeout: 10 * time.Second,
	}

	log.Printf("listening on http://%s", cfg.Bind)
	return s.httpServer.ListenAndServe()
}

func (s *Server) withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, X-Api-Key")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAnthropicError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	cfg := s.store.Get()
	summaries := make([]map[string]any, 0, len(cfg.Upstreams))
	for _, upstream := range cfg.Upstreams {
		summaries = append(summaries, map[string]any{
			"name":    upstream.Name,
			"baseUrl": upstream.BaseURL,
			"enabled": upstream.Enabled,
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status":    "ok",
		"bind":      cfg.Bind,
		"strategy":  cfg.Strategy,
		"upstreams": summaries,
		"updatedAt": cfg.UpdatedAt,
	})
}

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, http.StatusOK, s.store.Get())
	case http.MethodPut:
		var next config.Config
		if err := json.NewDecoder(r.Body).Decode(&next); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"message": "invalid config payload"})
			return
		}
		previous := s.store.Get()
		current, err := s.store.Update(next)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"message": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"config":          current,
			"restartRequired": previous.Bind != current.Bind,
		})
	default:
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"message": "method not allowed"})
	}
}

func (s *Server) handleAdminModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"message": "method not allowed"})
		return
	}

	cfg := s.store.Get()
	result := make([]map[string]any, 0, len(cfg.EnabledUpstreams()))
	for _, upstream := range cfg.EnabledUpstreams() {
		models, err := s.fetchUpstreamModels(r.Context(), upstream)
		row := map[string]any{
			"name":    upstream.Name,
			"baseUrl": upstream.BaseURL,
			"models":  models,
		}
		if err != nil {
			row["error"] = err.Error()
		}
		result = append(result, row)
	}

	writeJSON(w, http.StatusOK, map[string]any{"items": result})
}

func (s *Server) handleAnthropicModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAnthropicError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !s.authorizeRequest(r) {
		writeAnthropicError(w, http.StatusUnauthorized, "invalid proxy key")
		return
	}

	cfg := s.store.Get()
	seen := map[string]struct{}{}
	ids := make([]string, 0)

	for _, upstream := range cfg.EnabledUpstreams() {
		models, err := s.fetchUpstreamModels(r.Context(), upstream)
		if err != nil {
			continue
		}
		for _, model := range models {
			if _, ok := seen[model]; ok {
				continue
			}
			seen[model] = struct{}{}
			ids = append(ids, model)
		}
	}

	items := make([]map[string]any, 0, len(ids))
	for _, id := range ids {
		items = append(items, map[string]any{
			"type":         "model",
			"id":           id,
			"display_name": id,
		})
	}

	response := map[string]any{
		"data":     items,
		"first_id": "",
		"last_id":  "",
		"has_more": false,
	}
	if len(ids) > 0 {
		response["first_id"] = ids[0]
		response["last_id"] = ids[len(ids)-1]
	}

	writeJSON(w, http.StatusOK, response)
}

func (s *Server) handleCountTokens(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAnthropicError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !s.authorizeRequest(r) {
		writeAnthropicError(w, http.StatusUnauthorized, "invalid proxy key")
		return
	}

	var req anthropicRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeAnthropicError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"input_tokens": estimateAnthropicInputTokens(req),
	})
}

func (s *Server) handleMessages(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAnthropicError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !s.authorizeRequest(r) {
		writeAnthropicError(w, http.StatusUnauthorized, "invalid proxy key")
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeAnthropicError(w, http.StatusBadRequest, "failed to read request body")
		return
	}

	var req anthropicRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeAnthropicError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(req.Messages) == 0 {
		writeAnthropicError(w, http.StatusBadRequest, "messages are required")
		return
	}

	cfg := s.store.Get()
	upstreams := s.orderedUpstreams(cfg)
	if len(upstreams) == 0 {
		writeAnthropicError(w, http.StatusBadGateway, "no enabled upstreams available")
		return
	}

	var lastStatus int
	var lastMessage string
	for _, upstream := range upstreams {
		models, _ := s.fetchUpstreamModels(r.Context(), upstream)
		selectedModel := resolveRequestedModel(req.Model, models)
		payload, err := buildOpenAIRequest(req, selectedModel)
		if err != nil {
			writeAnthropicError(w, http.StatusBadRequest, err.Error())
			return
		}

		raw, err := json.Marshal(payload)
		if err != nil {
			writeAnthropicError(w, http.StatusInternalServerError, "failed to encode upstream request")
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), time.Duration(cfg.RequestTimeoutSeconds)*time.Second)
		upstreamReq, err := http.NewRequestWithContext(ctx, http.MethodPost, upstreamURL(upstream.BaseURL, "/v1/chat/completions"), bytes.NewReader(raw))
		if err != nil {
			cancel()
			writeAnthropicError(w, http.StatusInternalServerError, "failed to build upstream request")
			return
		}

		upstreamReq.Header.Set("Authorization", "Bearer "+upstream.APIKey)
		upstreamReq.Header.Set("Content-Type", "application/json")
		upstreamReq.Header.Set("Accept", "application/json")
		if req.Stream {
			upstreamReq.Header.Set("Accept", "text/event-stream")
		}

		resp, doErr := s.client.Do(upstreamReq)
		if doErr != nil {
			cancel()
			s.noteFailure(upstream.Name)
			lastStatus = http.StatusBadGateway
			lastMessage = doErr.Error()
			continue
		}

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			rawResp, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			cancel()

			lastStatus = statusForUpstreamFailure(resp.StatusCode)
			lastMessage = extractUpstreamError(rawResp)
			if shouldRetryStatus(resp.StatusCode) {
				s.noteFailure(upstream.Name)
				continue
			}
			writeAnthropicError(w, lastStatus, lastMessage)
			return
		}

		s.noteSuccess(upstream.Name)
		w.Header().Set("X-Proxy-Upstream", upstream.Name)

		if req.Stream {
			streamErr := s.streamAnthropicResponse(w, resp, req.Model, estimateAnthropicInputTokens(req))
			cancel()
			if streamErr != nil && errors.Is(streamErr, context.Canceled) {
				return
			}
			if streamErr != nil {
				writeAnthropicError(w, http.StatusBadGateway, streamErr.Error())
			}
			return
		}

		rawResp, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		cancel()
		if readErr != nil {
			lastStatus = http.StatusBadGateway
			lastMessage = readErr.Error()
			s.noteFailure(upstream.Name)
			continue
		}

		transformed, transformErr := transformNonStreamingResponse(rawResp, req.Model)
		if transformErr != nil {
			lastStatus = http.StatusBadGateway
			lastMessage = transformErr.Error()
			s.noteFailure(upstream.Name)
			continue
		}

		writeJSON(w, http.StatusOK, transformed)
		return
	}

	if lastStatus == 0 {
		lastStatus = http.StatusBadGateway
	}
	if lastMessage == "" {
		lastMessage = "all upstreams failed"
	}
	writeAnthropicError(w, lastStatus, lastMessage)
}

func (s *Server) authorizeRequest(r *http.Request) bool {
	cfg := s.store.Get()
	expected := strings.TrimSpace(cfg.AccessKey)
	if expected == "" {
		return true
	}

	provided := strings.TrimSpace(r.Header.Get("x-api-key"))
	if provided == "" {
		provided = normalizeIncomingBearer(r.Header.Get("Authorization"))
	}
	return provided == expected
}

func (s *Server) orderedUpstreams(cfg config.Config) []config.Upstream {
	enabled := cfg.EnabledUpstreams()
	if len(enabled) == 0 {
		return nil
	}

	now := time.Now()
	healthy := make([]config.Upstream, 0, len(enabled))
	delayed := make([]config.Upstream, 0)

	s.mu.Lock()
	for _, upstream := range enabled {
		state := s.upstreamState[upstream.Name]
		if state != nil && state.UnhealthyUntil.After(now) {
			delayed = append(delayed, upstream)
			continue
		}
		healthy = append(healthy, upstream)
	}
	s.mu.Unlock()

	base := healthy
	if len(base) == 0 {
		base = enabled
	}

	if cfg.Strategy == config.StrategyRoundRobin && len(base) > 1 {
		start := int(s.rr.Add(1)-1) % len(base)
		base = append(append([]config.Upstream{}, base[start:]...), base[:start]...)
	}

	if len(delayed) == 0 || slices.Equal(base, enabled) {
		return base
	}
	return append(base, delayed...)
}

func (s *Server) noteFailure(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	state := s.upstreamState[name]
	if state == nil {
		state = &upstreamState{}
		s.upstreamState[name] = state
	}
	state.Failures++
	backoff := time.Duration(state.Failures*5) * time.Second
	if backoff > 30*time.Second {
		backoff = 30 * time.Second
	}
	state.UnhealthyUntil = time.Now().Add(backoff)
}

func (s *Server) noteSuccess(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	state := s.upstreamState[name]
	if state == nil {
		state = &upstreamState{}
		s.upstreamState[name] = state
	}
	state.Failures = 0
	state.UnhealthyUntil = time.Time{}
}

func (s *Server) fetchUpstreamModels(ctx context.Context, upstream config.Upstream) ([]string, error) {
	s.mu.Lock()
	if cached, ok := s.modelCache[upstream.Name]; ok && time.Now().Before(cached.ExpiresAt) {
		models := append([]string(nil), cached.Models...)
		s.mu.Unlock()
		return models, nil
	}
	s.mu.Unlock()

	reqCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, upstreamURL(upstream.BaseURL, "/v1/models"), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+upstream.APIKey)

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(resp.Body)
		return nil, errors.New(extractUpstreamError(raw))
	}

	var envelope openAIModelsEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return nil, err
	}

	models := make([]string, 0, len(envelope.Data))
	for _, item := range envelope.Data {
		if strings.TrimSpace(item.ID) != "" {
			models = append(models, item.ID)
		}
	}

	s.mu.Lock()
	s.modelCache[upstream.Name] = modelCacheEntry{
		Models:    append([]string(nil), models...),
		ExpiresAt: time.Now().Add(5 * time.Minute),
	}
	s.mu.Unlock()

	return models, nil
}

func shouldRetryStatus(status int) bool {
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusTooManyRequests, http.StatusRequestTimeout,
		http.StatusConflict, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	default:
		return status >= 500
	}
}

func statusForUpstreamFailure(status int) int {
	switch {
	case status == http.StatusTooManyRequests:
		return http.StatusTooManyRequests
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return http.StatusBadGateway
	case status >= 400 && status < 500:
		return http.StatusBadRequest
	default:
		return http.StatusBadGateway
	}
}
