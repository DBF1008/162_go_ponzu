package storage

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ponzu-cms/ponzu/system/cfg"
	"github.com/ponzu-cms/ponzu/system/item"
)

// LocalStorage implements Storage by writing files to the local filesystem
// under the configured upload directory. This is the default backend used
// when no S3 credentials are configured.
type LocalStorage struct{}

// Store writes the file to uploads/YYYY/MM/filename.ext on local disk and
// returns the access path "/api/uploads/YYYY/MM/filename.ext". If a file with
// the same name already exists, a Unix timestamp prefix is prepended.
func (l *LocalStorage) Store(r io.Reader, filename string, contentType string) (string, error) {
	now := time.Now()
	year := fmt.Sprintf("%d", now.Year())
	month := fmt.Sprintf("%02d", now.Month())

	uploadDir := filepath.Join(cfg.UploadDir(), year, month)
	if err := os.MkdirAll(uploadDir, os.ModeDir|os.ModePerm); err != nil {
		return "", fmt.Errorf("failed to create upload directory: %w", err)
	}

	normalized, err := item.NormalizeString(filename)
	if err != nil {
		return "", fmt.Errorf("failed to normalize filename: %w", err)
	}

	absPath := filepath.Join(uploadDir, normalized)

	// Handle duplicate filenames by prepending a Unix timestamp
	if _, err := os.Stat(absPath); !os.IsNotExist(err) {
		normalized = fmt.Sprintf("%d-%s", time.Now().Unix(), normalized)
		absPath = filepath.Join(uploadDir, normalized)
	}

	dst, err := os.Create(absPath)
	if err != nil {
		return "", fmt.Errorf("failed to create destination file: %w", err)
	}
	defer dst.Close()

	if _, err := io.Copy(dst, r); err != nil {
		return "", fmt.Errorf("failed to write file to disk: %w", err)
	}

	urlPath := fmt.Sprintf("/api/uploads/%s/%s/%s", year, month, normalized)
	return urlPath, nil
}

// Delete removes a file from local disk. The path should be in the format
// returned by Store, e.g. "/api/uploads/2024/01/file.txt".
func (l *LocalStorage) Delete(path string) error {
	uploadDir := cfg.UploadDir()
	// Strip the /api/uploads/ prefix to get the relative date/filename path.
	// cfg.UploadDir() already resolves to the "uploads" directory on disk,
	// so we must strip both "/api/uploads/" and "/uploads/" to avoid
	// duplicating the "uploads" segment in the final path.
	relPath := path
	if strings.HasPrefix(relPath, "/api/uploads/") {
		relPath = relPath[len("/api/uploads/"):]
	} else if strings.HasPrefix(relPath, "/uploads/") {
		relPath = relPath[len("/uploads/"):]
	} else if strings.HasPrefix(relPath, "/api/") {
		relPath = relPath[len("/api/"):]
	}

	absPath := filepath.Join(uploadDir, filepath.FromSlash(relPath))
	return os.Remove(absPath)
}

// IsObjectStorage returns false for local disk storage.
func (l *LocalStorage) IsObjectStorage() bool {
	return false
}
