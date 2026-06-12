// Package cloud provides pluggable storage backends for the upload system. It
// selects, at runtime, between local filesystem storage (the default) and an
// optional S3-compatible object storage backend based on configuration.
//
// This package intentionally depends only on the standard library and the
// pure-stdlib system/cfg package, so it builds and is testable independently of
// the rest of the Ponzu system.
package cloud

import (
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/ponzu-cms/ponzu/system/cfg"
)

// Result describes where an uploaded file was stored.
type Result struct {
	// Key is the storage path of the object relative to the uploads root
	// (e.g. "2018/05/photo.png"). The local backend may change it on a name
	// collision; the returned value always reflects the stored object.
	Key string
	// URL is the address used to reference the file. For local storage it is a
	// relative path served by Ponzu ("/api/uploads/..."); for object storage it
	// is a stable, absolute URL.
	URL string
	// Size is the number of bytes stored.
	Size int64
}

// Store is a backend capable of persisting and removing uploaded files.
type Store interface {
	// Save persists src (size bytes, of the given content type) at key, the
	// path relative to the uploads root, and returns where it was stored.
	Save(key string, src io.Reader, size int64, contentType string) (Result, error)
	// Delete removes a previously stored file, identified by the URL/path that
	// was recorded for it (Result.URL).
	Delete(storedURL string) error
}

// Provider returns the storage backend selected by the current configuration:
// the S3-compatible object storage backend when credentials are configured,
// otherwise the local filesystem backend.
func Provider() Store {
	if cfg.S3Configured() {
		return newS3Store()
	}
	return &localStore{}
}

// IsRemote reports whether a stored file path/URL points at an external object
// storage backend (an absolute http(s) URL) rather than the local disk.
func IsRemote(storedPath string) bool {
	return strings.HasPrefix(storedPath, "http://") || strings.HasPrefix(storedPath, "https://")
}

// localStore writes uploads to the local filesystem under cfg.UploadDir() and
// serves them via Ponzu's "/api/uploads/" route.
type localStore struct{}

func (l *localStore) Save(key string, src io.Reader, size int64, contentType string) (Result, error) {
	key = strings.TrimPrefix(key, "/")

	abs := filepath.Join(cfg.UploadDir(), filepath.FromSlash(key))
	if err := os.MkdirAll(filepath.Dir(abs), os.ModeDir|os.ModePerm); err != nil {
		return Result{}, err
	}

	// If a file already exists at the path, prefix the filename with the current
	// timestamp so existing uploads are not overwritten (mirrors prior behavior).
	if _, err := os.Stat(abs); err == nil {
		dir, base := path.Split(key)
		base = fmt.Sprintf("%d-%s", time.Now().Unix(), base)
		key = dir + base
		abs = filepath.Join(cfg.UploadDir(), filepath.FromSlash(key))
	}

	dst, err := os.Create(abs)
	if err != nil {
		return Result{}, fmt.Errorf("failed to create destination file for upload: %s", err)
	}
	defer dst.Close()

	n, err := io.Copy(dst, src)
	if err != nil {
		return Result{}, fmt.Errorf("failed to copy uploaded file to destination: %s", err)
	}

	return Result{Key: key, URL: "/api/uploads/" + key, Size: n}, nil
}

func (l *localStore) Delete(storedURL string) error {
	rel := strings.TrimPrefix(storedURL, "/api/uploads/")
	rel = strings.TrimPrefix(rel, "/")
	abs := filepath.Join(cfg.UploadDir(), filepath.FromSlash(rel))
	return os.Remove(abs)
}
