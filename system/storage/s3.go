package storage

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// S3Storage implements Storage by writing files to an S3-compatible object
// store (AWS S3, MinIO, Cloudflare R2, DigitalOcean Spaces, etc.) using
// AWS Signature Version 4 for request authentication.
type S3Storage struct {
	bucket    string
	region    string
	accessKey string
	secretKey string
	endpoint  string // custom endpoint URL (optional)
	host      string // resolved host for HTTP requests
	baseURL   string // base URL for constructing object URLs
	keyPrefix string // prefix for all object keys (e.g. "uploads")
}

// NewS3Storage creates a new S3-compatible storage backend from the given config.
func NewS3Storage(cfg Config) *S3Storage {
	var host, baseURL string

	if cfg.Endpoint != "" {
		// Custom endpoint for MinIO, R2, Spaces, etc.
		if strings.HasPrefix(cfg.Endpoint, "http://") || strings.HasPrefix(cfg.Endpoint, "https://") {
			u, err := url.Parse(cfg.Endpoint)
			if err != nil {
				// Fallback: treat as https host
				host = cfg.Endpoint
				baseURL = "https://" + cfg.Endpoint
			} else {
				host = u.Host
				baseURL = strings.TrimSuffix(cfg.Endpoint, "/")
			}
		} else {
			host = cfg.Endpoint
			baseURL = "https://" + cfg.Endpoint
		}
	} else {
		// Standard AWS S3 virtual-hosted style
		host = fmt.Sprintf("%s.s3.%s.amazonaws.com", cfg.Bucket, cfg.Region)
		baseURL = fmt.Sprintf("https://%s", host)
	}

	return &S3Storage{
		bucket:    cfg.Bucket,
		region:    cfg.Region,
		accessKey: cfg.AccessKey,
		secretKey: cfg.SecretKey,
		endpoint:  cfg.Endpoint,
		host:      host,
		baseURL:   baseURL,
		keyPrefix: "uploads",
	}
}

// Store uploads the content from r to S3 at a key like "uploads/YYYY/MM/filename"
// and returns the full HTTPS URL to the object.
func (s *S3Storage) Store(r io.Reader, filename string, contentType string) (string, error) {
	now := time.Now()
	year := fmt.Sprintf("%d", now.Year())
	month := fmt.Sprintf("%02d", now.Month())

	key := fmt.Sprintf("%s/%s/%s/%s", s.keyPrefix, year, month, filename)

	// Handle duplicate keys: check if object exists, prepend timestamp if so
	if s.objectExists(key) {
		key = fmt.Sprintf("%s/%s/%s/%d-%s", s.keyPrefix, year, month, time.Now().Unix(), filename)
	}

	objURL := s.keyToURL(key)

	req, err := http.NewRequest(http.MethodPut, objURL, r)
	if err != nil {
		return "", fmt.Errorf("failed to create S3 PUT request: %w", err)
	}

	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}

	s.signRequest(req, "s3")

	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to upload to S3: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		body, _ := ioutil.ReadAll(resp.Body)
		return "", fmt.Errorf("S3 upload failed with status %d: %s", resp.StatusCode, string(body))
	}

	return objURL, nil
}

// Delete removes an object from S3. The path can be a full URL (as returned
// by Store) or a relative key path.
func (s *S3Storage) Delete(path string) error {
	var key string
	if strings.HasPrefix(path, "http://") || strings.HasPrefix(path, "https://") {
		u, err := url.Parse(path)
		if err != nil {
			return fmt.Errorf("failed to parse S3 URL for deletion: %w", err)
		}
		key = strings.TrimPrefix(u.Path, "/")
	} else {
		key = strings.TrimPrefix(path, "/")
	}

	objURL := s.keyToURL(key)

	req, err := http.NewRequest(http.MethodDelete, objURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create S3 DELETE request: %w", err)
	}

	s.signRequest(req, "s3")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to delete from S3: %w", err)
	}
	defer resp.Body.Close()

	// S3 returns 204 on successful delete; 404 means already gone (also OK)
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusNotFound && resp.StatusCode != http.StatusOK {
		body, _ := ioutil.ReadAll(resp.Body)
		return fmt.Errorf("S3 delete failed with status %d: %s", resp.StatusCode, string(body))
	}

	return nil
}

// IsObjectStorage returns true for S3 storage.
func (s *S3Storage) IsObjectStorage() bool {
	return true
}

// Host returns the resolved host used for HTTP requests. Useful for tests
// and for constructing URLs outside this package.
func (s *S3Storage) Host() string {
	return s.host
}

// Bucket returns the configured bucket name.
func (s *S3Storage) Bucket() string {
	return s.bucket
}

// objectExists checks whether an object with the given key already exists
// in the bucket via a HEAD request.
func (s *S3Storage) objectExists(key string) bool {
	objURL := s.keyToURL(key)
	req, err := http.NewRequest(http.MethodHead, objURL, nil)
	if err != nil {
		return false
	}

	s.signRequest(req, "s3")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()

	return resp.StatusCode == http.StatusOK
}

// keyToURL constructs the full HTTPS URL for an object key.
func (s *S3Storage) keyToURL(key string) string {
	return fmt.Sprintf("%s/%s", s.baseURL, urlEncodePath(key))
}

// urlEncodePath URL-encodes each segment of a path individually, preserving
// the "/" separators.
func urlEncodePath(path string) string {
	segments := strings.Split(path, "/")
	encoded := make([]string, len(segments))
	for i, seg := range segments {
		encoded[i] = awsURLEncode(seg)
	}
	return strings.Join(encoded, "/")
}

// ---------------------------------------------------------------------------
// AWS Signature Version 4
// ---------------------------------------------------------------------------

// signRequest adds AWS Signature Version 4 authentication headers to req.
func (s *S3Storage) signRequest(req *http.Request, service string) {
	now := s3Now()
	datestamp := now.Format("20060102")
	amzDate := now.Format("20060102T150405Z")

	// Read and replace body so we can compute its hash
	var payloadHash string
	if req.Body != nil {
		body, err := ioutil.ReadAll(req.Body)
		if err != nil {
			body = []byte{}
		}
		req.Body.Close()
		payloadHash = hashSHA256(body)
		req.Body = ioutil.NopCloser(strings.NewReader(string(body)))
	} else {
		payloadHash = hashSHA256([]byte{})
	}

	req.Header.Set("x-amz-date", amzDate)
	req.Header.Set("x-amz-content-sha256", payloadHash)
	if req.Host == "" {
		req.Host = s.host
	}

	// Collect and sort headers for signing
	signedHeaderKeys := []string{"host", "x-amz-content-sha256", "x-amz-date"}
	if req.Header.Get("Content-Type") != "" {
		signedHeaderKeys = append(signedHeaderKeys, "content-type")
	}
	sort.Strings(signedHeaderKeys)

	signedHeaders := strings.Join(signedHeaderKeys, ";")

	// Build canonical headers
	var canonHeaders strings.Builder
	for _, k := range signedHeaderKeys {
		val := getHeaderValue(req, k)
		canonHeaders.WriteString(strings.ToLower(k))
		canonHeaders.WriteString(":")
		canonHeaders.WriteString(strings.TrimSpace(val))
		canonHeaders.WriteString("\n")
	}

	canonicalURI := canonicalizeURI(req.URL.Path)
	canonicalQueryString := req.URL.Query().Encode()

	canonicalRequest := strings.Join([]string{
		req.Method,
		canonicalURI,
		canonicalQueryString,
		canonHeaders.String(),
		signedHeaders,
		payloadHash,
	}, "\n")

	// String to sign
	credentialScope := fmt.Sprintf("%s/%s/%s/aws4_request", datestamp, s.region, service)
	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256",
		amzDate,
		credentialScope,
		hashSHA256([]byte(canonicalRequest)),
	}, "\n")

	// Signing key
	signingKey := deriveSigningKey(s.secretKey, datestamp, s.region, service)
	signature := hex.EncodeToString(hmacSHA256(signingKey, []byte(stringToSign)))

	// Authorization header
	req.Header.Set("Authorization", fmt.Sprintf(
		"AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		s.accessKey, credentialScope, signedHeaders, signature,
	))
}

// getHeaderValue returns the value for a header key, handling "host" specially
// since it lives on the request struct, not in the Header map.
func getHeaderValue(req *http.Request, key string) string {
	if key == "host" {
		h := req.Host
		if h == "" {
			h = req.Header.Get("Host")
		}
		return h
	}
	return req.Header.Get(key)
}

// deriveSigningKey builds the AWS Sig V4 signing key:
//
//	HMAC(HMAC(HMAC(HMAC("AWS4"+secret, date), region), service), "aws4_request")
func deriveSigningKey(secretKey, datestamp, region, service string) []byte {
	kDate := hmacSHA256([]byte("AWS4"+secretKey), []byte(datestamp))
	kRegion := hmacSHA256(kDate, []byte(region))
	kService := hmacSHA256(kRegion, []byte(service))
	kSigning := hmacSHA256(kService, []byte("aws4_request"))
	return kSigning
}

// canonicalizeURI returns the URI-encoded form of the path, encoding each
// segment individually and preserving "/" separators.
func canonicalizeURI(path string) string {
	if path == "" {
		return "/"
	}
	return urlEncodePath(path)
}

func hashSHA256(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

func hmacSHA256(key, data []byte) []byte {
	h := hmac.New(sha256.New, key)
	h.Write(data)
	return h.Sum(nil)
}

// awsURLEncode encodes a string according to AWS Sig V4 rules:
// all characters except unreserved characters (A-Z, a-z, 0-9, '-', '.', '_', '~')
// are percent-encoded.
func awsURLEncode(s string) string {
	return strings.Replace(url.QueryEscape(s), "+", "%20", -1)
}

// s3Now returns the current time in UTC. It can be overridden in tests via
// the S3NowFunc package variable.
var S3NowFunc = func() time.Time { return time.Now().UTC() }

func s3Now() time.Time {
	return S3NowFunc()
}

// SetNowFunc overrides the time source used by S3 request signing. This is
// intended for deterministic testing only. Pass nil to restore the default.
func SetNowFunc(f func() time.Time) {
	if f == nil {
		S3NowFunc = func() time.Time { return time.Now().UTC() }
	} else {
		S3NowFunc = f
	}
}

