package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"

	"gok-pi/internal/lib/atomicfile"
)

// PriceLimitsStore provides thread-safe access to price limit settings,
// persisted to a JSON file on disk.
type PriceLimitsStore struct {
	mu     sync.RWMutex
	limits PriceLimits
	path   string
}

// NewPriceLimitsStore creates a store backed by the given file path.
// If the file exists, it is loaded; otherwise defaults (0/0 = no limits) apply.
func NewPriceLimitsStore(path string) *PriceLimitsStore {
	s := &PriceLimitsStore{path: path}
	if path != "" {
		s.load()
	}
	return s
}

func (s *PriceLimitsStore) load() {
	data, err := os.ReadFile(s.path)
	if err != nil {
		return // file doesn't exist yet — use zero defaults
	}
	var limits PriceLimits
	if err := json.Unmarshal(data, &limits); err != nil {
		return
	}
	s.limits = limits
}

// Get returns the current price limits.
func (s *PriceLimitsStore) Get() PriceLimits {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.limits
}

// Set updates the price limits and persists them to disk.
func (s *PriceLimitsStore) Set(limits PriceLimits) error {
	s.mu.Lock()
	s.limits = limits
	s.mu.Unlock()

	if s.path == "" {
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}

	data, err := json.MarshalIndent(limits, "", "  ")
	if err != nil {
		return err
	}
	return atomicfile.Write(s.path, data, 0o644)
}
