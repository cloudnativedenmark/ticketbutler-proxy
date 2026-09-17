package httpapi_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/cloudnativedenmark/ticketbutler-proxy/internal/aggregate"
	"github.com/cloudnativedenmark/ticketbutler-proxy/internal/config"
	"github.com/cloudnativedenmark/ticketbutler-proxy/internal/httpapi"
	"github.com/cloudnativedenmark/ticketbutler-proxy/internal/snapshot"
	"github.com/cloudnativedenmark/ticketbutler-proxy/internal/store"
	"github.com/cloudnativedenmark/ticketbutler-proxy/internal/ticketbutler"
)

const token = "test-token-at-least-16-chars"

var fetchedAt = time.Date(2026, 9, 9, 10, 0, 0, 0, time.UTC)

type stubFetcher struct {
	result ticketbutler.Result
	err    error
}

func (s *stubFetcher) FetchOrders(context.Context) (ticketbutler.Result, error) {
	return s.result, s.err
}

type stubStore struct {
	data []byte
	err  error
}

func (s stubStore) Load(context.Context) ([]byte, error) { return s.data, s.err }
func (stubStore) Save(context.Context, []byte) error     { return nil }
func (stubStore) Describe() string                       { return "stub test store" }

func sampleResult() ticketbutler.Result {
	return ticketbutler.Result{
		Duration: 78 * time.Second,
		Orders: ticketbutler.OrdersResponse{
			UUID: "event-uuid",
			Orders: []ticketbutler.Order{{
				State:   "PAID",
				VATRate: 0.25,
				Tickets: []ticketbutler.Ticket{
					{TicketTypeName: "Standard", Price: 1000, PriceTotal: 1000, Email: "attendee@example.example"},
					{TicketTypePK: 183067, TicketTypeName: "Community Sponsor", CompanyName: "Alpha A/S", Email: "sponsor@example.example"},
				},
			}},
		},
	}
}

// newServer returns a handler whose clock is fixed at now, so staleness is testable
// without waiting, and reports the refresher so a test can seed or skip a snapshot.
func newServer(t *testing.T, now time.Time, fetcher snapshot.Fetcher) (http.Handler, *snapshot.Refresher) {
	t.Helper()
	cfg := config.Config{APITokens: []string{token}, StaleAfter: 30 * time.Minute}
	refresher := &snapshot.Refresher{
		Fetcher: fetcher,
		Store:   store.NewMemory(),
		Options: aggregate.Options{MerchNamePatterns: []string{"hoodie"}, SponsorTicketTypePKs: []int{183067}},
		Now:     func() time.Time { return fetchedAt },
	}
	return httpapi.New(cfg, refresher, nil, func() time.Time { return now }).Handler(), refresher
}

func do(t *testing.T, handler http.Handler, method, path string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func authed() map[string]string { return map[string]string{"Authorization": "Bearer " + token} }

func decode(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding the response: %v\nbody: %s", err, rec.Body.String())
	}
	return body
}

func TestAuthentication(t *testing.T) {
	handler, refresher := newServer(t, fetchedAt, &stubFetcher{result: sampleResult()})
	if _, err := refresher.Refresh(context.Background()); err != nil {
		t.Fatalf("seeding a snapshot: %v", err)
	}

	for _, tc := range []struct {
		name    string
		headers map[string]string
		want    int
	}{
		{"no credentials", nil, http.StatusUnauthorized},
		{"wrong token", map[string]string{"Authorization": "Bearer nope"}, http.StatusUnauthorized},
		{"wrong scheme", map[string]string{"Authorization": "Token " + token}, http.StatusUnauthorized},
		{"empty bearer", map[string]string{"Authorization": "Bearer "}, http.StatusUnauthorized},
		{"valid bearer", authed(), http.StatusOK},
		{"valid api key header", map[string]string{"X-Api-Key": token}, http.StatusOK},
		// Cloud Scheduler's OIDC token occupies the Authorization header, so the
		// service token has to be accepted from X-Api-Key at the same time.
		{"oidc in authorization plus api key", map[string]string{
			"Authorization": "Bearer some.oidc.token", "X-Api-Key": token,
		}, http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := do(t, handler, http.MethodGet, "/v1/summary", tc.headers)
			if rec.Code != tc.want {
				t.Errorf("status = %d, want %d (body %s)", rec.Code, tc.want, rec.Body.String())
			}
			if tc.want == http.StatusUnauthorized && rec.Header().Get("WWW-Authenticate") == "" {
				t.Error("a 401 should say what credential it wants")
			}
		})
	}
}

func TestHealthzNeedsNoToken(t *testing.T) {
	handler, _ := newServer(t, fetchedAt, &stubFetcher{result: sampleResult()})
	rec := do(t, handler, http.MethodGet, "/healthz", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := decode(t, rec)
	// Healthy but with nothing to serve yet: Cloud Run must not restart an instance
	// that is merely waiting for its first scheduled refresh.
	if body["status"] != "ok" || body["snapshot"] != "missing" {
		t.Errorf("body = %v, want status ok and snapshot missing", body)
	}
}

func TestHealthzReportsSnapshotFailure(t *testing.T) {
	for _, tc := range []struct {
		name  string
		store store.Store
	}{
		{"storage error", stubStore{err: errors.New("storage unavailable")}},
		{"invalid snapshot", stubStore{data: []byte("not gzip")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.Config{APITokens: []string{token}, StaleAfter: 30 * time.Minute}
			refresher := &snapshot.Refresher{
				Fetcher: &stubFetcher{result: sampleResult()},
				Store:   tc.store,
			}
			handler := httpapi.New(cfg, refresher, nil, func() time.Time { return fetchedAt }).Handler()

			rec := do(t, handler, http.MethodGet, "/healthz", nil)
			if rec.Code != http.StatusServiceUnavailable {
				t.Fatalf("status = %d, want 503 (body %s)", rec.Code, rec.Body.String())
			}
			body := decode(t, rec)
			if body["status"] != "error" || body["snapshot"] != "unavailable" {
				t.Errorf("body = %v, want error and unavailable", body)
			}
		})
	}
}

func TestSummaryBeforeAnyRefresh(t *testing.T) {
	handler, _ := newServer(t, fetchedAt, &stubFetcher{result: sampleResult()})
	rec := do(t, handler, http.MethodGet, "/v1/summary", authed())
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 before the first refresh", rec.Code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Error("a 503 should tell the caller when to come back")
	}
}

func TestSummary(t *testing.T) {
	handler, refresher := newServer(t, fetchedAt.Add(2*time.Minute), &stubFetcher{result: sampleResult()})
	if _, err := refresher.Refresh(context.Background()); err != nil {
		t.Fatalf("seeding a snapshot: %v", err)
	}

	rec := do(t, handler, http.MethodGet, "/v1/summary", authed())
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	body := decode(t, rec)

	// The freshness fields Apps Script reads.
	if body["stale"] != false {
		t.Errorf("stale = %v, want false at two minutes old", body["stale"])
	}
	if age, ok := body["age_seconds"].(float64); !ok || age != 120 {
		t.Errorf("age_seconds = %v, want 120", body["age_seconds"])
	}
	if body["source_duration_ms"] != float64(78000) {
		t.Errorf("source_duration_ms = %v, want 78000", body["source_duration_ms"])
	}
	if body["schema_version"] != float64(aggregate.SchemaVersion) {
		t.Errorf("schema_version = %v, want %d", body["schema_version"], aggregate.SchemaVersion)
	}

	// The fields the sheets consume must be present under the documented names.
	for _, key := range []string{"ticket_groups", "tshirt_sizes", "merch", "counts", "orders_by_state"} {
		if _, ok := body[key]; !ok {
			t.Errorf("response is missing %q: %v", key, body)
		}
	}
	groups, _ := body["ticket_groups"].(map[string]any)
	standard, _ := groups["Standard"].(map[string]any)
	if standard["income_ex_vat"] != float64(800) {
		t.Errorf("Standard income_ex_vat = %v, want 800", standard["income_ex_vat"])
	}

	// Aggregates only, so this one may be cached; and it must not leak an attendee.
	if rec.Header().Get("Cache-Control") == "no-store" {
		t.Log("summary is marked no-store, which is harmless but unnecessary")
	}
	if body["orders"] != nil {
		t.Error("the summary must not carry raw orders")
	}
}

func TestSummaryReportsStaleness(t *testing.T) {
	handler, refresher := newServer(t, fetchedAt.Add(31*time.Minute), &stubFetcher{result: sampleResult()})
	if _, err := refresher.Refresh(context.Background()); err != nil {
		t.Fatalf("seeding a snapshot: %v", err)
	}

	rec := do(t, handler, http.MethodGet, "/v1/summary", authed())
	// Still served: an old number with a warning beats no number at all.
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; stale data is still served", rec.Code)
	}
	if body := decode(t, rec); body["stale"] != true {
		t.Errorf("stale = %v, want true past the 30-minute limit", body["stale"])
	}
}

func TestSponsors(t *testing.T) {
	handler, refresher := newServer(t, fetchedAt, &stubFetcher{result: sampleResult()})
	if _, err := refresher.Refresh(context.Background()); err != nil {
		t.Fatalf("seeding a snapshot: %v", err)
	}

	rec := do(t, handler, http.MethodGet, "/v1/sponsors", authed())
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	// Carries names, emails and employers, so it must not be cached anywhere.
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store on a response containing personal data", got)
	}
	body := decode(t, rec)
	sponsors, ok := body["sponsors"].([]any)
	if !ok || len(sponsors) != 1 {
		t.Fatalf("sponsors = %v, want one", body["sponsors"])
	}
	first, _ := sponsors[0].(map[string]any)
	if first["company_name"] != "Alpha A/S" {
		t.Errorf("company_name = %v, want Alpha A/S", first["company_name"])
	}
}

func TestOrdersResponse(t *testing.T) {
	handler, refresher := newServer(t, fetchedAt, &stubFetcher{result: sampleResult()})
	if _, err := refresher.Refresh(context.Background()); err != nil {
		t.Fatalf("seeding a snapshot: %v", err)
	}

	rec := do(t, handler, http.MethodGet, "/v1/orders", authed())
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store on the attendee payload", got)
	}
	body := decode(t, rec)
	orders, ok := body["orders"].(map[string]any)
	if !ok {
		t.Fatalf("orders = %v, want the modelled order fields", body["orders"])
	}
	if orders["uuid"] != "event-uuid" {
		t.Errorf("uuid = %v, want the upstream event uuid preserved", orders["uuid"])
	}
}

func TestRefresh(t *testing.T) {
	handler, _ := newServer(t, fetchedAt, &stubFetcher{result: sampleResult()})

	rec := do(t, handler, http.MethodPost, "/v1/refresh", authed())
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	body := decode(t, rec)
	counts, _ := body["counts"].(map[string]any)
	if counts["tickets"] != float64(2) {
		t.Errorf("counts = %v, want 2 tickets", body["counts"])
	}

	// And the data is now readable.
	if rec := do(t, handler, http.MethodGet, "/v1/summary", authed()); rec.Code != http.StatusOK {
		t.Errorf("summary after a refresh: status = %d, want 200", rec.Code)
	}
}

// TestRefreshWithRejectedToken checks the status translation: TicketButler rejecting
// our token is a configuration fault here, not an authentication fault of the caller,
// so it must not come back as a 401 that would send an operator hunting the wrong token.
func TestRefreshWithRejectedToken(t *testing.T) {
	fetcher := &stubFetcher{err: &ticketbutler.ErrUpstream{StatusCode: http.StatusUnauthorized, Body: "Invalid token."}}
	handler, _ := newServer(t, fetchedAt, fetcher)

	rec := do(t, handler, http.MethodPost, "/v1/refresh", authed())
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", rec.Code)
	}
	if msg, _ := decode(t, rec)["error"].(string); msg == "" {
		t.Error("the error should name what to check")
	}
}

func TestMethodAndPathRouting(t *testing.T) {
	handler, refresher := newServer(t, fetchedAt, &stubFetcher{result: sampleResult()})
	if _, err := refresher.Refresh(context.Background()); err != nil {
		t.Fatalf("seeding a snapshot: %v", err)
	}

	for _, tc := range []struct {
		method, path string
		want         int
	}{
		{http.MethodPost, "/v1/summary", http.StatusMethodNotAllowed},
		{http.MethodGet, "/v1/refresh", http.StatusMethodNotAllowed},
		{http.MethodGet, "/v1/nothing", http.StatusNotFound},
		{http.MethodGet, "/", http.StatusNotFound},
	} {
		rec := do(t, handler, tc.method, tc.path, authed())
		if rec.Code != tc.want {
			t.Errorf("%s %s: status = %d, want %d", tc.method, tc.path, rec.Code, tc.want)
		}
	}
}
