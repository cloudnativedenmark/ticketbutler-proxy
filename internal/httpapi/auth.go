package httpapi

import (
	"crypto/sha256"
	"crypto/subtle"
	"net/http"
	"strings"
)

// authenticator checks bearer tokens against the configured set.
//
// Several tokens are valid at once so one can be rotated without a window in which
// neither the old nor the new value works: add the new token, update Apps Script,
// then drop the old one.
type authenticator struct {
	digests [][32]byte
}

func newAuthenticator(tokens []string) *authenticator {
	a := &authenticator{}
	for _, t := range tokens {
		a.digests = append(a.digests, sha256.Sum256([]byte(t)))
	}
	return a
}

// allows reports whether the request carries a valid token.
//
// Tokens are compared as fixed-length digests in constant time, so neither the
// comparison nor its duration reveals anything about the expected value.
func (a *authenticator) allows(r *http.Request) bool {
	var matched int
	for _, presented := range presentedTokens(r) {
		got := sha256.Sum256([]byte(presented))
		// Every candidate is checked, without an early exit, so the work done does
		// not depend on which token matched.
		for _, want := range a.digests {
			matched |= subtle.ConstantTimeCompare(got[:], want[:])
		}
	}
	return matched == 1
}

// presentedTokens returns every credential the request offers, from both the
// Authorization header and X-Api-Key.
//
// Both are checked rather than one taking precedence, because the two can be in use
// at the same time by design. Cloud Scheduler authenticates to Cloud Run by putting
// its own OIDC token in the Authorization header; if that were the only header
// examined, enabling Cloud Run IAM would make this service reject the very caller
// that triggers its refreshes. Sending the service token as X-Api-Key alongside the
// OIDC token lets both checks apply, which is stricter than either alone.
func presentedTokens(r *http.Request) []string {
	var tokens []string
	if header := r.Header.Get("Authorization"); header != "" {
		scheme, value, found := strings.Cut(header, " ")
		if found && strings.EqualFold(scheme, "Bearer") {
			if token := strings.TrimSpace(value); token != "" {
				tokens = append(tokens, token)
			}
		}
	}
	if key := strings.TrimSpace(r.Header.Get("X-Api-Key")); key != "" {
		tokens = append(tokens, key)
	}
	return tokens
}

// requireToken wraps a handler so it is only reached by authenticated requests.
func (a *authenticator) requireToken(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !a.allows(r) {
			// WWW-Authenticate tells a well-behaved client what to send, and costs
			// nothing to include.
			w.Header().Set("WWW-Authenticate", `Bearer realm="ticketbutler-proxy"`)
			writeError(w, http.StatusUnauthorized, "a valid bearer token is required")
			return
		}
		next.ServeHTTP(w, r)
	})
}
