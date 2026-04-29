package email

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"gok-pi/internal/lib/atomicfile"
)

// stateStore tracks the date a report was last successfully sent for each
// (agentID, report kind) pair, persisted to a single JSON file. This survives
// restarts so reports are not duplicated across crashes or redeploys.
type stateStore struct {
	path string

	mu      sync.Mutex
	entries map[string]string // key: "<agentID>|<kind>" → "YYYY-MM-DD"
}

func newStateStore(path string) (*stateStore, error) {
	s := &stateStore{
		path:    path,
		entries: make(map[string]string),
	}
	if path == "" {
		return s, nil
	}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *stateStore) load() error {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read email state: %w", err)
	}
	if len(data) == 0 {
		return nil
	}
	if err := json.Unmarshal(data, &s.entries); err != nil {
		return fmt.Errorf("decode email state: %w", err)
	}
	return nil
}

func (s *stateStore) lastSent(agentID string, kind ReportKind) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.entries[stateKey(agentID, kind)]
}

func (s *stateStore) markSent(agentID string, kind ReportKind, dateISO string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries[stateKey(agentID, kind)] = dateISO
	return s.persistLocked()
}

func (s *stateStore) persistLocked() error {
	if s.path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("ensure state dir: %w", err)
	}
	data, err := json.MarshalIndent(s.entries, "", "  ")
	if err != nil {
		return fmt.Errorf("encode email state: %w", err)
	}
	if err := atomicfile.Write(s.path, data, 0o644); err != nil {
		return fmt.Errorf("persist email state: %w", err)
	}
	return nil
}

func stateKey(agentID string, kind ReportKind) string {
	return agentID + "|" + string(kind)
}
