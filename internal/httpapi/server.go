// Package httpapi serves the snapshot to Google Apps Script.
//
// Every read is served from the last stored snapshot. Nothing here calls
// TicketButler, because the caller is Apps Script and it abandons a request after 60
// seconds; refreshing is Cloud Scheduler's job, via POST /v1/refresh.
package httpapi

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/cloudnativedenmark/ticketbutler-proxy/internal/config"
	"github.com/cloudnativedenmark/ticketbutler-proxy/internal/snapshot"
)

// Server holds everything the handlers need.
type Server struct {
	refresher  *snapshot.Refresher
	auth       *authenticator
	staleAfter time.Duration
	log        *slog.Logger
	now        func() time.Time
}

// New builds the server. now may be nil, in which case time.Now is used.
func New(cfg config.Config, refresher *snapshot.Refresher, log *slog.Logger, now func() time.Time) *Server {
	if log == nil {
		log = slog.Default()
	}
	if now == nil {
		now = time.Now
	}
	return &Server{
		refresher:  refresher,
		auth:       newAuthenticator(cfg.APITokens),
		staleAfter: cfg.StaleAfter,
		log:        log,
		now:        now,
	}
}

// Handler returns the routed, logged handler for the whole API.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// Unauthenticated: it carries no data, and Cloud Run's own health checking has
	// no token to present.
	mux.HandleFunc("GET /healthz", s.handleHealth)

	mux.Handle("GET /v1/summary", s.auth.requireToken(http.HandlerFunc(s.handleSummary)))
	mux.Handle("GET /v1/sponsors", s.auth.requireToken(http.HandlerFunc(s.handleSponsors)))
	mux.Handle("GET /v1/orders", s.auth.requireToken(http.HandlerFunc(s.handleOrders)))
	mux.Handle("POST /v1/refresh", s.auth.requireToken(http.HandlerFunc(s.handleRefresh)))

	return s.logRequests(mux)
}

// logRequests logs one line per request. It deliberately records no header, query
// string or body: those carry the API token, and the responses carry attendee
// personal data.
func (s *Server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := s.now()
		recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(recorder, r)
		s.log.Info("request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", recorder.status,
			"duration_ms", s.now().Sub(started).Milliseconds())
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

// meta is the freshness information attached to every data response, so a caller can
// tell how old the numbers are without asking a second endpoint.
type meta struct {
	FetchedAt        time.Time `json:"fetched_at"`
	AgeSeconds       int64     `json:"age_seconds"`
	Stale            bool      `json:"stale"`
	SourceDurationMS int64     `json:"source_duration_ms"`
}

func (s *Server) metaFor(snap *snapshot.Snapshot) meta {
	now := s.now()
	return meta{
		FetchedAt:        snap.FetchedAt,
		AgeSeconds:       int64(snap.Age(now).Seconds()),
		Stale:            snap.Stale(now, s.staleAfter),
		SourceDurationMS: snap.SourceDurationMS,
	}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	// The response is already committed by this point, so a write failure is only
	// worth noting, not turning into a status code.
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
