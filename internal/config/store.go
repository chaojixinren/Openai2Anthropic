package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

type Store struct {
	path   string
	mu     sync.RWMutex
	config Config
}

func NewStore(path string) (*Store, error) {
	store := &Store{path: path, config: Default()}
	if err := store.load(); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *Store) Get() Config {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.config.Clone()
}

func (s *Store) Update(next Config) (Config, error) {
	next.Normalize()
	if err := next.Validate(); err != nil {
		return Config{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.config = next.Clone()
	if err := s.persistLocked(); err != nil {
		return Config{}, err
	}
	return s.config.Clone(), nil
}

func (s *Store) load() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	raw, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return s.persistLocked()
		}
		return err
	}

	var current Config
	if err := json.Unmarshal(raw, &current); err != nil {
		return err
	}
	current.Normalize()
	s.config = current
	return nil
}

func (s *Store) persistLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}

	payload, err := json.MarshalIndent(s.config, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, payload, 0o644)
}

