package ticketbutler

import (
	"compress/gzip"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// newTestClient returns a client whose retry backoff does not actually sleep, so the
// retry logic can be exercised without adding seconds to the test run.
func newTestClient(url string) *Client {
	return &Client{
		URL:     url,
		Token:   "test-token",
		Timeout: 5 * time.Second,
		sleep:   func(context.Context, time.Duration) error { return nil },
	}
}

const onePaidOrder = `{"uuid":"event","orders":[{"uuid":"o1","state":"PAID","vat_rate":0.25,
	"tickets":[{"ticket_type_name":"Standard","price":"1000.00","price_total":"1000.00"}]}]}`

func TestFetchOrders(t *testing.T) {
	var gotAuth, gotAccept string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotAccept = r.Header.Get("Accept")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(onePaidOrder))
	}))
	defer srv.Close()

	result, err := newTestClient(srv.URL).FetchOrders(context.Background())
	if err != nil {
		t.Fatalf("FetchOrders: %v", err)
	}
	if gotAuth != "Token test-token" {
		t.Errorf("Authorization = %q, want %q", gotAuth, "Token test-token")
	}
	if gotAccept != "application/json" {
		t.Errorf("Accept = %q, want application/json", gotAccept)
	}
	if len(result.Orders.Orders) != 1 {
		t.Fatalf("got %d orders, want 1", len(result.Orders.Orders))
	}
	if price := result.Orders.Orders[0].Tickets[0].PriceTotal; price != 1000 {
		t.Errorf("price_total = %v, want 1000: decimal strings must decode to numbers", price)
	}
}

// TestFetchOrdersDecompresses guards a mistake that is easy to reintroduce: setting
// Accept-Encoding by hand stops net/http decompressing the body transparently, and
// the JSON decode then fails on gzip bytes.
func TestFetchOrdersDecompresses(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept-Encoding") == "" {
			t.Error("net/http should be requesting gzip on our behalf")
		}
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Set("Content-Type", "application/json")
		zw := gzip.NewWriter(w)
		defer zw.Close()
		_, _ = zw.Write([]byte(onePaidOrder))
	}))
	defer srv.Close()

	result, err := newTestClient(srv.URL).FetchOrders(context.Background())
	if err != nil {
		t.Fatalf("FetchOrders: %v", err)
	}
	if len(result.Orders.Orders) != 1 {
		t.Errorf("got %d orders, want 1", len(result.Orders.Orders))
	}
}

func TestFetchOrdersRetriesServerErrors(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) < 3 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		_, _ = w.Write([]byte(onePaidOrder))
	}))
	defer srv.Close()

	client := newTestClient(srv.URL)
	client.Attempts = 3
	if _, err := client.FetchOrders(context.Background()); err != nil {
		t.Fatalf("FetchOrders: %v", err)
	}
	if got := calls.Load(); got != 3 {
		t.Errorf("made %d calls, want 3", got)
	}
}

// TestFetchOrdersDoesNotRetryClientErrors matters operationally: a rejected token is
// rejected just as firmly on the third attempt, and retrying only delays a clear
// error while holding the refresh open.
func TestFetchOrdersDoesNotRetryClientErrors(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"detail":"Invalid token."}`))
	}))
	defer srv.Close()

	client := newTestClient(srv.URL)
	client.Attempts = 3
	_, err := client.FetchOrders(context.Background())
	if err == nil {
		t.Fatal("expected an error")
	}
	var upstream *ErrUpstream
	if !errors.As(err, &upstream) {
		t.Fatalf("error = %v, want an *ErrUpstream so the caller can tell configuration from transport", err)
	}
	if upstream.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", upstream.StatusCode)
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("made %d calls, want 1: a 4xx must not be retried", got)
	}
}

func TestFetchOrdersGivesUpAfterAllAttempts(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	client := newTestClient(srv.URL)
	client.Attempts = 2
	if _, err := client.FetchOrders(context.Background()); err == nil {
		t.Fatal("expected an error after exhausting the attempts")
	}
}

func TestFetchOrdersHonoursCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	client := newTestClient(srv.URL)
	client.Attempts = 3
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	if _, err := client.FetchOrders(ctx); err == nil {
		t.Fatal("expected an error when the context is cancelled")
	}
}

func TestFetchOrdersFromFile(t *testing.T) {
	client := &Client{File: filepath.Join("..", "..", "testdata", "orders.sample.json")}
	result, err := client.FetchOrders(context.Background())
	if err != nil {
		t.Fatalf("FetchOrders: %v", err)
	}
	if len(result.Orders.Orders) != 63 {
		t.Errorf("got %d orders, want the fixture's 63", len(result.Orders.Orders))
	}
}

func TestFetchOrdersFromMissingFile(t *testing.T) {
	client := &Client{File: filepath.Join("testdata", "does-not-exist.json")}
	if _, err := client.FetchOrders(context.Background()); err == nil {
		t.Fatal("expected an error for a missing file")
	}
}

func TestAmountUnmarshal(t *testing.T) {
	for input, want := range map[string]Amount{
		`"1499.00"`: 1499, `"0.00"`: 0, `1499`: 1499, `""`: 0, `null`: 0, `"-50.5"`: -50.5,
	} {
		var got Amount
		if err := got.UnmarshalJSON([]byte(input)); err != nil {
			t.Errorf("UnmarshalJSON(%s): %v", input, err)
			continue
		}
		if got != want {
			t.Errorf("UnmarshalJSON(%s) = %v, want %v", input, got, want)
		}
	}
	var got Amount
	if err := got.UnmarshalJSON([]byte(`"not a number"`)); err == nil {
		t.Error("expected an error for a non-numeric amount")
	}
}

func TestTicketAnswerHelpers(t *testing.T) {
	ticket := Ticket{AnswerCollection: &AnswerCollection{Answers: []Answer{
		{QuestionHeading: " T-Shirt Size ", AnsweredChoices: []Choice{{ChoiceHeading: "Large"}}},
		{QuestionHeading: "Job Title", AnswerValue: "Engineer"},
	}}}

	answer, ok := ticket.AnswerFor("T-Shirt Size")
	if !ok {
		t.Fatal("AnswerFor should trim the heading before comparing")
	}
	if got := answer.FirstChoice(); got != "Large" {
		t.Errorf("FirstChoice = %q, want Large", got)
	}
	if _, ok := ticket.AnswerFor("Absent"); ok {
		t.Error("AnswerFor should report a missing question")
	}
	// A ticket with no answer collection at all must not panic.
	if got := (Ticket{}).Answers(); got != nil {
		t.Errorf("Answers = %v, want nil for a ticket with no answer collection", got)
	}
	if got := (Answer{}).FirstChoice(); got != "" {
		t.Errorf("FirstChoice = %q, want empty for an unanswered question", got)
	}
}
