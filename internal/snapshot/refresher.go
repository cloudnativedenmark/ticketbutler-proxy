package snapshot

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/cloudnativedenmark/ticketbutler-proxy/internal/aggregate"
	"github.com/cloudnativedenmark/ticketbutler-proxy/internal/store"
	"github.com/cloudnativedenmark/ticketbutler-proxy/internal/ticketbutler"
)

// Fetcher is the part of the TicketButler client the refresher needs, narrowed to an
// interface so tests can supply a payload without an HTTP server.
type Fetcher interface {
	FetchOrders(ctx context.Context) (ticketbutler.Result, error)
}

// Refresher owns the current snapshot: it refreshes it from upstream on demand and
// hands out the latest one for reads.
//
// Reads never trigger a fetch. That is the whole design constraint — the caller is
// Google Apps Script, which gives up after 60 seconds, and the upstream fetch now
// takes longer than that. Refreshes come from Cloud Scheduler instead.
type Refresher struct {
	Fetcher Fetcher
	Store   store.Store
	Options aggregate.Options
	Log     *slog.Logger

	// Now exists so tests can control snapshot ages.
	Now func() time.Time

	// refreshMu serialises refreshes so two overlapping triggers cannot both fetch
	// and then race to write. Cloud Run is configured for a single instance, which
	// makes this mutex sufficient rather than merely helpful.
	refreshMu sync.Mutex

	// cacheMu guards the in-process copy of the last known snapshot, which spares
	// every read a round trip to Cloud Storage.
	cacheMu sync.RWMutex
	cached  *Snapshot
}

// Refresh fetches from upstream, recomputes everything and stores the result.
//
// Concurrent callers are serialised, and the second one performs its own fetch
// rather than sharing the first one's — a refresh is meant to produce fresh data, so
// coalescing would quietly hand back something older than asked for.
func (r *Refresher) Refresh(ctx context.Context) (*Snapshot, error) {
	r.refreshMu.Lock()
	defer r.refreshMu.Unlock()

	result, err := r.Fetcher.FetchOrders(ctx)
	if err != nil {
		return nil, err
	}

	snap := Build(result, r.now(), r.Options)
	data, err := snap.Encode()
	if err != nil {
		return nil, err
	}
	if err := r.Store.Save(ctx, data); err != nil {
		// The fetch succeeded, so serve what we have rather than discarding it. It
		// will be lost when the instance goes away, and the error says so.
		r.setCached(snap)
		return snap, err
	}

	r.setCached(snap)
	r.log().Info("refreshed snapshot",
		"orders", snap.Summary.Counts.Orders,
		"tickets", snap.Summary.Counts.Tickets,
		"source_duration_ms", snap.SourceDurationMS,
		"stored_bytes", len(data),
		"store", r.Store.Describe())
	return snap, nil
}

// Current returns the latest snapshot, preferring the in-process copy and falling
// back to the store. It returns store.ErrNotFound when no refresh has happened yet.
func (r *Refresher) Current(ctx context.Context) (*Snapshot, error) {
	r.cacheMu.RLock()
	cached := r.cached
	r.cacheMu.RUnlock()
	if cached != nil {
		return cached, nil
	}

	data, err := r.Store.Load(ctx)
	if err != nil {
		return nil, err
	}
	snap, err := Decode(data)
	if err != nil {
		return nil, err
	}
	r.setCached(snap)
	return snap, nil
}

// Warm loads the stored snapshot into memory at start-up, so the first request after
// a cold start does not pay for the round trip. A missing snapshot is not an error:
// the service starts, reports itself unready to serve data, and waits for a refresh.
func (r *Refresher) Warm(ctx context.Context) {
	snap, err := r.Current(ctx)
	switch {
	case errors.Is(err, store.ErrNotFound):
		r.log().Warn("no snapshot stored yet; waiting for the first refresh", "store", r.Store.Describe())
	case err != nil:
		r.log().Error("loading the stored snapshot failed", "error", err, "store", r.Store.Describe())
	default:
		r.log().Info("loaded stored snapshot",
			"fetched_at", snap.FetchedAt, "age", r.now().Sub(snap.FetchedAt).Round(time.Second))
	}
}

func (r *Refresher) setCached(s *Snapshot) {
	r.cacheMu.Lock()
	r.cached = s
	r.cacheMu.Unlock()
}

func (r *Refresher) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func (r *Refresher) log() *slog.Logger {
	if r.Log != nil {
		return r.Log
	}
	return slog.Default()
}
