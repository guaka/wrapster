package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestCachePutGetValid(t *testing.T) {
	cache, err := Open(filepath.Join(t.TempDir(), "cache.db"))
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer cache.Close()

	now := time.Unix(1700000000, 0)
	if err := cache.Put(context.Background(), "pubkey", "alice", now, now.Add(time.Hour)); err != nil {
		t.Fatalf("Put returned error: %v", err)
	}

	entry, ok, err := cache.GetValid(context.Background(), "pubkey", now)
	if err != nil {
		t.Fatalf("GetValid returned error: %v", err)
	}
	if !ok {
		t.Fatal("expected cache hit")
	}
	if entry.Username != "alice" {
		t.Fatalf("expected alice, got %s", entry.Username)
	}
}

func TestCacheExpired(t *testing.T) {
	cache, err := Open(filepath.Join(t.TempDir(), "cache.db"))
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer cache.Close()

	now := time.Unix(1700000000, 0)
	if err := cache.Put(context.Background(), "pubkey", "alice", now.Add(-2*time.Hour), now.Add(-time.Hour)); err != nil {
		t.Fatalf("Put returned error: %v", err)
	}

	_, ok, err := cache.GetValid(context.Background(), "pubkey", now)
	if err != nil {
		t.Fatalf("GetValid returned error: %v", err)
	}
	if ok {
		t.Fatal("expected expired cache miss")
	}
}

func TestCacheSummary(t *testing.T) {
	cache, err := Open(filepath.Join(t.TempDir(), "cache.db"))
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer cache.Close()

	now := time.Unix(1700000000, 0)
	if err := cache.Put(context.Background(), "pubkey-1", "alice", now.Add(-2*time.Hour), now.Add(time.Hour)); err != nil {
		t.Fatalf("Put returned error: %v", err)
	}
	if err := cache.Put(context.Background(), "pubkey-2", "bob", now.Add(-time.Hour), now.Add(-time.Minute)); err != nil {
		t.Fatalf("Put returned error: %v", err)
	}

	summary, err := cache.Summary(context.Background(), now)
	if err != nil {
		t.Fatalf("Summary returned error: %v", err)
	}
	if summary.Total != 2 || summary.Valid != 1 || summary.Expired != 1 {
		t.Fatalf("unexpected summary: %+v", summary)
	}
	if summary.OldestUnix != now.Add(-2*time.Hour).Unix() {
		t.Fatalf("unexpected oldest timestamp: %d", summary.OldestUnix)
	}
	if summary.NewestUnix != now.Add(-time.Hour).Unix() {
		t.Fatalf("unexpected newest timestamp: %d", summary.NewestUnix)
	}
}
