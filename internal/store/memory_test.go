package store_test

import (
	"context"
	"errors"
	"testing"

	"github.com/cloudnativedenmark/ticketbutler-proxy/internal/store"
)

func TestMemoryStore(t *testing.T) {
	ctx := context.Background()
	m := store.NewMemory()

	if _, err := m.Load(ctx); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("Load on an empty store = %v, want ErrNotFound", err)
	}

	want := []byte("snapshot bytes")
	if err := m.Save(ctx, want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := m.Load(ctx)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if string(got) != string(want) {
		t.Errorf("Load = %q, want %q", got, want)
	}

	// The store must hand back a copy, not a window onto its own buffer: a caller
	// mutating what it read would otherwise corrupt the stored snapshot.
	got[0] = 'X'
	again, err := m.Load(ctx)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if string(again) != string(want) {
		t.Errorf("Load = %q after the caller mutated an earlier result, want %q", again, want)
	}

	if err := m.Save(ctx, []byte("replacement")); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if got, _ := m.Load(ctx); string(got) != "replacement" {
		t.Errorf("Load = %q, want the replacement", got)
	}
	if m.Describe() == "" {
		t.Error("Describe should name the backing location for logs")
	}
}
