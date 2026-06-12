package storage

import "io"

// Storage is the interface for file storage backends. Ponzu ships two
// implementations: LocalStorage (default, writes to disk) and S3Storage
// (writes to an S3-compatible object store).
type Storage interface {
	// Store writes the content of r as a file with the given name and content
	// type. It returns the access path or URL where the file can be retrieved.
	//
	// LocalStorage returns a relative path like "/api/uploads/2024/01/file.txt".
	// S3Storage returns a full URL like "https://bucket.s3.us-east-1.amazonaws.com/2024/01/file.txt".
	Store(r io.Reader, filename string, contentType string) (string, error)

	// Delete removes a previously stored file identified by its path or URL.
	Delete(path string) error

	// IsObjectStorage reports whether this backend is a remote object store.
	IsObjectStorage() bool
}

// New creates the appropriate Storage implementation based on the current
// environment. If S3 credentials are configured, it returns an S3 backend;
// otherwise it returns the local disk backend.
func New() Storage {
	cfg := ConfigFromEnv()
	if cfg.IsConfigured() {
		return NewS3Storage(cfg)
	}
	return &LocalStorage{}
}
