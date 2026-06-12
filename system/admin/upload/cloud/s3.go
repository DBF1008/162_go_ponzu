package cloud

import (
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"strings"
	"time"

	"github.com/ponzu-cms/ponzu/system/cfg"
)

// s3Store persists uploads to an S3-compatible object storage service using
// path-style addressing and AWS Signature Version 4 (see sigv4.go). It uses a
// streamed (UNSIGNED-PAYLOAD) body so files are not buffered in memory.
type s3Store struct {
	bucket     string
	region     string
	accessKey  string
	secretKey  string
	endpoint   string // scheme://host[:port], no trailing slash
	publicBase string // optional public/CDN base URL, no trailing slash
	prefix     string // key prefix, no surrounding slashes
	acl        string // canned ACL, or "none"/"" to omit

	client *http.Client
}

func newS3Store() *s3Store {
	return &s3Store{
		bucket:     cfg.S3Bucket(),
		region:     cfg.S3Region(),
		accessKey:  cfg.S3AccessKeyID(),
		secretKey:  cfg.S3SecretAccessKey(),
		endpoint:   cfg.S3Endpoint(),
		publicBase: cfg.S3PublicBaseURL(),
		prefix:     cfg.S3Prefix(),
		acl:        cfg.S3ACL(),
		client:     &http.Client{Timeout: 5 * time.Minute},
	}
}

// objectKey applies the configured prefix to a relative upload key.
func (s *s3Store) objectKey(key string) string {
	key = strings.TrimPrefix(key, "/")
	if s.prefix != "" {
		return s.prefix + "/" + key
	}
	return key
}

// publicURL builds the stable, externally accessible URL for an object key.
func (s *s3Store) publicURL(objectKey string) string {
	if s.publicBase != "" {
		return s.publicBase + "/" + objectKey
	}
	return s.endpoint + "/" + s.bucket + "/" + objectKey
}

func (s *s3Store) Save(key string, src io.Reader, size int64, contentType string) (Result, error) {
	objectKey := s.objectKey(key)
	target := s.endpoint + "/" + s.bucket + "/" + objectKey

	req, err := http.NewRequest(http.MethodPut, target, src)
	if err != nil {
		return Result{}, err
	}
	// Force a fixed Content-Length so the request is not chunked; S3 requires a
	// known length for a simple PUT.
	req.ContentLength = size
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if s.acl != "" && s.acl != "none" {
		req.Header.Set("X-Amz-Acl", s.acl)
	}
	req.Header.Set("X-Amz-Content-Sha256", unsignedPayload)
	signV4(req, s.region, "s3", s.accessKey, s.secretKey, unsignedPayload, time.Now())

	resp, err := s.client.Do(req)
	if err != nil {
		return Result{}, fmt.Errorf("s3 upload request failed: %s", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		body, _ := ioutil.ReadAll(io.LimitReader(resp.Body, 2048))
		return Result{}, fmt.Errorf("s3 upload of %q failed: %s: %s",
			objectKey, resp.Status, strings.TrimSpace(string(body)))
	}

	return Result{Key: key, URL: s.publicURL(objectKey), Size: size}, nil
}

func (s *s3Store) Delete(storedURL string) error {
	objectKey := s.keyFromURL(storedURL)
	if objectKey == "" {
		return fmt.Errorf("cannot derive S3 object key from %q", storedURL)
	}
	target := s.endpoint + "/" + s.bucket + "/" + objectKey

	req, err := http.NewRequest(http.MethodDelete, target, nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-Amz-Content-Sha256", unsignedPayload)
	signV4(req, s.region, "s3", s.accessKey, s.secretKey, unsignedPayload, time.Now())

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("s3 delete request failed: %s", err)
	}
	defer resp.Body.Close()

	// S3 returns 204 on success; treat a missing object (404) as already gone.
	if (resp.StatusCode < 200 || resp.StatusCode > 299) && resp.StatusCode != http.StatusNotFound {
		body, _ := ioutil.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("s3 delete of %q failed: %s: %s",
			objectKey, resp.Status, strings.TrimSpace(string(body)))
	}

	return nil
}

// keyFromURL recovers an object key from a previously returned public URL.
func (s *s3Store) keyFromURL(storedURL string) string {
	if s.publicBase != "" {
		if prefix := s.publicBase + "/"; strings.HasPrefix(storedURL, prefix) {
			return strings.TrimPrefix(storedURL, prefix)
		}
	}
	if prefix := s.endpoint + "/" + s.bucket + "/"; strings.HasPrefix(storedURL, prefix) {
		return strings.TrimPrefix(storedURL, prefix)
	}
	return ""
}
