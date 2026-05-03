package agentwatcher

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

type dedupeSnapshot struct {
	Values map[string]time.Time `json:"values"`
}

func NewPersistentDedupeStore(path string, ttl time.Duration, now time.Time) (*DedupeStore, error) {
	store := NewDedupeStore(ttl)
	store.path = path
	if path == "" {
		return store, nil
	}
	if err := store.load(now); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *DedupeStore) load(now time.Time) error {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var snapshot dedupeSnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		return err
	}
	for key, seenAt := range snapshot.Values {
		if seenAt.Add(s.ttl).After(now) {
			s.values[key] = seenAt
		}
	}
	return nil
}

func (s *DedupeStore) persistLocked(now time.Time) error {
	if s == nil || s.path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	values := make(map[string]time.Time, len(s.values))
	for key, seenAt := range s.values {
		if seenAt.Add(s.ttl).After(now) {
			values[key] = seenAt
		}
	}
	data, err := json.MarshalIndent(dedupeSnapshot{Values: values}, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}
