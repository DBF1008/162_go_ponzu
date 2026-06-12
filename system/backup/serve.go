package backup

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
)

// ServeArchive tars and gzips the directory at basedir into a temporary file and
// streams it to res as a download named filename. The temporary file is always
// removed before returning — including on archive errors and context
// cancellation — and response headers are set consistently via
// SetDownloadHeaders. Headers are only written once the archive has been built
// successfully, so a failure leaves the response status free for the caller.
func ServeArchive(ctx context.Context, res http.ResponseWriter, basedir, filename string) error {
	path := filepath.Join(os.TempDir(), filename)

	f, err := os.Create(path)
	if err != nil {
		return err
	}
	// Guarantee the temp file is cleaned up on every return path.
	defer os.Remove(path)

	err = ArchiveFS(ctx, basedir, f)
	if err != nil {
		f.Close()
		return err
	}

	// Flush the archive to disk before re-opening it for reading.
	err = f.Close()
	if err != nil {
		return err
	}

	data, err := os.Open(path)
	if err != nil {
		return err
	}
	defer data.Close()

	info, err := data.Stat()
	if err != nil {
		return err
	}

	SetDownloadHeaders(res, filename, "application/octet-stream", info.Size())

	_, err = io.Copy(res, data)
	return err
}
