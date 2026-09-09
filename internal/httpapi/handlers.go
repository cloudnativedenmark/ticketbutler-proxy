package httpapi

import (
	"errors"
	"net/http"

	"github.com/cloudnativedenmark/ticketbutler-proxy/internal/aggregate"
	"github.com/cloudnativedenmark/ticketbutler-proxy/internal/snapshot"
	"github.com/cloudnativedenmark/ticketbutler-proxy/internal/store"
	"github.com/cloudnativedenmark/ticketbutler-proxy/internal/ticketbutler"
)

// handleHealth answers whether the process is up, and separately whether it has data
// to serve. A service that is running but has never refreshed is healthy and not yet
// ready, and conflating the two would make Cloud Run restart an instance that is
// only waiting for its first scheduled refresh.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	body := map[string]any{"status": "ok", "snapshot": "missing"}
	if snap, err := s.refresher.Current(r.Context()); err == nil {
		m := s.metaFor(snap)
		body["snapshot"] = "present"
		body["fetched_at"] = m.FetchedAt
		body["age_seconds"] = m.AgeSeconds
		body["stale"] = m.Stale
	}
	writeJSON(w, http.StatusOK, body)
}

// summaryResponse is the payload TicketSales.gs consumes.
type summaryResponse struct {
	meta
	aggregate.Summary
}

// handleSummary serves the pre-aggregated numbers for the REALIZED and
// MERCH AND T-SHIRTS sheets. Aggregates only, so no personal data.
func (s *Server) handleSummary(w http.ResponseWriter, r *http.Request) {
	snap, ok := s.snapshotOr503(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, summaryResponse{meta: s.metaFor(snap), Summary: snap.Summary})
}

type sponsorsResponse struct {
	meta
	Sponsors []aggregate.Sponsor `json:"sponsors"`
}

// handleSponsors serves the community sponsors for Sponsors.gs.
func (s *Server) handleSponsors(w http.ResponseWriter, r *http.Request) {
	snap, ok := s.snapshotOr503(w, r)
	if !ok {
		return
	}
	sponsors := snap.Sponsors
	if sponsors == nil {
		// An empty list is easier to consume than null.
		sponsors = []aggregate.Sponsor{}
	}
	noStore(w)
	writeJSON(w, http.StatusOK, sponsorsResponse{meta: s.metaFor(snap), Sponsors: sponsors})
}

type ordersResponse struct {
	meta
	Orders ticketbutler.OrdersResponse `json:"orders"`
}

// handleOrders serves the cached upstream payload unchanged, for scripts that need a
// field the summary does not carry. It contains every attendee's name, email address
// and employer, plus their free-text answers.
func (s *Server) handleOrders(w http.ResponseWriter, r *http.Request) {
	snap, ok := s.snapshotOr503(w, r)
	if !ok {
		return
	}
	noStore(w)
	writeJSON(w, http.StatusOK, ordersResponse{meta: s.metaFor(snap), Orders: snap.Orders})
}

// handleRefresh fetches from upstream and stores a new snapshot. Cloud Scheduler
// calls this; Apps Script must not, because the fetch is exactly the slow operation
// this service exists to keep off the Apps Script request path.
func (s *Server) handleRefresh(w http.ResponseWriter, r *http.Request) {
	snap, err := s.refresher.Refresh(r.Context())
	if err != nil {
		var upstream *ticketbutler.ErrUpstream
		if errors.As(err, &upstream) {
			s.log.Error("upstream rejected the refresh", "status", upstream.StatusCode, "error", err)
			// The upstream's own status is not this service's status: a rejected
			// TicketButler token is a configuration problem here.
			writeError(w, http.StatusBadGateway, "ticketbutler rejected the request; check TICKETBUTLER_TOKEN")
			return
		}
		if snap != nil {
			// The fetch worked and only storing failed, so the numbers are good but
			// will not survive this instance.
			s.log.Error("storing the refreshed snapshot failed", "error", err)
			writeJSON(w, http.StatusMultiStatus, map[string]any{
				"warning": "refreshed but not stored; the snapshot will be lost when this instance stops",
				"error":   err.Error(),
				"counts":  snap.Summary.Counts,
			})
			return
		}
		s.log.Error("refresh failed", "error", err)
		writeError(w, http.StatusBadGateway, "refreshing from ticketbutler failed: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"fetched_at":         snap.FetchedAt,
		"source_duration_ms": snap.SourceDurationMS,
		"counts":             snap.Summary.Counts,
		"orders_by_state":    snap.Summary.OrdersByState,
	})
}

// snapshotOr503 fetches the current snapshot, answering the request itself if there
// is none. A 503 with Retry-After is the honest answer before the first refresh: the
// service is fine, it just has nothing yet.
func (s *Server) snapshotOr503(w http.ResponseWriter, r *http.Request) (*snapshot.Snapshot, bool) {
	current, err := s.refresher.Current(r.Context())
	if errors.Is(err, store.ErrNotFound) {
		w.Header().Set("Retry-After", "60")
		writeError(w, http.StatusServiceUnavailable,
			"no snapshot yet; wait for the scheduled refresh or POST /v1/refresh")
		return nil, false
	}
	if err != nil {
		s.log.Error("reading the stored snapshot failed", "error", err)
		writeError(w, http.StatusInternalServerError, "reading the stored snapshot failed")
		return nil, false
	}
	return current, true
}

// noStore marks a response as uncacheable. Used on everything carrying attendee
// personal data, so it is not retained by a proxy or a browser cache.
func noStore(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
}
