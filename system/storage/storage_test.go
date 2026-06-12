package storage

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Config tests
// ---------------------------------------------------------------------------

func TestConfigFromEnv_NoS3Vars(t *testing.T) {
	// Clear any S3 env vars that might be set
	os.Unsetenv("PONZU_S3_BUCKET")
	os.Unsetenv("PONZU_S3_REGION")
	os.Unsetenv("PONZU_S3_ACCESS_KEY")
	os.Unsetenv("PONZU_S3_SECRET_KEY")
	os.Unsetenv("PONZU_S3_ENDPOINT")

	cfg := ConfigFromEnv()
	if cfg.IsConfigured() {
		t.Error("expected IsConfigured() to be false when no env vars are set")
	}
}

func TestConfigFromEnv_AllS3Vars(t *testing.T) {
	os.Setenv("PONZU_S3_BUCKET", "my-bucket")
	os.Setenv("PONZU_S3_REGION", "us-east-1")
	os.Setenv("PONZU_S3_ACCESS_KEY", "AKID123")
	os.Setenv("PONZU_S3_SECRET_KEY", "secret456")
	os.Setenv("PONZU_S3_ENDPOINT", "https://s3.example.com")
	defer func() {
		os.Unsetenv("PONZU_S3_BUCKET")
		os.Unsetenv("PONZU_S3_REGION")
		os.Unsetenv("PONZU_S3_ACCESS_KEY")
		os.Unsetenv("PONZU_S3_SECRET_KEY")
		os.Unsetenv("PONZU_S3_ENDPOINT")
	}()

	cfg := ConfigFromEnv()
	if !cfg.IsConfigured() {
		t.Error("expected IsConfigured() to be true when all env vars are set")
	}
	if cfg.Bucket != "my-bucket" {
		t.Errorf("expected Bucket=my-bucket, got %s", cfg.Bucket)
	}
	if cfg.Region != "us-east-1" {
		t.Errorf("expected Region=us-east-1, got %s", cfg.Region)
	}
	if cfg.AccessKey != "AKID123" {
		t.Errorf("expected AccessKey=AKID123, got %s", cfg.AccessKey)
	}
	if cfg.SecretKey != "secret456" {
		t.Errorf("expected SecretKey=secret456, got %s", cfg.SecretKey)
	}
	if cfg.Endpoint != "https://s3.example.com" {
		t.Errorf("expected Endpoint=https://s3.example.com, got %s", cfg.Endpoint)
	}
}

func TestConfigFromEnv_PartialVars(t *testing.T) {
	// Only bucket and region set — not enough
	os.Setenv("PONZU_S3_BUCKET", "my-bucket")
	os.Setenv("PONZU_S3_REGION", "us-east-1")
	os.Unsetenv("PONZU_S3_ACCESS_KEY")
	os.Unsetenv("PONZU_S3_SECRET_KEY")
	defer func() {
		os.Unsetenv("PONZU_S3_BUCKET")
		os.Unsetenv("PONZU_S3_REGION")
	}()

	cfg := ConfigFromEnv()
	if cfg.IsConfigured() {
		t.Error("expected IsConfigured() to be false when only partial env vars are set")
	}
}

// ---------------------------------------------------------------------------
// New() factory tests
// ---------------------------------------------------------------------------

func TestNew_DefaultIsLocal(t *testing.T) {
	os.Unsetenv("PONZU_S3_BUCKET")
	os.Unsetenv("PONZU_S3_REGION")
	os.Unsetenv("PONZU_S3_ACCESS_KEY")
	os.Unsetenv("PONZU_S3_SECRET_KEY")

	s := New()
	if s.IsObjectStorage() {
		t.Error("expected local storage when S3 not configured")
	}
	if _, ok := s.(*LocalStorage); !ok {
		t.Error("expected *LocalStorage type")
	}
}

func TestNew_S3Configured(t *testing.T) {
	os.Setenv("PONZU_S3_BUCKET", "test-bucket")
	os.Setenv("PONZU_S3_REGION", "us-east-1")
	os.Setenv("PONZU_S3_ACCESS_KEY", "AKID")
	os.Setenv("PONZU_S3_SECRET_KEY", "SECRET")
	defer func() {
		os.Unsetenv("PONZU_S3_BUCKET")
		os.Unsetenv("PONZU_S3_REGION")
		os.Unsetenv("PONZU_S3_ACCESS_KEY")
		os.Unsetenv("PONZU_S3_SECRET_KEY")
	}()

	s := New()
	if !s.IsObjectStorage() {
		t.Error("expected S3 storage when configured")
	}
	if _, ok := s.(*S3Storage); !ok {
		t.Error("expected *S3Storage type")
	}
}

// ---------------------------------------------------------------------------
// LocalStorage tests
// ---------------------------------------------------------------------------

func TestLocalStorage_StoreAndDelete(t *testing.T) {
	tmpDir, err := ioutil.TempDir("", "ponzu-test-uploads")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	os.Setenv("PONZU_UPLOAD_DIR", tmpDir)
	defer os.Unsetenv("PONZU_UPLOAD_DIR")

	local := &LocalStorage{}
	content := "hello world"

	path, err := local.Store(strings.NewReader(content), "test.txt", "text/plain")
	if err != nil {
		t.Fatalf("Store failed: %v", err)
	}

	// Path format: /api/uploads/YYYY/MM/test.txt
	if !strings.HasPrefix(path, "/api/uploads/") {
		t.Errorf("expected path to start with /api/uploads/, got %s", path)
	}
	if !strings.HasSuffix(path, "/test.txt") {
		t.Errorf("expected path to end with /test.txt, got %s", path)
	}

	// Verify the file was actually written.
	// cfg.UploadDir() = tmpDir, so the file lives at tmpDir/YYYY/MM/test.txt.
	// Strip "/api/uploads/" from the returned path to get the relative portion.
	relPath := strings.TrimPrefix(path, "/api/uploads/")
	fullPath := filepath.Join(tmpDir, filepath.FromSlash(relPath))
	data, err := ioutil.ReadFile(fullPath)
	if err != nil {
		t.Fatalf("failed to read stored file: %v", err)
	}
	if string(data) != content {
		t.Errorf("expected content %q, got %q", content, string(data))
	}

	// Delete the file
	err = local.Delete(path)
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	if _, err := os.Stat(fullPath); !os.IsNotExist(err) {
		t.Error("expected file to be deleted")
	}
}

func TestLocalStorage_DuplicateFilename(t *testing.T) {
	tmpDir, err := ioutil.TempDir("", "ponzu-test-uploads")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	os.Setenv("PONZU_UPLOAD_DIR", tmpDir)
	defer os.Unsetenv("PONZU_UPLOAD_DIR")

	local := &LocalStorage{}

	path1, err := local.Store(strings.NewReader("first"), "dup.txt", "text/plain")
	if err != nil {
		t.Fatalf("first Store failed: %v", err)
	}

	path2, err := local.Store(strings.NewReader("second"), "dup.txt", "text/plain")
	if err != nil {
		t.Fatalf("second Store failed: %v", err)
	}

	if path1 == path2 {
		t.Error("expected different paths for duplicate filenames")
	}
	if !strings.HasSuffix(path1, "/dup.txt") {
		t.Errorf("first path should end with /dup.txt, got %s", path1)
	}
	// Second path should have a timestamp prefix: /<digits>-dup.txt
	secondTail := path2[strings.LastIndex(path2, "/")+1:]
	if secondTail == "dup.txt" {
		t.Error("second file should have a timestamp prefix for duplicate name")
	}
}

func TestLocalStorage_IsObjectStorage(t *testing.T) {
	local := &LocalStorage{}
	if local.IsObjectStorage() {
		t.Error("LocalStorage should return false for IsObjectStorage")
	}
}

// ---------------------------------------------------------------------------
// S3Storage tests (using httptest mock server)
// ---------------------------------------------------------------------------

func TestS3Storage_Store(t *testing.T) {
	// Fixed time for deterministic signing
	fixedTime := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
	SetNowFunc(func() time.Time { return fixedTime })
	defer SetNowFunc(nil)

	var receivedMethod, receivedPath string
	var receivedAuth, receivedContentType, receivedAmzDate string
	var receivedBody []byte

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedMethod = r.Method
		receivedPath = r.URL.Path
		receivedAuth = r.Header.Get("Authorization")
		receivedContentType = r.Header.Get("Content-Type")
		receivedAmzDate = r.Header.Get("X-Amz-Date")
		receivedBody, _ = ioutil.ReadAll(r.Body)

		// HEAD request for objectExists check → 404
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusNotFound)
			return
		}

		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	cfg := Config{
		Bucket:    "test-bucket",
		Region:    "us-east-1",
		AccessKey: "AKIDEXAMPLE",
		SecretKey: "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY",
		Endpoint:  ts.URL,
	}

	s := NewS3Storage(cfg)
	content := "test file content"

	path, err := s.Store(strings.NewReader(content), "test.txt", "text/plain")
	if err != nil {
		t.Fatalf("Store failed: %v", err)
	}

	if receivedMethod != http.MethodPut {
		t.Errorf("expected PUT method, got %s", receivedMethod)
	}
	if !strings.Contains(receivedPath, "uploads/") {
		t.Errorf("expected path to contain 'uploads/', got %s", receivedPath)
	}
	if !strings.Contains(receivedPath, "/test.txt") {
		t.Errorf("expected path to contain '/test.txt', got %s", receivedPath)
	}
	if string(receivedBody) != content {
		t.Errorf("expected body %q, got %q", content, string(receivedBody))
	}
	if receivedContentType != "text/plain" {
		t.Errorf("expected Content-Type text/plain, got %s", receivedContentType)
	}

	// Validate Authorization header format
	if !strings.HasPrefix(receivedAuth, "AWS4-HMAC-SHA256 ") {
		t.Errorf("expected AWS4-HMAC-SHA256 auth, got %s", receivedAuth)
	}
	if !strings.Contains(receivedAuth, "Credential=AKIDEXAMPLE/") {
		t.Error("expected credential with access key in auth header")
	}
	if !strings.Contains(receivedAuth, "SignedHeaders=") {
		t.Error("expected SignedHeaders in auth header")
	}
	if !strings.Contains(receivedAuth, "Signature=") {
		t.Error("expected Signature in auth header")
	}
	if receivedAmzDate != "20240115T103000Z" {
		t.Errorf("expected amz date 20240115T103000Z, got %s", receivedAmzDate)
	}

	// Verify URL format: endpoint/YYYY/MM/test.txt (key prefix is "uploads")
	if !strings.HasPrefix(path, ts.URL+"/") {
		t.Errorf("expected path to start with %s/, got %s", ts.URL, path)
	}
}

func TestS3Storage_StoreWithDuplicate(t *testing.T) {
	fixedTime := time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC)
	SetNowFunc(func() time.Time { return fixedTime })
	defer SetNowFunc(nil)

	headCallCount := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			headCallCount++
			// First key exists, second (with timestamp) does not
			if headCallCount == 1 {
				w.WriteHeader(http.StatusOK) // object exists
			} else {
				w.WriteHeader(http.StatusNotFound) // object does not exist
			}
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	cfg := Config{
		Bucket:    "test-bucket",
		Region:    "us-east-1",
		AccessKey: "AKID",
		SecretKey: "SECRET",
		Endpoint:  ts.URL,
	}

	s := NewS3Storage(cfg)

	path, err := s.Store(strings.NewReader("data"), "existing.txt", "text/plain")
	if err != nil {
		t.Fatalf("Store failed: %v", err)
	}

	// Path should contain timestamp prefix because the original key existed
	// Expected pattern: /.../1718452800-existing.txt (the unix timestamp of fixedTime)
	if !strings.Contains(path, "-existing.txt") {
		t.Errorf("expected timestamp-prefixed filename in path, got %s", path)
	}
}

func TestS3Storage_Delete(t *testing.T) {
	fixedTime := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
	SetNowFunc(func() time.Time { return fixedTime })
	defer SetNowFunc(nil)

	var receivedMethod, receivedPath, receivedAuth string

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedMethod = r.Method
		receivedPath = r.URL.Path
		receivedAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer ts.Close()

	cfg := Config{
		Bucket:    "test-bucket",
		Region:    "us-east-1",
		AccessKey: "AKIDEXAMPLE",
		SecretKey: "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY",
		Endpoint:  ts.URL,
	}

	s := NewS3Storage(cfg)

	// Delete using a full URL (as returned by Store)
	deleteURL := ts.URL + "/uploads/2024/01/test.txt"
	err := s.Delete(deleteURL)
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	if receivedMethod != http.MethodDelete {
		t.Errorf("expected DELETE method, got %s", receivedMethod)
	}
	if receivedPath != "/uploads/2024/01/test.txt" {
		t.Errorf("expected path /uploads/2024/01/test.txt, got %s", receivedPath)
	}
	if !strings.HasPrefix(receivedAuth, "AWS4-HMAC-SHA256 ") {
		t.Error("expected AWS4-HMAC-SHA256 auth header")
	}
}

func TestS3Storage_URLFormat_AWS(t *testing.T) {
	cfg := Config{
		Bucket:    "my-bucket",
		Region:    "us-east-1",
		AccessKey: "AKID",
		SecretKey: "SECRET",
	}

	s := NewS3Storage(cfg)

	expectedHost := "my-bucket.s3.us-east-1.amazonaws.com"
	if s.Host() != expectedHost {
		t.Errorf("expected host %s, got %s", expectedHost, s.Host())
	}

	url := s.keyToURL("uploads/2024/01/test.txt")
	expected := "https://my-bucket.s3.us-east-1.amazonaws.com/uploads/2024/01/test.txt"
	if url != expected {
		t.Errorf("expected URL %s, got %s", expected, url)
	}
}

func TestS3Storage_URLFormat_CustomEndpoint(t *testing.T) {
	cfg := Config{
		Bucket:    "my-bucket",
		Region:    "us-east-1",
		AccessKey: "AKID",
		SecretKey: "SECRET",
		Endpoint:  "https://minio.example.com:9000",
	}

	s := NewS3Storage(cfg)

	if s.Host() != "minio.example.com:9000" {
		t.Errorf("expected host minio.example.com:9000, got %s", s.Host())
	}

	url := s.keyToURL("uploads/2024/01/test.txt")
	expected := "https://minio.example.com:9000/uploads/2024/01/test.txt"
	if url != expected {
		t.Errorf("expected URL %s, got %s", expected, url)
	}
}

func TestS3Storage_URLFormat_EndpointWithoutScheme(t *testing.T) {
	cfg := Config{
		Bucket:    "my-bucket",
		Region:    "us-east-1",
		AccessKey: "AKID",
		SecretKey: "SECRET",
		Endpoint:  "minio.local:9000",
	}

	s := NewS3Storage(cfg)

	if s.Host() != "minio.local:9000" {
		t.Errorf("expected host minio.local:9000, got %s", s.Host())
	}

	url := s.keyToURL("uploads/2024/01/test.txt")
	expected := "https://minio.local:9000/uploads/2024/01/test.txt"
	if url != expected {
		t.Errorf("expected URL %s, got %s", expected, url)
	}
}

func TestS3Storage_IsObjectStorage(t *testing.T) {
	cfg := Config{
		Bucket:    "b",
		Region:    "r",
		AccessKey: "a",
		SecretKey: "s",
	}
	s := NewS3Storage(cfg)
	if !s.IsObjectStorage() {
		t.Error("S3Storage should return true for IsObjectStorage")
	}
}

// ---------------------------------------------------------------------------
// AWS Signature V4 unit tests
// ---------------------------------------------------------------------------

func TestAWSSignatureV4_SigningKey(t *testing.T) {
	// Verify signing key derivation produces deterministic results
	key1 := deriveSigningKey("wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY", "20240115", "us-east-1", "s3")
	key2 := deriveSigningKey("wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY", "20240115", "us-east-1", "s3")

	if !hmac.Equal(key1, key2) {
		t.Error("signing key derivation should be deterministic")
	}

	// Different date should produce different key
	key3 := deriveSigningKey("wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY", "20240116", "us-east-1", "s3")
	if hmac.Equal(key1, key3) {
		t.Error("different dates should produce different signing keys")
	}

	// Different region should produce different key
	key4 := deriveSigningKey("wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY", "20240115", "eu-west-1", "s3")
	if hmac.Equal(key1, key4) {
		t.Error("different regions should produce different signing keys")
	}
}

func TestAWSSignatureV4_PayloadHash(t *testing.T) {
	// SHA-256 of empty string is well-known
	emptyHash := hashSHA256([]byte{})
	expected := "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	if emptyHash != expected {
		t.Errorf("expected empty hash %s, got %s", expected, emptyHash)
	}

	// SHA-256 of "hello"
	helloHash := hashSHA256([]byte("hello"))
	expectedHello := "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"
	if helloHash != expectedHello {
		t.Errorf("expected hello hash %s, got %s", expectedHello, helloHash)
	}
}

func TestAWSSignatureV4_HMAC(t *testing.T) {
	key := []byte("secret")
	data := []byte("message")

	result := hmacSHA256(key, data)

	// Verify against a known-good computation
	h := hmac.New(sha256.New, key)
	h.Write(data)
	expected := h.Sum(nil)

	if !hmac.Equal(result, expected) {
		t.Errorf("HMAC mismatch: got %x, expected %x", result, expected)
	}
}

func TestAWSSignatureV4_AuthorizationHeaderFormat(t *testing.T) {
	fixedTime := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
	SetNowFunc(func() time.Time { return fixedTime })
	defer SetNowFunc(nil)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")

		// Must start with algorithm identifier
		if !strings.HasPrefix(auth, "AWS4-HMAC-SHA256 ") {
			t.Errorf("auth should start with AWS4-HMAC-SHA256, got: %s", auth)
		}

		// Must contain credential with access key and date/region/service
		expectedCred := "Credential=AKIDEXAMPLE/20240115/us-east-1/s3/aws4_request"
		if !strings.Contains(auth, expectedCred) {
			t.Errorf("expected credential %q in auth header, got: %s", expectedCred, auth)
		}

		// Must contain SignedHeaders
		if !strings.Contains(auth, "SignedHeaders=content-type;host;x-amz-content-sha256;x-amz-date") {
			t.Errorf("unexpected SignedHeaders in: %s", auth)
		}

		// Must contain a 64-char hex Signature
		sigIdx := strings.Index(auth, "Signature=")
		if sigIdx == -1 {
			t.Error("missing Signature in auth header")
		} else {
			sig := auth[sigIdx+len("Signature="):]
			if len(sig) != 64 {
				t.Errorf("expected 64-char hex signature, got %d chars: %s", len(sig), sig)
			}
			// Verify it's valid hex
			if _, err := hex.DecodeString(sig); err != nil {
				t.Errorf("signature is not valid hex: %s", sig)
			}
		}

		// Verify x-amz-content-sha256 matches the payload
		contentHash := r.Header.Get("X-Amz-Content-Sha256")
		expectedHash := hashSHA256([]byte("test body"))
		if contentHash != expectedHash {
			t.Errorf("expected content hash %s, got %s", expectedHash, contentHash)
		}

		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	cfg := Config{
		Bucket:    "test-bucket",
		Region:    "us-east-1",
		AccessKey: "AKIDEXAMPLE",
		SecretKey: "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY",
		Endpoint:  ts.URL,
	}
	s := NewS3Storage(cfg)

	req, _ := http.NewRequest(http.MethodPut, ts.URL+"/test.txt", strings.NewReader("test body"))
	req.Header.Set("Content-Type", "text/plain")
	s.signRequest(req, "s3")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()
}

func TestAWSSignatureV4_CanonicalizeURI(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"/", "/"},
		{"", "/"},
		{"/uploads/2024/01/test.txt", "/uploads/2024/01/test.txt"},
		{"/path/to/my file.txt", "/path/to/my%20file.txt"},
		{"/uploads/a+b.txt", "/uploads/a%2Bb.txt"},
	}

	for _, tt := range tests {
		result := canonicalizeURI(tt.input)
		if result != tt.expected {
			t.Errorf("canonicalizeURI(%q) = %q, want %q", tt.input, result, tt.expected)
		}
	}
}

func TestAWSEncode(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"hello", "hello"},
		{"hello world", "hello%20world"},
		{"a+b", "a%2Bb"},
		{"file.txt", "file.txt"},
		{"2024", "2024"},
		{"test-file_v2.0", "test-file_v2.0"},
		{"~user", "~user"},
	}

	for _, tt := range tests {
		result := awsURLEncode(tt.input)
		if result != tt.expected {
			t.Errorf("awsURLEncode(%q) = %q, want %q", tt.input, result, tt.expected)
		}
	}
}

// TestS3Storage_SignatureConsistency verifies that signing the same request
// twice with the same time produces identical signatures.
func TestS3Storage_SignatureConsistency(t *testing.T) {
	fixedTime := time.Date(2024, 3, 1, 8, 0, 0, 0, time.UTC)
	SetNowFunc(func() time.Time { return fixedTime })
	defer SetNowFunc(nil)

	cfg := Config{
		Bucket:    "bucket",
		Region:    "us-east-1",
		AccessKey: "AKID",
		SecretKey: "SECRET",
		Endpoint:  "https://s3.example.com",
	}
	s := NewS3Storage(cfg)

	makeReq := func() string {
		req, _ := http.NewRequest(http.MethodGet, "https://s3.example.com/test.txt", nil)
		req.Host = "s3.example.com"
		s.signRequest(req, "s3")
		return req.Header.Get("Authorization")
	}

	auth1 := makeReq()
	auth2 := makeReq()

	if auth1 != auth2 {
		t.Errorf("expected identical signatures for same request and time:\n  %s\n  %s", auth1, auth2)
	}
}

// TestS3Storage_SignatureChangesWithTime verifies that different timestamps
// produce different signatures.
func TestS3Storage_SignatureChangesWithTime(t *testing.T) {
	cfg := Config{
		Bucket:    "bucket",
		Region:    "us-east-1",
		AccessKey: "AKID",
		SecretKey: "SECRET",
		Endpoint:  "https://s3.example.com",
	}
	s := NewS3Storage(cfg)

	SetNowFunc(func() time.Time { return time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC) })
	req1, _ := http.NewRequest(http.MethodGet, "https://s3.example.com/test.txt", nil)
	req1.Host = "s3.example.com"
	s.signRequest(req1, "s3")
	auth1 := req1.Header.Get("Authorization")

	SetNowFunc(func() time.Time { return time.Date(2024, 1, 1, 0, 1, 0, 0, time.UTC) })
	req2, _ := http.NewRequest(http.MethodGet, "https://s3.example.com/test.txt", nil)
	req2.Host = "s3.example.com"
	s.signRequest(req2, "s3")
	auth2 := req2.Header.Get("Authorization")

	SetNowFunc(nil) // restore

	if auth1 == auth2 {
		t.Error("expected different signatures for different timestamps")
	}
}

// ---------------------------------------------------------------------------
// Integration-style test: end-to-end Store → Delete with mock
// ---------------------------------------------------------------------------

func TestS3Storage_StoreThenDelete(t *testing.T) {
	fixedTime := time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC)
	SetNowFunc(func() time.Time { return fixedTime })
	defer SetNowFunc(nil)

	stored := make(map[string][]byte)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := strings.TrimPrefix(r.URL.Path, "/")

		switch r.Method {
		case http.MethodHead:
			if _, ok := stored[key]; ok {
				w.WriteHeader(http.StatusOK)
			} else {
				w.WriteHeader(http.StatusNotFound)
			}
		case http.MethodPut:
			body, _ := ioutil.ReadAll(r.Body)
			stored[key] = body
			w.WriteHeader(http.StatusOK)
		case http.MethodDelete:
			delete(stored, key)
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	defer ts.Close()

	cfg := Config{
		Bucket:    "integration-bucket",
		Region:    "us-east-1",
		AccessKey: "AKID",
		SecretKey: "SECRET",
		Endpoint:  ts.URL,
	}

	s := NewS3Storage(cfg)

	// Store a file
	path, err := s.Store(strings.NewReader("integration test data"), "report.pdf", "application/pdf")
	if err != nil {
		t.Fatalf("Store failed: %v", err)
	}

	// Verify the object was stored
	if len(stored) != 1 {
		t.Errorf("expected 1 stored object, got %d", len(stored))
	}

	// Delete the file
	err = s.Delete(path)
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	// Verify the object was removed
	if len(stored) != 0 {
		t.Errorf("expected 0 stored objects after delete, got %d", len(stored))
	}
}

// ---------------------------------------------------------------------------
// URL parsing for Delete
// ---------------------------------------------------------------------------

func TestS3Storage_DeleteWithRelativePath(t *testing.T) {
	fixedTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	SetNowFunc(func() time.Time { return fixedTime })
	defer SetNowFunc(nil)

	var receivedPath string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedPath = r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	}))
	defer ts.Close()

	cfg := Config{
		Bucket:    "b",
		Region:    "r",
		AccessKey: "a",
		SecretKey: "s",
		Endpoint:  ts.URL,
	}
	s := NewS3Storage(cfg)

	// Delete with a relative key path (not a full URL)
	err := s.Delete("uploads/2024/01/old.txt")
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	if receivedPath != "/uploads/2024/01/old.txt" {
		t.Errorf("expected path /uploads/2024/01/old.txt, got %s", receivedPath)
	}
}

func TestS3Storage_DeleteNotFoundIsOK(t *testing.T) {
	fixedTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	SetNowFunc(func() time.Time { return fixedTime })
	defer SetNowFunc(nil)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// S3 returns 404 for already-deleted objects — should not be an error
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	cfg := Config{
		Bucket:    "b",
		Region:    "r",
		AccessKey: "a",
		SecretKey: "s",
		Endpoint:  ts.URL,
	}
	s := NewS3Storage(cfg)

	err := s.Delete(ts.URL + "/uploads/2024/01/gone.txt")
	if err != nil {
		t.Errorf("expected no error for 404 delete, got: %v", err)
	}
}

// TestS3Storage_StoreFailure verifies that non-2xx responses from S3 are
// reported as errors.
func TestS3Storage_StoreFailure(t *testing.T) {
	fixedTime := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	SetNowFunc(func() time.Time { return fixedTime })
	defer SetNowFunc(nil)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, "AccessDenied")
	}))
	defer ts.Close()

	cfg := Config{
		Bucket:    "b",
		Region:    "r",
		AccessKey: "a",
		SecretKey: "s",
		Endpoint:  ts.URL,
	}
	s := NewS3Storage(cfg)

	_, err := s.Store(strings.NewReader("data"), "secret.txt", "text/plain")
	if err == nil {
		t.Error("expected error for 403 response, got nil")
	}
	if !strings.Contains(err.Error(), "403") {
		t.Errorf("expected error to mention status 403, got: %v", err)
	}
	if !strings.Contains(err.Error(), "AccessDenied") {
		t.Errorf("expected error to include response body, got: %v", err)
	}
}
