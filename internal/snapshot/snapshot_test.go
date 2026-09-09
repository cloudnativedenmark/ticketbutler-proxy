package snapshot

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/cloudnativedenmark/ticketbutler-proxy/internal/aggregate"
	"github.com/cloudnativedenmark/ticketbutler-proxy/internal/store"
	"github.com/cloudnativedenmark/ticketbutler-proxy/internal/ticketbutler"
)

var fixedTime = time.Date(2026, 9, 9, 10, 0, 0, 0, time.UTC)

func sampleResult() ticketbutler.Result {
	return ticketbutler.Result{
		Duration: 78 * time.Second,
		Orders: ticketbutler.OrdersResponse{
			UUID: "event-uuid",
			Orders: []ticketbutler.Order{{
				State:   "PAID",
				VATRate: 0.25,
				Tickets: []ticketbutler.Ticket{
					{TicketTypeName: "Standard", Price: 1000, PriceTotal: 1000, Email: "a@example.example"},
					{TicketTypePK: 183067, TicketTypeName: "Community Sponsor", Price: 0, PriceTotal: 0, CompanyName: "Alpha A/S"},
				},
			}},
		},
	}
}

func testOptions() aggregate.Options {
	return aggregate.Options{MerchNamePatterns: []string{"hoodie"}, SponsorTicketTypePKs: []int{183067}}
}

type stubFetcher struct {
	result ticketbutler.Result
	err    error
	calls  int
}

func (s *stubFetcher) FetchOrders(context.Context) (ticketbutler.Result, error) {
	s.calls++
	return s.result, s.err
}

func TestBuild(t *testing.T) {
	snap := Build(sampleResult(), fixedTime, testOptions())

	if !snap.FetchedAt.Equal(fixedTime) {
		t.Errorf("FetchedAt = %v, want %v", snap.FetchedAt, fixedTime)
	}
	if snap.SourceDurationMS != 78000 {
		t.Errorf("SourceDurationMS = %d, want 78000", snap.SourceDurationMS)
	}
	if snap.Summary.Counts.Tickets != 2 {
		t.Errorf("tickets = %d, want 2", snap.Summary.Counts.Tickets)
	}
	if len(snap.Sponsors) != 1 || snap.Sponsors[0].CompanyName != "Alpha A/S" {
		t.Errorf("sponsors = %+v, want one Alpha A/S", snap.Sponsors)
	}
}

func TestEncodeDecodeRoundTrip(t *testing.T) {
	original := Build(sampleResult(), fixedTime, testOptions())

	data, err := original.Encode()
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	// Gzip, so the stored object should not be readable JSON.
	if len(data) > 1 && data[0] == '{' {
		t.Error("the encoded snapshot should be compressed, not plain JSON")
	}

	decoded, err := Decode(data)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if !decoded.FetchedAt.Equal(original.FetchedAt) {
		t.Errorf("FetchedAt = %v, want %v", decoded.FetchedAt, original.FetchedAt)
	}
	if decoded.Summary.Counts != original.Summary.Counts {
		t.Errorf("counts = %+v, want %+v", decoded.Summary.Counts, original.Summary.Counts)
	}
	if len(decoded.Orders.Orders) != len(original.Orders.Orders) {
		t.Errorf("orders = %d, want %d", len(decoded.Orders.Orders), len(original.Orders.Orders))
	}
	if decoded.Summary.TicketGroups["Standard"] != original.Summary.TicketGroups["Standard"] {
		t.Error("ticket groups did not survive the round trip")
	}
}

func TestDecodeRejectsGarbage(t *testing.T) {
	if _, err := Decode([]byte("not gzip at all")); err == nil {
		t.Fatal("expected an error for input that is not a snapshot")
	}
}

func TestStale(t *testing.T) {
	snap := Build(sampleResult(), fixedTime, testOptions())
	staleAfter := 30 * time.Minute

	if snap.Stale(fixedTime.Add(29*time.Minute), staleAfter) {
		t.Error("a 29-minute-old snapshot should not be stale with a 30-minute limit")
	}
	if !snap.Stale(fixedTime.Add(31*time.Minute), staleAfter) {
		t.Error("a 31-minute-old snapshot should be stale with a 30-minute limit")
	}
	if got := snap.Age(fixedTime.Add(time.Minute)); got != time.Minute {
		t.Errorf("Age = %v, want 1m", got)
	}
}

func TestRefresherStoresAndCaches(t *testing.T) {
	fetcher := &stubFetcher{result: sampleResult()}
	memory := store.NewMemory()
	r := &Refresher{
		Fetcher: fetcher, Store: memory, Options: testOptions(),
		Now: func() time.Time { return fixedTime },
	}

	snap, err := r.Refresh(context.Background())
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if snap.Summary.Counts.Tickets != 2 {
		t.Errorf("tickets = %d, want 2", snap.Summary.Counts.Tickets)
	}

	// Current must be served from memory, without re-fetching.
	if _, err := r.Current(context.Background()); err != nil {
		t.Fatalf("Current: %v", err)
	}
	if fetcher.calls != 1 {
		t.Errorf("fetched %d times, want 1: a read must never trigger an upstream fetch", fetcher.calls)
	}

	// And a fresh refresher over the same store must find it.
	cold := &Refresher{Fetcher: &stubFetcher{}, Store: memory, Options: testOptions()}
	loaded, err := cold.Current(context.Background())
	if err != nil {
		t.Fatalf("Current on a cold refresher: %v", err)
	}
	if loaded.Summary.Counts.Tickets != 2 {
		t.Errorf("tickets = %d, want the stored 2", loaded.Summary.Counts.Tickets)
	}
}

func TestRefresherCurrentWithoutSnapshot(t *testing.T) {
	r := &Refresher{Fetcher: &stubFetcher{}, Store: store.NewMemory(), Options: testOptions()}
	_, err := r.Current(context.Background())
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("error = %v, want store.ErrNotFound before the first refresh", err)
	}
}

func TestRefresherPropagatesFetchFailure(t *testing.T) {
	want := errors.New("upstream is down")
	r := &Refresher{
		Fetcher: &stubFetcher{err: want}, Store: store.NewMemory(), Options: testOptions(),
	}
	if _, err := r.Refresh(context.Background()); !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}
}

// failingStore accepts nothing, standing in for a Cloud Storage outage or a missing
// permission.
type failingStore struct{ store.Store }

func (failingStore) Load(context.Context) ([]byte, error) { return nil, store.ErrNotFound }
func (failingStore) Save(context.Context, []byte) error   { return errors.New("permission denied") }
func (failingStore) Describe() string                     { return "failing" }

// TestRefresherServesDataItCouldNotStore covers the case worth getting right: the
// fetch succeeded, so the numbers are good and should be usable even though they
// will not outlive the instance.
func TestRefresherServesDataItCouldNotStore(t *testing.T) {
	r := &Refresher{Fetcher: &stubFetcher{result: sampleResult()}, Store: failingStore{}, Options: testOptions()}

	snap, err := r.Refresh(context.Background())
	if err == nil {
		t.Fatal("expected the storage failure to be reported")
	}
	if snap == nil {
		t.Fatal("expected the snapshot to be returned anyway; the fetch succeeded")
	}
	current, err := r.Current(context.Background())
	if err != nil {
		t.Fatalf("Current: %v", err)
	}
	if current.Summary.Counts.Tickets != 2 {
		t.Error("the unstored snapshot should still be served from memory")
	}
}
