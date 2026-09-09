// Package snapshot holds one fetch of TicketButler data, everything derived from it,
// and the logic for refreshing and reading it.
package snapshot

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/cloudnativedenmark/ticketbutler-proxy/internal/aggregate"
	"github.com/cloudnativedenmark/ticketbutler-proxy/internal/ticketbutler"
)

// Snapshot is the result of one refresh: the raw orders, plus everything computed
// from them, so a read never has to recompute anything.
type Snapshot struct {
	FetchedAt        time.Time `json:"fetched_at"`
	SourceDurationMS int64     `json:"source_duration_ms"`

	Summary  aggregate.Summary           `json:"summary"`
	Sponsors []aggregate.Sponsor         `json:"sponsors"`
	Orders   ticketbutler.OrdersResponse `json:"orders"`
}

// Build derives a snapshot from one fetch.
func Build(result ticketbutler.Result, at time.Time, opts aggregate.Options) *Snapshot {
	return &Snapshot{
		FetchedAt:        at.UTC(),
		SourceDurationMS: result.Duration.Milliseconds(),
		Summary:          aggregate.Summarise(result.Orders, opts),
		Sponsors:         aggregate.Sponsors(result.Orders, opts),
		Orders:           result.Orders,
	}
}

// Age is how long ago the snapshot was fetched.
func (s *Snapshot) Age(now time.Time) time.Duration { return now.Sub(s.FetchedAt) }

// Stale reports whether the snapshot is older than the configured limit.
//
// A stale snapshot is still served. The alternative is failing a spreadsheet refresh
// outright, and a number with a warning attached is more useful to the organisers
// than no number at all.
func (s *Snapshot) Stale(now time.Time, staleAfter time.Duration) bool {
	return s.Age(now) > staleAfter
}

// Encode serialises the snapshot as gzipped JSON.
//
// Gzip matters more than it looks: the upstream payload repeats the entire event
// question schema on every single ticket, so it compresses by roughly an order of
// magnitude, and both the stored object and the response body shrink with it.
func (s *Snapshot) Encode() ([]byte, error) {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if err := json.NewEncoder(zw).Encode(s); err != nil {
		_ = zw.Close()
		return nil, fmt.Errorf("encoding snapshot: %w", err)
	}
	if err := zw.Close(); err != nil {
		return nil, fmt.Errorf("compressing snapshot: %w", err)
	}
	return buf.Bytes(), nil
}

// Decode parses a snapshot previously written by Encode.
func Decode(data []byte) (*Snapshot, error) {
	zr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("reading snapshot: %w", err)
	}
	defer zr.Close()

	// Bound the decompressed size so a corrupt or hostile object cannot exhaust
	// memory. The real payload is a few megabytes; 256 MB is far above any plausible
	// conference and far below anything that would take the instance down.
	const maxDecompressed = 256 << 20
	var out Snapshot
	if err := json.NewDecoder(io.LimitReader(zr, maxDecompressed)).Decode(&out); err != nil {
		return nil, fmt.Errorf("decoding snapshot: %w", err)
	}
	return &out, nil
}
