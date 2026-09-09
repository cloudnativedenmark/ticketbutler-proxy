package config_test

import (
	"strings"
	"testing"
	"time"

	"github.com/cloudnativedenmark/ticketbutler-proxy/internal/config"
)

// valid returns the smallest environment Load accepts, for a test to mutate.
func valid() map[string]string {
	return map[string]string{
		"TICKETBUTLER_EVENT_UUID": "11cc0a9f7f124ddcb11bc027e1a64f23",
		"TICKETBUTLER_TOKEN":      "upstream-token",
		"API_TOKENS":              "a-token-of-sufficient-length",
	}
}

func load(t *testing.T, env map[string]string) (config.Config, error) {
	t.Helper()
	// Setenv registers its own cleanup, so each test gets a clean environment.
	for _, key := range []string{
		"PORT", "TICKETBUTLER_BASE_URL", "TICKETBUTLER_EVENT_UUID", "TICKETBUTLER_TOKEN",
		"TICKETBUTLER_FILE", "UPSTREAM_TIMEOUT", "API_TOKENS", "SNAPSHOT_BUCKET",
		"SNAPSHOT_OBJECT", "STALE_AFTER", "SPONSOR_TICKET_TYPE_PKS", "MERCH_NAME_PATTERNS",
	} {
		t.Setenv(key, "")
	}
	for key, value := range env {
		t.Setenv(key, value)
	}
	return config.Load()
}

func TestLoadDefaults(t *testing.T) {
	cfg, err := load(t, valid())
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Port != "8080" {
		t.Errorf("Port = %q, want 8080", cfg.Port)
	}
	if cfg.UpstreamTimeout != 10*time.Minute {
		t.Errorf("UpstreamTimeout = %v, want 10m: the whole point is exceeding the 60s Apps Script limit", cfg.UpstreamTimeout)
	}
	if cfg.StaleAfter != 90*time.Minute {
		t.Errorf("StaleAfter = %v, want 90m: three 30-minute refresh cycles", cfg.StaleAfter)
	}
	if cfg.UpstreamAttempts != 3 {
		t.Errorf("UpstreamAttempts = %d, want 3", cfg.UpstreamAttempts)
	}
	if cfg.Object != "snapshot.json.gz" {
		t.Errorf("Object = %q, want snapshot.json.gz", cfg.Object)
	}
	// The pattern the old Apps Script hardcoded.
	if len(cfg.MerchNamePatterns) != 1 || cfg.MerchNamePatterns[0] != "hoodie" {
		t.Errorf("MerchNamePatterns = %v, want [hoodie]", cfg.MerchNamePatterns)
	}
	if got := cfg.OrdersURL(); got != "https://cloudnativedenmark.ticketbutler.io/api/v3/events/11cc0a9f7f124ddcb11bc027e1a64f23/orders/" {
		t.Errorf("OrdersURL = %q", got)
	}
}

func TestLoadTrimsTrailingSlashFromBaseURL(t *testing.T) {
	env := valid()
	env["TICKETBUTLER_BASE_URL"] = "https://example.invalid/"
	cfg, err := load(t, env)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if strings.Contains(cfg.OrdersURL(), "//api") {
		t.Errorf("OrdersURL = %q, want no doubled slash", cfg.OrdersURL())
	}
}

func TestLoadRejectsMissingSettings(t *testing.T) {
	for name, mutate := range map[string]func(map[string]string){
		"no event uuid": func(e map[string]string) { delete(e, "TICKETBUTLER_EVENT_UUID") },
		"no upstream credential or file": func(e map[string]string) {
			delete(e, "TICKETBUTLER_TOKEN")
		},
		// Without this the service would hand attendee data to anyone who asks, so
		// refusing to start is the only safe default.
		"no api tokens":     func(e map[string]string) { delete(e, "API_TOKENS") },
		"short api token":   func(e map[string]string) { e["API_TOKENS"] = "short" },
		"bad duration":      func(e map[string]string) { e["UPSTREAM_TIMEOUT"] = "ten minutes" },
		"negative duration": func(e map[string]string) { e["UPSTREAM_TIMEOUT"] = "-5m" },
		"bad sponsor pk":    func(e map[string]string) { e["SPONSOR_TICKET_TYPE_PKS"] = "183067,abc" },
	} {
		t.Run(name, func(t *testing.T) {
			env := valid()
			mutate(env)
			if _, err := load(t, env); err == nil {
				t.Error("expected Load to reject this configuration")
			}
		})
	}
}

func TestLoadAcceptsFileInsteadOfToken(t *testing.T) {
	env := valid()
	delete(env, "TICKETBUTLER_TOKEN")
	env["TICKETBUTLER_FILE"] = "testdata/orders.sample.json"

	cfg, err := load(t, env)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.UsesFile() {
		t.Error("UsesFile should be true so development needs no credential")
	}
}

func TestLoadParsesLists(t *testing.T) {
	env := valid()
	env["API_TOKENS"] = " first-token-long-enough , second-token-long-enough ,, "
	env["SPONSOR_TICKET_TYPE_PKS"] = "183067, 183068"
	env["MERCH_NAME_PATTERNS"] = "Hoodie, T-Shirt"

	cfg, err := load(t, env)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// Two tokens valid at once is what makes rotation possible without downtime.
	if len(cfg.APITokens) != 2 {
		t.Errorf("APITokens = %v, want two entries with blanks and spaces dropped", cfg.APITokens)
	}
	if len(cfg.SponsorTicketTypePKs) != 2 || cfg.SponsorTicketTypePKs[1] != 183068 {
		t.Errorf("SponsorTicketTypePKs = %v, want [183067 183068]", cfg.SponsorTicketTypePKs)
	}
	// Lower-cased, because matching is done against a lower-cased ticket type name.
	if cfg.MerchNamePatterns[0] != "hoodie" || cfg.MerchNamePatterns[1] != "t-shirt" {
		t.Errorf("MerchNamePatterns = %v, want lower-cased", cfg.MerchNamePatterns)
	}
}

func TestParseHelpers(t *testing.T) {
	if got, err := config.ParseIntList("1, 2,3"); err != nil || len(got) != 3 || got[2] != 3 {
		t.Errorf("ParseIntList = %v, %v", got, err)
	}
	if _, err := config.ParseIntList("1,x"); err == nil {
		t.Error("ParseIntList should reject a non-numeric entry")
	}
	if got := config.ParseMerchPatterns(""); len(got) != 1 || got[0] != "hoodie" {
		t.Errorf("ParseMerchPatterns = %v, want the default [hoodie]", got)
	}
}
