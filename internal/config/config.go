// Package config reads the service configuration from the environment.
//
// Cloud Run supplies configuration as environment variables, with secrets mounted
// from Secret Manager the same way, so there is no config file.
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config is the whole configuration of the service. Load validates it, so the rest
// of the code can use these fields without re-checking them.
type Config struct {
	// Port is the address to listen on. Cloud Run sets this.
	Port string

	// TicketButler upstream.
	BaseURL   string
	EventUUID string
	Token     string

	// File, when set, makes the client read this JSON file instead of calling the
	// API. It exists so development and CI need neither a token nor network access.
	File string

	// UpstreamTimeout is how long a single fetch may take. The whole point of this
	// service is that this can exceed the 60s ceiling Apps Script imposes.
	UpstreamTimeout time.Duration

	// APITokens are the accepted bearer tokens. Several are allowed at once so a
	// token can be rotated without a window where neither value works.
	APITokens []string

	// Snapshot storage. Bucket empty means keep the snapshot in memory only, which
	// is fine locally but loses the snapshot whenever Cloud Run scales to zero.
	Bucket string
	Object string

	// StaleAfter is the age at which a snapshot is reported as stale. Responses stay
	// served past it — a stale number with a warning beats no number at all — but
	// the flag lets the caller say so.
	StaleAfter time.Duration

	// SponsorTicketTypePKs are the ticket type ids counted as community sponsors.
	SponsorTicketTypePKs []int

	// MerchNamePatterns are lower-case substrings that mark a ticket type as
	// merchandise rather than admission.
	MerchNamePatterns []string
}

// Load reads the configuration from the environment and validates it.
func Load() (Config, error) {
	c := Config{
		Port:              env("PORT", "8080"),
		BaseURL:           strings.TrimRight(env("TICKETBUTLER_BASE_URL", "https://cloudnativedenmark.ticketbutler.io"), "/"),
		EventUUID:         os.Getenv("TICKETBUTLER_EVENT_UUID"),
		Token:             os.Getenv("TICKETBUTLER_TOKEN"),
		File:              os.Getenv("TICKETBUTLER_FILE"),
		Bucket:            os.Getenv("SNAPSHOT_BUCKET"),
		Object:            env("SNAPSHOT_OBJECT", "snapshot.json.gz"),
		APITokens:         splitList(os.Getenv("API_TOKENS")),
		MerchNamePatterns: lower(splitListDefault(os.Getenv("MERCH_NAME_PATTERNS"), "hoodie")),
	}

	var err error
	if c.UpstreamTimeout, err = duration("UPSTREAM_TIMEOUT", 10*time.Minute); err != nil {
		return Config{}, err
	}
	if c.StaleAfter, err = duration("STALE_AFTER", 30*time.Minute); err != nil {
		return Config{}, err
	}
	if c.SponsorTicketTypePKs, err = ints("SPONSOR_TICKET_TYPE_PKS"); err != nil {
		return Config{}, err
	}

	return c, c.validate()
}

func (c Config) validate() error {
	var errs []error
	if c.EventUUID == "" {
		errs = append(errs, errors.New("TICKETBUTLER_EVENT_UUID is required"))
	}
	// A token is only needed when actually calling the API.
	if c.Token == "" && c.File == "" {
		errs = append(errs, errors.New("either TICKETBUTLER_TOKEN or TICKETBUTLER_FILE is required"))
	}
	if len(c.APITokens) == 0 {
		errs = append(errs, errors.New("API_TOKENS is required: the service would otherwise serve attendee data unauthenticated"))
	}
	for _, t := range c.APITokens {
		if len(t) < 16 {
			errs = append(errs, errors.New("every API_TOKENS entry must be at least 16 characters"))
			break
		}
	}
	if c.Bucket == "" && c.Object == "" {
		errs = append(errs, errors.New("SNAPSHOT_OBJECT must be set when SNAPSHOT_BUCKET is"))
	}
	return errors.Join(errs...)
}

// UsesFile reports whether the upstream is a local file rather than the API.
func (c Config) UsesFile() bool { return c.File != "" }

// OrdersURL is the upstream endpoint for this event's orders.
func (c Config) OrdersURL() string {
	return fmt.Sprintf("%s/api/v3/events/%s/orders/", c.BaseURL, c.EventUUID)
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func duration(key string, def time.Duration) (time.Duration, error) {
	v := os.Getenv(key)
	if v == "" {
		return def, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("%s: %w", key, err)
	}
	if d <= 0 {
		return 0, fmt.Errorf("%s must be positive, got %s", key, v)
	}
	return d, nil
}

func ints(key string) ([]int, error) {
	var out []int
	for _, s := range splitList(os.Getenv(key)) {
		n, err := strconv.Atoi(s)
		if err != nil {
			return nil, fmt.Errorf("%s: %q is not a number", key, s)
		}
		out = append(out, n)
	}
	return out, nil
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func splitListDefault(s, def string) []string {
	if out := splitList(s); len(out) > 0 {
		return out
	}
	return splitList(def)
}

func lower(in []string) []string {
	out := make([]string, len(in))
	for i, s := range in {
		out[i] = strings.ToLower(s)
	}
	return out
}
