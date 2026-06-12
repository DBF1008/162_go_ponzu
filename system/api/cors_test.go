package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ponzu-cms/ponzu/system/db"
)

// setupTestConfig sets config cache values for testing without BoltDB.
// It configures both CORS-related and CacheControl-related settings so
// that the full CORS() middleware chain can execute without panicking
// on nil type assertions.
func setupTestConfig(corsDisabled bool, domain string) {
	// CORS settings
	db.SetTestConfigCache("cors_disabled", corsDisabled)
	db.SetTestConfigCache("domain", domain)
	// CacheControl middleware settings (read by db.CacheControl inside CORS())
	db.SetTestConfigCache("cache_disabled", true)
}

func TestCORS_RestrictedPreflight(t *testing.T) {
	setupTestConfig(true, "example.com")

	handler := CORS(http.HandlerFunc(func(res http.ResponseWriter, req *http.Request) {
		t.Error("next handler should NOT be called for OPTIONS request")
	}))

	req := httptest.NewRequest(http.MethodOptions, "/api/content", nil)
	req.Header.Set("Origin", "https://example.com")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	acao := rr.Header().Get("Access-Control-Allow-Origin")
	if acao != "https://example.com" {
		t.Errorf("expected ACAO %q, got %q", "https://example.com", acao)
	}

	acah := rr.Header().Get("Access-Control-Allow-Headers")
	if acah != "Accept, Authorization, Content-Type" {
		t.Errorf("expected Allow-Headers %q, got %q", "Accept, Authorization, Content-Type", acah)
	}
}

func TestCORS_RestrictedActualRequest(t *testing.T) {
	setupTestConfig(true, "example.com")

	nextCalled := false
	handler := CORS(http.HandlerFunc(func(res http.ResponseWriter, req *http.Request) {
		nextCalled = true
		res.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/content?type=Post&id=1", nil)
	req.Header.Set("Origin", "https://example.com")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if !nextCalled {
		t.Error("next handler should have been called for GET request")
	}

	acao := rr.Header().Get("Access-Control-Allow-Origin")
	if acao != "https://example.com" {
		t.Errorf("expected ACAO %q, got %q", "https://example.com", acao)
	}
}

func TestCORS_PreflightAndActualConsistency(t *testing.T) {
	setupTestConfig(true, "example.com")

	nextHandler := http.HandlerFunc(func(res http.ResponseWriter, req *http.Request) {
		res.WriteHeader(http.StatusOK)
	})
	handler := CORS(nextHandler)

	origin := "https://example.com"

	// Send preflight OPTIONS
	preflightReq := httptest.NewRequest(http.MethodOptions, "/api/content", nil)
	preflightReq.Header.Set("Origin", origin)
	preflightRR := httptest.NewRecorder()
	handler.ServeHTTP(preflightRR, preflightReq)

	// Send actual GET
	actualReq := httptest.NewRequest(http.MethodGet, "/api/content?type=Post", nil)
	actualReq.Header.Set("Origin", origin)
	actualRR := httptest.NewRecorder()
	handler.ServeHTTP(actualRR, actualReq)

	preflightACAO := preflightRR.Header().Get("Access-Control-Allow-Origin")
	actualACAO := actualRR.Header().Get("Access-Control-Allow-Origin")

	if preflightACAO != actualACAO {
		t.Errorf("ACAO mismatch between preflight and actual: preflight=%q actual=%q", preflightACAO, actualACAO)
	}

	if preflightACAO != origin {
		t.Errorf("expected ACAO %q for both, got preflight=%q actual=%q", origin, preflightACAO, actualACAO)
	}
}

func TestCORS_UnauthorizedOrigin(t *testing.T) {
	setupTestConfig(true, "example.com")

	handler := CORS(http.HandlerFunc(func(res http.ResponseWriter, req *http.Request) {
		t.Error("next handler should NOT be called for unauthorized origin")
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/content", nil)
	req.Header.Set("Origin", "https://evil.com")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("expected status 403 for unauthorized origin, got %d", rr.Code)
	}

	acao := rr.Header().Get("Access-Control-Allow-Origin")
	if acao != "" {
		t.Errorf("expected no ACAO header for rejected request, got %q", acao)
	}
}

func TestCORS_UnauthorizedOriginPreflight(t *testing.T) {
	setupTestConfig(true, "example.com")

	handler := CORS(http.HandlerFunc(func(res http.ResponseWriter, req *http.Request) {
		t.Error("next handler should NOT be called for unauthorized preflight")
	}))

	req := httptest.NewRequest(http.MethodOptions, "/api/content", nil)
	req.Header.Set("Origin", "https://evil.com")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("expected status 403 for unauthorized preflight, got %d", rr.Code)
	}

	acao := rr.Header().Get("Access-Control-Allow-Origin")
	if acao != "" {
		t.Errorf("expected no ACAO header for rejected preflight, got %q", acao)
	}
}

func TestCORS_OpenMode(t *testing.T) {
	setupTestConfig(false, "example.com") // cors_disabled=false → open CORS

	handler := CORS(http.HandlerFunc(func(res http.ResponseWriter, req *http.Request) {
		res.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/content", nil)
	req.Header.Set("Origin", "https://any-site.com")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	acao := rr.Header().Get("Access-Control-Allow-Origin")
	if acao != "*" {
		t.Errorf("expected ACAO %q in open mode, got %q", "*", acao)
	}
}

func TestCORS_OpenModePreflight(t *testing.T) {
	setupTestConfig(false, "") // cors_disabled=false → open CORS

	handler := CORS(http.HandlerFunc(func(res http.ResponseWriter, req *http.Request) {
		t.Error("next handler should NOT be called for OPTIONS in open mode")
	}))

	req := httptest.NewRequest(http.MethodOptions, "/api/content", nil)
	req.Header.Set("Origin", "https://any-site.com")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	acao := rr.Header().Get("Access-Control-Allow-Origin")
	if acao != "*" {
		t.Errorf("expected ACAO %q in open preflight, got %q", "*", acao)
	}
}

func TestCORS_LocalhostDevEnvironment(t *testing.T) {
	setupTestConfig(true, "localhost")

	handler := CORS(http.HandlerFunc(func(res http.ResponseWriter, req *http.Request) {
		res.WriteHeader(http.StatusOK)
	}))

	// Preflight from localhost with port
	preflightReq := httptest.NewRequest(http.MethodOptions, "/api/content", nil)
	preflightReq.Header.Set("Origin", "http://localhost:8080")
	preflightRR := httptest.NewRecorder()
	handler.ServeHTTP(preflightRR, preflightReq)

	if preflightRR.Code != http.StatusOK {
		t.Errorf("preflight: expected status 200, got %d", preflightRR.Code)
	}

	acao := preflightRR.Header().Get("Access-Control-Allow-Origin")
	if acao != "http://localhost:8080" {
		t.Errorf("preflight: expected ACAO %q, got %q", "http://localhost:8080", acao)
	}

	// Actual request from localhost with port
	actualReq := httptest.NewRequest(http.MethodGet, "/api/content", nil)
	actualReq.Header.Set("Origin", "http://localhost:8080")
	actualRR := httptest.NewRecorder()
	handler.ServeHTTP(actualRR, actualReq)

	actualACAO := actualRR.Header().Get("Access-Control-Allow-Origin")
	if actualACAO != "http://localhost:8080" {
		t.Errorf("actual: expected ACAO %q, got %q", "http://localhost:8080", actualACAO)
	}

	// Consistency check
	if acao != actualACAO {
		t.Errorf("preflight/actual ACAO mismatch on localhost: preflight=%q actual=%q", acao, actualACAO)
	}
}

func TestCORS_LocalhostUnauthorizedOrigin(t *testing.T) {
	setupTestConfig(true, "localhost")

	handler := CORS(http.HandlerFunc(func(res http.ResponseWriter, req *http.Request) {
		t.Error("next handler should NOT be called for non-localhost origin")
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/content", nil)
	req.Header.Set("Origin", "https://evil.com")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("expected status 403 for non-localhost origin, got %d", rr.Code)
	}
}

func TestCORS_DomainWithPort(t *testing.T) {
	// When the configured domain is just a hostname (no port),
	// requests from that hostname with any port should match.
	setupTestConfig(true, "example.com")

	handler := CORS(http.HandlerFunc(func(res http.ResponseWriter, req *http.Request) {
		res.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/content", nil)
	req.Header.Set("Origin", "https://example.com:8443")
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200 for port-bearing origin, got %d", rr.Code)
	}

	acao := rr.Header().Get("Access-Control-Allow-Origin")
	if acao != "https://example.com:8443" {
		t.Errorf("expected ACAO %q, got %q", "https://example.com:8443", acao)
	}
}

func TestCORS_NoOriginHeader(t *testing.T) {
	setupTestConfig(true, "example.com")

	nextCalled := false
	handler := CORS(http.HandlerFunc(func(res http.ResponseWriter, req *http.Request) {
		nextCalled = true
		res.WriteHeader(http.StatusOK)
	}))

	// Request without Origin header (non-browser client / same-origin)
	req := httptest.NewRequest(http.MethodGet, "/api/content", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if !nextCalled {
		t.Error("next handler should be called when no Origin header is present")
	}
}
