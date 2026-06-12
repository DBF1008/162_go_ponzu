package cloud

import (
	"bytes"
	"encoding/hex"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ponzu-cms/ponzu/system/cfg"
)

// exampleSecret is the secret key from AWS's published Signature V4 example and
// is reused as a throwaway secret for the local fake-S3 tests.
const exampleSecret = "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY"

// envKeys are all environment variables that influence storage backend
// selection; withEnv clears and restores them so tests are isolated.
var envKeys = []string{
	"PONZU_S3_BUCKET",
	"PONZU_S3_ACCESS_KEY_ID",
	"PONZU_S3_SECRET_ACCESS_KEY",
	"PONZU_S3_REGION",
	"PONZU_S3_ENDPOINT",
	"PONZU_S3_PUBLIC_BASE_URL",
	"PONZU_S3_PREFIX",
	"PONZU_S3_ACL",
	"PONZU_UPLOAD_DIR",
	"PONZU_DATA_DIR",
}

// withEnv clears all storage-related env vars, applies kv, and restores the
// previous environment when the test finishes.
func withEnv(t *testing.T, kv map[string]string) {
	t.Helper()
	saved := make(map[string]*string, len(envKeys))
	for _, k := range envKeys {
		if v, ok := os.LookupEnv(k); ok {
			vv := v
			saved[k] = &vv
		}
		os.Unsetenv(k)
	}
	for k, v := range kv {
		os.Setenv(k, v)
	}
	t.Cleanup(func() {
		for _, k := range envKeys {
			if v := saved[k]; v != nil {
				os.Setenv(k, *v)
			} else {
				os.Unsetenv(k)
			}
		}
	})
}

// TestSigV4KnownAnswer locks the signer against AWS's published Signature
// Version 4 example (GET iam.amazonaws.com ListUsers). If this fails the
// canonicalization or signing-key derivation has regressed.
func TestSigV4KnownAnswer(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet,
		"https://iam.amazonaws.com/?Action=ListUsers&Version=2010-05-08", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=utf-8")

	when := time.Date(2015, time.August, 30, 12, 36, 0, 0, time.UTC)
	signV4(req, "us-east-1", "iam", "AKIDEXAMPLE", exampleSecret, sha256Hex([]byte("")), when)

	got := req.Header.Get("Authorization")
	want := "AWS4-HMAC-SHA256 " +
		"Credential=AKIDEXAMPLE/20150830/us-east-1/iam/aws4_request, " +
		"SignedHeaders=content-type;host;x-amz-date, " +
		"Signature=5d672d79c15b13162d9279b0855cfba6789a8edb4c82c400e06b5924a6f2b5d7"
	if got != want {
		t.Fatalf("SigV4 Authorization mismatch:\n got: %s\nwant: %s", got, want)
	}
	if d := req.Header.Get("X-Amz-Date"); d != "20150830T123600Z" {
		t.Fatalf("X-Amz-Date = %q, want 20150830T123600Z", d)
	}
}

// TestS3Configured verifies the predicate that gates the object storage backend.
func TestS3Configured(t *testing.T) {
	withEnv(t, map[string]string{
		"PONZU_S3_BUCKET":            "b",
		"PONZU_S3_ACCESS_KEY_ID":     "a",
		"PONZU_S3_SECRET_ACCESS_KEY": "s",
	})
	if !cfg.S3Configured() {
		t.Fatal("expected S3Configured() == true when bucket + keys are set")
	}

	os.Unsetenv("PONZU_S3_SECRET_ACCESS_KEY")
	if cfg.S3Configured() {
		t.Fatal("expected S3Configured() == false when secret key is missing")
	}

	os.Setenv("PONZU_S3_SECRET_ACCESS_KEY", "s")
	os.Unsetenv("PONZU_S3_BUCKET")
	if cfg.S3Configured() {
		t.Fatal("expected S3Configured() == false when bucket is missing")
	}
}

// TestIsRemote checks classification of stored paths.
func TestIsRemote(t *testing.T) {
	cases := map[string]bool{
		"/api/uploads/2018/05/x.png":             false,
		"https://cdn.example.test/uploads/x.png": true,
		"http://minio:9000/bucket/uploads/x.png": true,
		"":                                       false,
		"uploads/2018/05/x.png":                  false,
	}
	for p, want := range cases {
		if got := IsRemote(p); got != want {
			t.Errorf("IsRemote(%q) = %v, want %v", p, got, want)
		}
	}
}

// TestProviderLocalFallback verifies that without S3 credentials uploads go to
// the local filesystem, return a Ponzu-served path, avoid overwriting existing
// files, and can be deleted.
func TestProviderLocalFallback(t *testing.T) {
	dir, err := ioutil.TempDir("", "ponzu-upload-local")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	withEnv(t, map[string]string{"PONZU_UPLOAD_DIR": dir})

	if cfg.S3Configured() {
		t.Fatal("expected S3 to be unconfigured")
	}
	store := Provider()
	if _, ok := store.(*localStore); !ok {
		t.Fatalf("Provider() = %T, want *localStore", store)
	}

	content := []byte("hello world")
	res, err := store.Save("2018/05/hello.txt", bytes.NewReader(content), int64(len(content)), "text/plain")
	if err != nil {
		t.Fatal(err)
	}
	if res.URL != "/api/uploads/2018/05/hello.txt" {
		t.Fatalf("Result.URL = %q, want /api/uploads/2018/05/hello.txt", res.URL)
	}
	if res.Size != int64(len(content)) {
		t.Fatalf("Result.Size = %d, want %d", res.Size, len(content))
	}

	onDisk, err := ioutil.ReadFile(filepath.Join(dir, "2018", "05", "hello.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(onDisk) != string(content) {
		t.Fatalf("stored file = %q, want %q", onDisk, content)
	}

	// A second upload to the same key must not overwrite the first.
	res2, err := store.Save("2018/05/hello.txt", bytes.NewReader([]byte("second")), 6, "text/plain")
	if err != nil {
		t.Fatal(err)
	}
	if res2.Key == "2018/05/hello.txt" {
		t.Fatalf("expected a collision-renamed key, got %q", res2.Key)
	}
	if !strings.HasPrefix(res2.URL, "/api/uploads/2018/05/") {
		t.Fatalf("Result.URL = %q, want /api/uploads/2018/05/ prefix", res2.URL)
	}
	again, _ := ioutil.ReadFile(filepath.Join(dir, "2018", "05", "hello.txt"))
	if string(again) != "hello world" {
		t.Fatalf("original file was overwritten: %q", again)
	}

	// Delete should remove the original file from disk.
	if err := store.Delete(res.URL); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "2018", "05", "hello.txt")); !os.IsNotExist(err) {
		t.Fatalf("expected file to be removed, stat err = %v", err)
	}
}

// TestProviderS3Upload verifies that with S3 credentials configured, uploads are
// PUT to the object storage endpoint with a valid SigV4 signature and that a
// stable public URL is returned. The fake server independently re-derives the
// signature to guard against canonicalization regressions.
func TestProviderS3Upload(t *testing.T) {
	content := []byte("\x89PNG\r\n\x1a\nsome-binary-content")

	var (
		gotPUT, gotDELETE bool
		putPath, delPath  string
		putBody           []byte
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		verifySignature(t, r, exampleSecret)
		switch r.Method {
		case http.MethodPut:
			gotPUT = true
			putPath = r.URL.Path
			if v := r.Header.Get("X-Amz-Content-Sha256"); v != "UNSIGNED-PAYLOAD" {
				t.Errorf("X-Amz-Content-Sha256 = %q, want UNSIGNED-PAYLOAD", v)
			}
			if v := r.Header.Get("X-Amz-Acl"); v != "public-read" {
				t.Errorf("X-Amz-Acl = %q, want public-read", v)
			}
			if v := r.Header.Get("Content-Type"); v != "image/png" {
				t.Errorf("Content-Type = %q, want image/png", v)
			}
			if r.ContentLength != int64(len(content)) {
				t.Errorf("Content-Length = %d, want %d", r.ContentLength, len(content))
			}
			putBody, _ = ioutil.ReadAll(r.Body)
			w.WriteHeader(http.StatusOK)
		case http.MethodDelete:
			gotDELETE = true
			delPath = r.URL.Path
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected method %s", r.Method)
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	defer srv.Close()

	withEnv(t, map[string]string{
		"PONZU_S3_BUCKET":            "test-bucket",
		"PONZU_S3_ACCESS_KEY_ID":     "AKIDEXAMPLE",
		"PONZU_S3_SECRET_ACCESS_KEY": exampleSecret,
		"PONZU_S3_REGION":            "us-east-1",
		"PONZU_S3_ENDPOINT":          srv.URL,
		"PONZU_S3_PUBLIC_BASE_URL":   "https://cdn.example.test",
	})

	if !cfg.S3Configured() {
		t.Fatal("expected S3 to be configured")
	}
	store := Provider()
	if _, ok := store.(*s3Store); !ok {
		t.Fatalf("Provider() = %T, want *s3Store", store)
	}

	res, err := store.Save("2018/05/photo.png", bytes.NewReader(content), int64(len(content)), "image/png")
	if err != nil {
		t.Fatal(err)
	}
	if !gotPUT {
		t.Fatal("fake S3 server never received the PUT request")
	}
	if putPath != "/test-bucket/uploads/2018/05/photo.png" {
		t.Fatalf("PUT path = %q, want /test-bucket/uploads/2018/05/photo.png", putPath)
	}
	if !bytes.Equal(putBody, content) {
		t.Fatalf("uploaded body did not match source (%d vs %d bytes)", len(putBody), len(content))
	}
	if res.URL != "https://cdn.example.test/uploads/2018/05/photo.png" {
		t.Fatalf("Result.URL = %q, want https://cdn.example.test/uploads/2018/05/photo.png", res.URL)
	}
	if res.Key != "2018/05/photo.png" {
		t.Fatalf("Result.Key = %q, want 2018/05/photo.png", res.Key)
	}
	if res.Size != int64(len(content)) {
		t.Fatalf("Result.Size = %d, want %d", res.Size, len(content))
	}

	// Deleting by the returned public URL must hit the right object key.
	if err := store.Delete(res.URL); err != nil {
		t.Fatal(err)
	}
	if !gotDELETE {
		t.Fatal("fake S3 server never received the DELETE request")
	}
	if delPath != "/test-bucket/uploads/2018/05/photo.png" {
		t.Fatalf("DELETE path = %q, want /test-bucket/uploads/2018/05/photo.png", delPath)
	}
}

// verifySignature independently re-derives the SigV4 signature for an incoming
// request and asserts it matches the Authorization header the client sent. This
// is a deliberately separate implementation from signV4 so a bug in the
// production canonical-request assembly (path, host, headers) is caught.
func verifySignature(t *testing.T, r *http.Request, secret string) {
	t.Helper()
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, sigV4Algorithm+" ") {
		t.Errorf("Authorization header missing or malformed: %q", auth)
		return
	}

	var credential, signedHeaders, signature string
	for _, part := range strings.Split(strings.TrimPrefix(auth, sigV4Algorithm+" "), ",") {
		part = strings.TrimSpace(part)
		switch {
		case strings.HasPrefix(part, "Credential="):
			credential = strings.TrimPrefix(part, "Credential=")
		case strings.HasPrefix(part, "SignedHeaders="):
			signedHeaders = strings.TrimPrefix(part, "SignedHeaders=")
		case strings.HasPrefix(part, "Signature="):
			signature = strings.TrimPrefix(part, "Signature=")
		}
	}

	credParts := strings.Split(credential, "/")
	if len(credParts) != 5 {
		t.Errorf("malformed Credential scope: %q", credential)
		return
	}
	shortDate, region, service := credParts[1], credParts[2], credParts[3]
	amzDate := r.Header.Get("X-Amz-Date")
	payloadHash := r.Header.Get("X-Amz-Content-Sha256")

	var canonicalHeaders strings.Builder
	for _, name := range strings.Split(signedHeaders, ";") {
		var value string
		switch name {
		case "host":
			value = r.Host
		case "content-type":
			value = r.Header.Get("Content-Type")
		default:
			value = r.Header.Get(http.CanonicalHeaderKey(name))
		}
		canonicalHeaders.WriteString(name + ":" + strings.TrimSpace(value) + "\n")
	}

	uri := r.URL.EscapedPath()
	if uri == "" {
		uri = "/"
	}
	canonicalRequest := strings.Join([]string{
		r.Method,
		uri,
		canonicalQueryString(r.URL.Query()),
		canonicalHeaders.String(),
		signedHeaders,
		payloadHash,
	}, "\n")

	scope := strings.Join([]string{shortDate, region, service, "aws4_request"}, "/")
	stringToSign := strings.Join([]string{
		sigV4Algorithm, amzDate, scope, sha256Hex([]byte(canonicalRequest)),
	}, "\n")

	kDate := hmacSHA256([]byte("AWS4"+secret), shortDate)
	kRegion := hmacSHA256(kDate, region)
	kService := hmacSHA256(kRegion, service)
	kSigning := hmacSHA256(kService, "aws4_request")
	want := hex.EncodeToString(hmacSHA256(kSigning, stringToSign))

	if want != signature {
		t.Errorf("SigV4 signature mismatch for %s %s:\n  recomputed by server: %s\n  sent by client:       %s",
			r.Method, uri, want, signature)
	}
}
