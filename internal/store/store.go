// Package store persists the snapshot bytes the service serves.
//
// It deals in opaque bytes so that nothing about the snapshot's shape leaks into the
// storage layer; encoding lives in the snapshot package.
package store

import (
	"context"
	"errors"
)

// ErrNotFound reports that nothing has been stored yet. Callers treat it as "no
// snapshot to serve", which is a normal state before the first refresh — not a
// failure.
var ErrNotFound = errors.New("no snapshot stored")

// Store reads and writes the current snapshot.
type Store interface {
	// Load returns the stored bytes, or ErrNotFound.
	Load(ctx context.Context) ([]byte, error)

	// Save replaces the stored bytes.
	Save(ctx context.Context, data []byte) error

	// Describe names the backing location, for logs and /healthz.
	Describe() string
}
