package store

import (
	"context"
	"sync"
)

// Memory keeps the snapshot in process memory.
//
// Useful for local development and tests. On Cloud Run it is a poor choice: the
// service scales to zero, and the snapshot dies with the instance, so the first read
// after an idle period would have nothing to serve.
type Memory struct {
	mu   sync.RWMutex
	data []byte
}

// NewMemory returns an empty in-memory store.
func NewMemory() *Memory { return &Memory{} }

// Load returns the stored bytes, or ErrNotFound if Save has not been called.
func (m *Memory) Load(context.Context) ([]byte, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.data == nil {
		return nil, ErrNotFound
	}
	out := make([]byte, len(m.data))
	copy(out, m.data)
	return out, nil
}

// Save replaces the stored bytes.
func (m *Memory) Save(_ context.Context, data []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data = make([]byte, len(data))
	copy(m.data, data)
	return nil
}

// Describe names the backing location.
func (m *Memory) Describe() string { return "memory" }
