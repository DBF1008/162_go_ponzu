package api

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

// TestOriginAllowed covers the domain-validation logic used by restricted CORS.
func TestOriginAllowed(t *testing.T) {
	cases := []struct {
		name   string
		domain string
		origin string
		want   bool
	}{
		{"exact https match", "example.com", "https://example.com", true},
		{"exact http match", "example.com", "http://example.com", true},
		{"different domain rejected", "example.com", "https://evil.com", false},
		{"subdomain rejected", "example.com", "https://api.example.com", false},
		{"non-default port rejected", "example.com", "https://example.com:8443", false},
		{"localhost any port", "localhost", "http://localhost:3000", true},
		{"localhost no port", "localhost", "http://localhost", true},
		{"localhost https with port", "localhost", "https://localhost:8080", true},
		{"localhost rejects other host", "localhost", "http://evil.com", false},
		{"localhost rejects lookalike host", "localhost", "http://notlocalhost:3000", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			u, err := url.Parse(tc.origin)
			if err != nil {
				t.Fatalf("failed to parse origin %q: %v", tc.origin, err)
			}

			if got := originAllowed(u, tc.domain); got != tc.want {
				t.Errorf("originAllowed(%q, %q) = %v, want %v", tc.origin, tc.domain, got, tc.want)
			}
		})
	}
}

// runCORS drives corsHandler with a stub downstream handler and reports the
// recorded response along with whether the downstream handler ran.
func runCORS(method, origin string, corsRestricted bool, domain string) (*httptest.ResponseRecorder, bool) {
	req := httptest.NewRequest(method, "/api/contents", nil)
	if origin != "" {
		req.Header.Set("Origin", origin)
	}

	nextCalled := false
	next := func(w http.ResponseWriter, r *http.Request) {
		nextCalled = true
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}

	rec := httptest.NewRecorder()
	corsHandler(rec, req, next, corsRestricted, domain)

	return rec, nextCalled
}

// TestCORSPreflightAndActualAgree is the core regression test: the preflight
// (OPTIONS) and the actual request must advertise an identical, valid
// Access-Control-Allow-Origin so the browser does not block legitimate
// cross-origin requests.
func TestCORSPreflightAndActualAgree(t *testing.T) {
	cases := []struct {
		name            string
		corsRestricted  bool
		domain          string
		origin          string
		wantAllowOrigin string
	}{
		{"restricted example.com", true, "example.com", "https://example.com", "https://example.com"},
		{"restricted localhost dev", true, "localhost", "http://localhost:3000", "http://localhost:3000"},
		{"open mode", false, "example.com", "https://anything.example.org", "*"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			preflight, _ := runCORS(http.MethodOptions, tc.origin, tc.corsRestricted, tc.domain)
			actual, _ := runCORS(http.MethodGet, tc.origin, tc.corsRestricted, tc.domain)

			preflightOrigin := preflight.Header().Get("Access-Control-Allow-Origin")
			actualOrigin := actual.Header().Get("Access-Control-Allow-Origin")

			if preflightOrigin != actualOrigin {
				t.Errorf("preflight Allow-Origin %q != actual Allow-Origin %q",
					preflightOrigin, actualOrigin)
			}
			if preflightOrigin != tc.wantAllowOrigin {
				t.Errorf("Allow-Origin = %q, want %q", preflightOrigin, tc.wantAllowOrigin)
			}
		})
	}
}

// TestCORSPreflightStatusOK verifies an authorized preflight returns 200 with
// CORS headers and does not invoke the downstream handler.
func TestCORSPreflightStatusOK(t *testing.T) {
	rec, nextCalled := runCORS(http.MethodOptions, "https://example.com", true, "example.com")

	if rec.Code != http.StatusOK {
		t.Errorf("preflight status = %d, want %d", rec.Code, http.StatusOK)
	}
	if nextCalled {
		t.Error("next handler should not be called for an OPTIONS preflight")
	}
	if got := rec.Header().Get("Access-Control-Allow-Headers"); got == "" {
		t.Error("preflight should set Access-Control-Allow-Headers")
	}
}

// TestCORSAllowedActualRequest verifies a permitted actual request reaches the
// downstream handler and carries the echoed Origin plus a Vary header.
func TestCORSAllowedActualRequest(t *testing.T) {
	rec, nextCalled := runCORS(http.MethodGet, "https://example.com", true, "example.com")

	if !nextCalled {
		t.Error("next handler should run for an allowed origin")
	}
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://example.com" {
		t.Errorf("Allow-Origin = %q, want %q", got, "https://example.com")
	}
	if got := rec.Header().Get("Vary"); got != "Origin" {
		t.Errorf("Vary = %q, want %q", got, "Origin")
	}
}

// TestCORSRejectsUnauthorizedOrigin verifies unauthorized origins are rejected
// with 403 for both preflight and actual requests, with no CORS header leaked
// and the downstream handler never run.
func TestCORSRejectsUnauthorizedOrigin(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodOptions} {
		rec, nextCalled := runCORS(method, "https://evil.com", true, "example.com")

		if rec.Code != http.StatusForbidden {
			t.Errorf("%s: status = %d, want %d", method, rec.Code, http.StatusForbidden)
		}
		if nextCalled {
			t.Errorf("%s: next handler should not run for an unauthorized origin", method)
		}
		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
			t.Errorf("%s: unauthorized origin should not receive Allow-Origin, got %q", method, got)
		}
	}
}

// TestCORSNoOriginPassesThrough verifies that requests without an Origin header
// (same-origin, curl, server-to-server) are not rejected when restricted CORS
// is enabled.
func TestCORSNoOriginPassesThrough(t *testing.T) {
	rec, nextCalled := runCORS(http.MethodGet, "", true, "example.com")

	if !nextCalled {
		t.Error("request without an Origin header should pass through in restricted mode")
	}
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("no-Origin request should not receive Allow-Origin, got %q", got)
	}
}
