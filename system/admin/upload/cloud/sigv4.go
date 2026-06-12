package cloud

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// This file implements AWS Signature Version 4 request signing using only the
// standard library, so the S3-compatible backend needs no third-party SDK. It
// works with AWS S3 and S3-compatible services (MinIO, DigitalOcean Spaces,
// Wasabi, etc.) using path-style addressing.

const (
	sigV4Algorithm  = "AWS4-HMAC-SHA256"
	amzDateFormat   = "20060102T150405Z"
	shortDateFormat = "20060102"
	unsignedPayload = "UNSIGNED-PAYLOAD"
)

func hmacSHA256(key []byte, data string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(data))
	return h.Sum(nil)
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// signV4 signs req in place with AWS Signature Version 4, setting the
// X-Amz-Date and Authorization headers. payloadHash must be the hex SHA-256 of
// the request body, or the literal "UNSIGNED-PAYLOAD" for streamed bodies.
//
// The signed header set is: host, content-type (when present), and every
// x-amz-* header already on the request. Callers must therefore set any
// required x-amz-* headers (e.g. X-Amz-Content-Sha256, X-Amz-Acl) before
// calling signV4.
func signV4(req *http.Request, region, service, accessKey, secretKey, payloadHash string, t time.Time) {
	t = t.UTC()
	amzDate := t.Format(amzDateFormat)
	shortDate := t.Format(shortDateFormat)
	req.Header.Set("X-Amz-Date", amzDate)

	// Collect the headers to sign: host + content-type + all x-amz-* headers.
	signed := map[string]string{"host": req.URL.Host}
	if ct := strings.TrimSpace(req.Header.Get("Content-Type")); ct != "" {
		signed["content-type"] = ct
	}
	for name, values := range req.Header {
		lower := strings.ToLower(name)
		if strings.HasPrefix(lower, "x-amz-") {
			signed[lower] = strings.TrimSpace(strings.Join(values, ","))
		}
	}

	names := make([]string, 0, len(signed))
	for name := range signed {
		names = append(names, name)
	}
	sort.Strings(names)

	var canonicalHeaders strings.Builder
	for _, name := range names {
		canonicalHeaders.WriteString(name)
		canonicalHeaders.WriteByte(':')
		canonicalHeaders.WriteString(signed[name])
		canonicalHeaders.WriteByte('\n')
	}
	signedHeaders := strings.Join(names, ";")

	canonicalURI := req.URL.EscapedPath()
	if canonicalURI == "" {
		canonicalURI = "/"
	}

	canonicalRequest := strings.Join([]string{
		req.Method,
		canonicalURI,
		canonicalQueryString(req.URL.Query()),
		canonicalHeaders.String(),
		signedHeaders,
		payloadHash,
	}, "\n")

	scope := strings.Join([]string{shortDate, region, service, "aws4_request"}, "/")
	stringToSign := strings.Join([]string{
		sigV4Algorithm,
		amzDate,
		scope,
		sha256Hex([]byte(canonicalRequest)),
	}, "\n")

	kDate := hmacSHA256([]byte("AWS4"+secretKey), shortDate)
	kRegion := hmacSHA256(kDate, region)
	kService := hmacSHA256(kRegion, service)
	kSigning := hmacSHA256(kService, "aws4_request")
	signature := hex.EncodeToString(hmacSHA256(kSigning, stringToSign))

	authorization := fmt.Sprintf(
		"%s Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		sigV4Algorithm, accessKey, scope, signedHeaders, signature,
	)
	req.Header.Set("Authorization", authorization)
}

// canonicalQueryString builds the SigV4 canonical query string: keys and values
// RFC 3986 encoded, sorted by key (then value), joined with '&'.
func canonicalQueryString(values url.Values) string {
	if len(values) == 0 {
		return ""
	}

	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	var parts []string
	for _, key := range keys {
		vals := append([]string(nil), values[key]...)
		sort.Strings(vals)
		for _, val := range vals {
			parts = append(parts, uriEncode(key, true)+"="+uriEncode(val, true))
		}
	}
	return strings.Join(parts, "&")
}

// uriEncode applies RFC 3986 percent-encoding as required by SigV4. Unreserved
// characters are left as-is; everything else is encoded. '/' is encoded unless
// encodeSlash is false.
func uriEncode(s string, encodeSlash bool) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') ||
			(c >= '0' && c <= '9') || c == '-' || c == '.' || c == '_' || c == '~':
			b.WriteByte(c)
		case c == '/' && !encodeSlash:
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}
