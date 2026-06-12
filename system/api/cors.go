package api

import (
	"log"
	"net/http"
	"net/url"

	"github.com/ponzu-cms/ponzu/system/db"
)

// sendPreflight responds to a cross-origin "OPTIONS" preflight request. The CORS
// headers are already set by applyCORS before this is called, so they are
// intentionally not re-set here. Re-setting Access-Control-Allow-Origin in the
// preflight was the cause of the preflight and the actual response advertising
// different origins.
func sendPreflight(res http.ResponseWriter) {
	res.WriteHeader(http.StatusOK)
}

// applyCORS sets the appropriate CORS headers on res based on the configured
// policy and the request's Origin, returning res and whether the request is
// allowed to proceed. The configuration is passed in (rather than read from the
// config cache here) so the behavior can be exercised directly in tests.
//
// When corsRestricted is true, cross-origin access is limited to the single
// configured domain; otherwise any origin is allowed.
func applyCORS(res http.ResponseWriter, req *http.Request, corsRestricted bool, domain string) (http.ResponseWriter, bool) {
	if !corsRestricted {
		// apply full CORS headers and return
		res.Header().Set("Access-Control-Allow-Headers", "Accept, Authorization, Content-Type")
		res.Header().Set("Access-Control-Allow-Origin", "*")
		return res, true
	}

	origin := req.Header.Get("Origin")

	// A request without an Origin header is not a cross-origin browser request
	// (e.g. same-origin requests, curl, or server-to-server calls). CORS does
	// not apply to it, so let it through without CORS headers rather than
	// rejecting it.
	if origin == "" {
		return res, true
	}

	u, err := url.Parse(origin)
	if err != nil {
		log.Println("Error parsing URL from request Origin header:", origin)
		res.WriteHeader(http.StatusForbidden)
		return res, false
	}

	if !originAllowed(u, domain) {
		// disallow request from an unauthorized origin
		res.WriteHeader(http.StatusForbidden)
		return res, false
	}

	// Echo back the exact Origin the client sent. Using the request Origin
	// (which includes the scheme, e.g. "https://example.com") instead of the
	// bare configured domain ensures the value is one the browser will accept,
	// and guarantees the preflight and the actual response advertise an
	// identical Access-Control-Allow-Origin.
	res.Header().Set("Access-Control-Allow-Headers", "Accept, Authorization, Content-Type")
	res.Header().Set("Access-Control-Allow-Origin", origin)
	// The response now depends on the Origin, so caches must key on it to avoid
	// serving one origin's CORS headers to another.
	res.Header().Set("Vary", "Origin")

	return res, true
}

// originAllowed reports whether the request Origin is permitted under the
// configured domain when restricted CORS is enabled.
func originAllowed(origin *url.URL, domain string) bool {
	// Local development: when the domain is configured as "localhost", allow any
	// localhost origin regardless of the port the dev server runs on.
	if domain == "localhost" {
		return origin.Hostname() == "localhost"
	}

	// Otherwise require an exact host match with the configured domain. This
	// matches a default-port origin such as "https://example.com" (whose host
	// is "example.com") and rejects other hosts, subdomains, and ports.
	return origin.Host == domain
}

// corsHandler applies CORS to the request and dispatches it: an unauthorized
// origin is short-circuited, an OPTIONS preflight request is answered directly,
// and any other request is passed to next.
func corsHandler(res http.ResponseWriter, req *http.Request, next http.HandlerFunc, corsRestricted bool, domain string) {
	res, allowed := applyCORS(res, req, corsRestricted, domain)
	if !allowed {
		return
	}

	if req.Method == http.MethodOptions {
		sendPreflight(res)
		return
	}

	next.ServeHTTP(res, req)
}

// CORS wraps a HandlerFunc to apply CORS headers and to respond to OPTIONS
// preflight requests according to the configured CORS policy.
func CORS(next http.HandlerFunc) http.HandlerFunc {
	return db.CacheControl(http.HandlerFunc(func(res http.ResponseWriter, req *http.Request) {
		// DisableCORS == true restricts cross-origin access to the single
		// configured domain; false allows any origin.
		corsRestricted := db.ConfigCache("cors_disabled").(bool)
		domain := db.ConfigCache("domain").(string)

		corsHandler(res, req, next, corsRestricted, domain)
	}))
}
