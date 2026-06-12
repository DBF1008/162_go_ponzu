package backup

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

// Download describes a prepared backup artifact ready for HTTP delivery.
type Download struct {
	// Filename is the base name for Content-Disposition
	// (e.g. "uploads-1234567890.bak.tar.gz").
	Filename string
	// Size is the content length in bytes. Use -1 if unknown.
	Size int64
	// ContentType is the MIME type (e.g. "application/octet-stream").
	ContentType string
	// Body provides the download payload. Serve guarantees it is closed.
	Body io.ReadCloser
}

// Serve streams the Download to the HTTP response with correct headers.
// Body is always closed when Serve returns.
func (d *Download) Serve(res http.ResponseWriter) error {
	defer d.Body.Close()

	res.Header().Set("Content-Type", d.ContentType)
	res.Header().Set("Content-Disposition",
		fmt.Sprintf(`attachment; filename="%s"`, d.Filename))
	if d.Size >= 0 {
		res.Header().Set("Content-Length", fmt.Sprintf("%d", d.Size))
	}

	_, err := io.Copy(res, d.Body)
	return err
}

// NewFileDownload creates a Download backed by a file on disk. The file at
// filepath is automatically deleted when the download body is closed,
// guaranteeing cleanup even if the caller panics.
func NewFileDownload(filepath, filename, contentType string) (*Download, error) {
	f, err := os.Open(filepath)
	if err != nil {
		return nil, err
	}

	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}

	return &Download{
		Filename:    filename,
		Size:        info.Size(),
		ContentType: contentType,
		Body:        &removingReadCloser{File: f, path: filepath},
	}, nil
}

// removingReadCloser wraps os.File to delete the underlying file on Close.
type removingReadCloser struct {
	*os.File
	path string
}

func (r *removingReadCloser) Close() error {
	err := r.File.Close()
	// Best-effort cleanup; ignore error if file already removed.
	_ = os.Remove(r.path)
	return err
}

// FormatFilename generates a timestamped backup filename.
// Example: FormatFilename("uploads", ".bak.tar.gz") → "uploads-1718234567.bak.tar.gz"
func FormatFilename(prefix, extension string) string {
	return fmt.Sprintf("%s-%d%s", prefix, time.Now().Unix(), extension)
}

// SetDBBackupHeaders sets consistent HTTP headers for direct-to-response
// database streaming (used by BoltDB backup sources that call tx.WriteTo).
func SetDBBackupHeaders(res http.ResponseWriter, filename string, size int64) {
	res.Header().Set("Content-Type", "application/octet-stream")
	res.Header().Set("Content-Disposition",
		fmt.Sprintf(`attachment; filename="%s"`, filename))
	res.Header().Set("Content-Length", fmt.Sprintf("%d", size))
}

// contextWriter wraps an io.Writer to check context cancellation before each
// Write call. When the context is cancelled, subsequent writes return the
// context error immediately instead of blocking on a dead connection.
type contextWriter struct {
	w   io.Writer
	ctx context.Context
}

// ContextWriter returns an io.Writer that checks ctx cancellation on each
// Write. This is used by DB backup goroutines so that tx.WriteTo exits
// promptly when the backup is cancelled rather than continuing to write to
// an abandoned response.
func ContextWriter(ctx context.Context, w io.Writer) io.Writer {
	return &contextWriter{w: w, ctx: ctx}
}

func (cw *contextWriter) Write(p []byte) (int, error) {
	select {
	case <-cw.ctx.Done():
		return 0, cw.ctx.Err()
	default:
	}
	return cw.w.Write(p)
}
