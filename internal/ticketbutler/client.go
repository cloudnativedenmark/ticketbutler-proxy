package ticketbutler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"time"
)

// Client fetches orders from the TicketButler v3 API.
//
// The endpoint offers no pagination, filtering or field selection, so every call
// returns every order for the event in one response. That response is what stopped
// fitting inside Apps Script's 60-second ceiling, so the only lever left is a
// timeout long enough for it to finish — which is the reason this service exists.
type Client struct {
	// URL is the fully-qualified orders endpoint.
	URL string

	// Token is the TicketButler API token, sent as "Authorization: Token <token>".
	Token string

	// File, when set, is read instead of calling the API. Development and CI use
	// this so they need neither a token nor network access.
	File string

	// Timeout bounds a single attempt.
	Timeout time.Duration

	// Attempts is how many times to try before giving up. Zero means 3.
	Attempts int

	// Log receives one line per attempt. Nil means slog.Default.
	Log *slog.Logger

	// now and sleep exist so tests can run the retry loop without waiting.
	now   func() time.Time
	sleep func(context.Context, time.Duration) error
}

// ErrUpstream reports a non-retryable response from TicketButler, such as a rejected
// token. It is separate from a transport failure so callers can tell a broken
// configuration from a slow network.
type ErrUpstream struct {
	StatusCode int
	Body       string
}

func (e *ErrUpstream) Error() string {
	return fmt.Sprintf("ticketbutler returned %d: %s", e.StatusCode, e.Body)
}

// Result is one successful fetch, with how long the fetch took. The duration is
// carried through to the API responses because it is the number that says how close
// the upstream is to being unusable again.
type Result struct {
	Orders   OrdersResponse
	Duration time.Duration
}

// FetchOrders retrieves every order for the event.
//
// Transport errors, timeouts and 5xx responses are retried with exponential
// backoff. A 4xx is returned immediately: a rejected token will be rejected just as
// firmly on the third attempt, and retrying only delays a clear error.
func (c *Client) FetchOrders(ctx context.Context) (Result, error) {
	log := c.Log
	if log == nil {
		log = slog.Default()
	}
	attempts := c.Attempts
	if attempts <= 0 {
		attempts = 3
	}
	started := c.timeNow()

	if c.File != "" {
		orders, err := c.readFile()
		if err != nil {
			return Result{}, err
		}
		log.Info("read orders from file instead of the api", "file", c.File, "orders", len(orders.Orders))
		return Result{Orders: orders, Duration: c.timeNow().Sub(started)}, nil
	}

	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		orders, err := c.fetchOnce(ctx)
		if err == nil {
			duration := c.timeNow().Sub(started)
			log.Info("fetched orders from ticketbutler",
				"attempt", attempt, "orders", len(orders.Orders),
				"duration", duration.Round(time.Millisecond))
			return Result{Orders: orders, Duration: duration}, nil
		}

		var upstream *ErrUpstream
		if errors.As(err, &upstream) && upstream.StatusCode < 500 {
			return Result{}, err
		}
		if ctx.Err() != nil {
			return Result{}, errors.Join(err, ctx.Err())
		}

		lastErr = err
		log.Warn("fetching orders failed", "attempt", attempt, "of", attempts, "error", err)

		if attempt < attempts {
			// 1s, 2s, 4s … long enough to ride out a blip, short enough that the
			// whole retry budget still fits inside the request timeout.
			backoff := time.Second << (attempt - 1)
			if err := c.sleepFor(ctx, backoff); err != nil {
				return Result{}, errors.Join(lastErr, err)
			}
		}
	}
	return Result{}, fmt.Errorf("fetching orders failed after %d attempts: %w", attempts, lastErr)
}

func (c *Client) fetchOnce(ctx context.Context) (OrdersResponse, error) {
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.URL, nil)
	if err != nil {
		return OrdersResponse{}, err
	}
	req.Header.Set("Authorization", "Token "+c.Token)
	req.Header.Set("Accept", "application/json")
	// Accept-Encoding is deliberately not set: net/http requests gzip and
	// transparently decompresses it only while the header is unset. Setting it by
	// hand hands back a still-compressed body.

	// A per-attempt client rather than a shared one: the timeout above already
	// bounds the attempt, and no connection is worth reusing across a ten-minute gap.
	resp, err := (&http.Client{Timeout: timeout}).Do(req)
	if err != nil {
		return OrdersResponse{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// Cap the excerpt: an HTML error page would otherwise land in the logs whole.
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return OrdersResponse{}, &ErrUpstream{StatusCode: resp.StatusCode, Body: string(body)}
	}

	var orders OrdersResponse
	if err := json.NewDecoder(resp.Body).Decode(&orders); err != nil {
		return OrdersResponse{}, fmt.Errorf("decoding orders response: %w", err)
	}
	return orders, nil
}

func (c *Client) readFile() (OrdersResponse, error) {
	f, err := os.Open(c.File)
	if err != nil {
		return OrdersResponse{}, err
	}
	defer f.Close()

	var orders OrdersResponse
	if err := json.NewDecoder(f).Decode(&orders); err != nil {
		return OrdersResponse{}, fmt.Errorf("decoding %s: %w", c.File, err)
	}
	return orders, nil
}

func (c *Client) timeNow() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}

func (c *Client) sleepFor(ctx context.Context, d time.Duration) error {
	if c.sleep != nil {
		return c.sleep(ctx, d)
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
