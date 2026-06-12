package api

import (
	"log"
	"net/http"
	"net/url"

	"github.com/ponzu-cms/ponzu/system/db"
)

// sendPreflight is used to respond to a cross-origin "OPTIONS" request
func sendPreflight(res http.ResponseWriter) {
	res.Header().Set("Access-Control-Allow-Headers", "Accept, Authorization, Content-Type")
	// Access-Control-Allow-Origin is already set by responseWithCORS().
	// Do NOT overwrite it here — doing so would replace a specific domain
	// with "*" in restricted CORS mode, causing browser inconsistencies.
	res.WriteHeader(http.StatusOK)
}

func responseWithCORS(res http.ResponseWriter, req *http.Request) (http.ResponseWriter, bool) {
	if db.ConfigCache("cors_disabled").(bool) == true {
		// check origin matches config domain
		domain := db.ConfigCache("domain").(string)
		origin := req.Header.Get("Origin")
		if origin == "" {
			// No Origin header — not a cross-origin request, pass through
			res.Header().Set("Access-Control-Allow-Headers", "Accept, Authorization, Content-Type")
			res.Header().Set("Access-Control-Allow-Origin", "*")
			return res, true
		}

		u, err := url.Parse(origin)
		if err != nil {
			log.Println("Error parsing URL from request Origin header:", origin)
			return res, false
		}

		// Use Hostname() to compare without port, so that "localhost:8080"
		// matches domain "localhost", and "example.com" matches "example.com"
		// regardless of the port the client connected from.
		originHostname := u.Hostname()

		// currently, this will check for exact match. will need feedback to
		// determine if subdomains should be allowed or allow multiple domains
		// in config
		if originHostname == domain {
			// Reflect the actual Origin header value as ACAO so that the
			// browser receives a valid, fully-qualified origin (including
			// scheme and port) instead of a bare hostname.
			res.Header().Set("Access-Control-Allow-Headers", "Accept, Authorization, Content-Type")
			res.Header().Set("Access-Control-Allow-Origin", origin)
			return res, true
		}

		// disallow request
		res.WriteHeader(http.StatusForbidden)
		return res, false
	}

	// apply full CORS headers and return
	res.Header().Set("Access-Control-Allow-Headers", "Accept, Authorization, Content-Type")
	res.Header().Set("Access-Control-Allow-Origin", "*")

	return res, true
}

// CORS wraps a HandlerFunc to respond to OPTIONS requests properly
func CORS(next http.HandlerFunc) http.HandlerFunc {
	return db.CacheControl(http.HandlerFunc(func(res http.ResponseWriter, req *http.Request) {
		res, cors := responseWithCORS(res, req)
		if !cors {
			return
		}

		if req.Method == http.MethodOptions {
			sendPreflight(res)
			return
		}

		next.ServeHTTP(res, req)
	}))
}
