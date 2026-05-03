package agentwatcher

import (
	"path/filepath"
	"testing"
	"time"
)

func TestPersistentDedupeStoreRecoversAcrossRestart(t *testing.T) {
	now := time.Date(2026, 5, 3, 21, 0, 0, 0, time.UTC)
	path := filepath.Join(t.TempDir(), "dedupe.json")
	store, err := NewPersistentDedupeStore(path, time.Hour, now)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	if !store.Seen("delivery-1", now) {
		t.Fatal("expected first delivery to be accepted")
	}

	restarted, err := NewPersistentDedupeStore(path, time.Hour, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("restart store: %v", err)
	}
	if restarted.Seen("delivery-1", now.Add(time.Minute)) {
		t.Fatal("expected restarted store to reject duplicate delivery")
	}
}

func TestPersistentDedupeStoreDropsExpiredEntries(t *testing.T) {
	now := time.Date(2026, 5, 3, 21, 0, 0, 0, time.UTC)
	path := filepath.Join(t.TempDir(), "dedupe.json")
	store, err := NewPersistentDedupeStore(path, time.Minute, now)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	if !store.Seen("delivery-1", now) {
		t.Fatal("expected first delivery to be accepted")
	}

	restarted, err := NewPersistentDedupeStore(path, time.Minute, now.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("restart store: %v", err)
	}
	if !restarted.Seen("delivery-1", now.Add(2*time.Minute)) {
		t.Fatal("expected expired delivery to be accepted again")
	}
}
