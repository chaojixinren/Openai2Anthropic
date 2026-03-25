package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"
)

const (
	StrategyRoundRobin = "round_robin"
	StrategyFailover   = "failover"
)

type Upstream struct {
	Name    string `json:"name"`
	BaseURL string `json:"baseUrl"`
	APIKey  string `json:"apiKey"`
	Enabled bool   `json:"enabled"`
}

type Config struct {
	Bind                  string     `json:"bind"`
	AccessKey             string     `json:"accessKey"`
	Strategy              string     `json:"strategy"`
	RequestTimeoutSeconds int        `json:"requestTimeoutSeconds"`
	Upstreams             []Upstream `json:"upstreams"`
	UpdatedAt             time.Time  `json:"updatedAt"`
}

func Default() Config {
	return Config{
		Bind:                  "127.0.0.1:3144",
		AccessKey:             "change-me",
		Strategy:              StrategyRoundRobin,
		RequestTimeoutSeconds: 180,
		Upstreams:             []Upstream{},
		UpdatedAt:             time.Now().UTC(),
	}
}

func (c Config) Clone() Config {
	cloned := c
	cloned.Upstreams = append([]Upstream(nil), c.Upstreams...)
	return cloned
}

func (c *Config) Normalize() {
	if strings.TrimSpace(c.Bind) == "" {
		c.Bind = "127.0.0.1:3144"
	}
	if strings.TrimSpace(c.AccessKey) == "" {
		c.AccessKey = "change-me"
	}
	switch strings.TrimSpace(c.Strategy) {
	case StrategyFailover:
		c.Strategy = StrategyFailover
	default:
		c.Strategy = StrategyRoundRobin
	}
	if c.RequestTimeoutSeconds <= 0 {
		c.RequestTimeoutSeconds = 180
	}

	normalized := make([]Upstream, 0, len(c.Upstreams))
	for idx, upstream := range c.Upstreams {
		upstream.Name = strings.TrimSpace(upstream.Name)
		if upstream.Name == "" {
			upstream.Name = fmt.Sprintf("upstream-%d", idx+1)
		}
		upstream.BaseURL = normalizeBaseURL(upstream.BaseURL)
		upstream.APIKey = strings.TrimSpace(upstream.APIKey)
		if !upstream.Enabled {
			upstream.Enabled = upstream.BaseURL != "" && upstream.APIKey != ""
		}
		normalized = append(normalized, upstream)
	}
	c.Upstreams = normalized
	c.UpdatedAt = time.Now().UTC()
}

func (c Config) Validate() error {
	if _, _, err := net.SplitHostPort(c.Bind); err != nil {
		return fmt.Errorf("bind must be in host:port format: %w", err)
	}
	if strings.TrimSpace(c.AccessKey) == "" {
		return errors.New("accessKey is required")
	}
	if len(c.Upstreams) == 0 {
		return errors.New("at least one upstream is required")
	}

	seen := map[string]struct{}{}
	enabledCount := 0
	for _, upstream := range c.Upstreams {
		if upstream.Name == "" {
			return errors.New("upstream name is required")
		}
		if _, ok := seen[upstream.Name]; ok {
			return fmt.Errorf("duplicate upstream name: %s", upstream.Name)
		}
		seen[upstream.Name] = struct{}{}
		if !upstream.Enabled {
			continue
		}
		enabledCount++
		if upstream.BaseURL == "" {
			return fmt.Errorf("upstream %s baseUrl is required", upstream.Name)
		}
		parsed, err := url.Parse(upstream.BaseURL)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" {
			return fmt.Errorf("upstream %s baseUrl is invalid", upstream.Name)
		}
		if upstream.APIKey == "" {
			return fmt.Errorf("upstream %s apiKey is required", upstream.Name)
		}
	}
	if enabledCount == 0 {
		return errors.New("at least one enabled upstream is required")
	}
	return nil
}

func (c Config) EnabledUpstreams() []Upstream {
	items := make([]Upstream, 0, len(c.Upstreams))
	for _, upstream := range c.Upstreams {
		if upstream.Enabled {
			items = append(items, upstream)
		}
	}
	return items
}

func normalizeBaseURL(value string) string {
	trimmed := strings.TrimSpace(value)
	return strings.TrimRight(trimmed, "/")
}

