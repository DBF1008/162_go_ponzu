package backup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// writeTree creates a temp directory populated with known files and returns the
// directory path plus a map of basename -> contents for assertions.
func writeTree(t *testing.T) (string, map[string]string) {
	t.Helper()

	dir := t.TempDir()
	want := map[string]string{
		"alpha.txt": "alpha contents",
		"beta.txt":  "beta contents\nsecond line",
	}
	for name, content := range want {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	return dir, want
}

// readTarGz decompresses and untars r, returning regular-file contents keyed by
// basename. It fails the test if the stream is not a valid gzip+tar archive,
// which is what catches an incorrectly finalized archive.
func readTarGz(t *testing.T, r io.Reader) map[string]string {
	t.Helper()

	gz, err := gzip.NewReader(r)
	if err != nil {
		t.Fatalf("gzip.NewReader: %v", err)
	}
	defer gz.Close()

	got := map[string]string{}
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("tar.Next: %v", err)
		}
		if hdr.FileInfo().IsDir() {
			continue
		}
		var b bytes.Buffer
		if _, err := io.Copy(&b, tr); err != nil {
			t.Fatalf("read entry %s: %v", hdr.Name, err)
		}
		got[filepath.Base(hdr.Name)] = b.String()
	}
	return got
}

func assertContains(t *testing.T, got, want map[string]string) {
	t.Helper()
	for name, content := range want {
		g, ok := got[name]
		if !ok {
			t.Errorf("archive missing file %q", name)
			continue
		}
		if g != content {
			t.Errorf("file %q: got %q, want %q", name, g, content)
		}
	}
}

func TestSetDownloadHeaders(t *testing.T) {
	rec := httptest.NewRecorder()
	SetDownloadHeaders(rec, "search-123.bak.tar.gz", "application/octet-stream", 4096)

	if got := rec.Header().Get("Content-Type"); got != "application/octet-stream" {
		t.Errorf("Content-Type = %q, want application/octet-stream", got)
	}
	wantCD := `attachment; filename="search-123.bak.tar.gz"`
	if got := rec.Header().Get("Content-Disposition"); got != wantCD {
		t.Errorf("Content-Disposition = %q, want %q", got, wantCD)
	}
	if got := rec.Header().Get("Content-Length"); got != "4096" {
		t.Errorf("Content-Length = %q, want 4096", got)
	}
}

// TestArchiveFSRoundTrip proves ArchiveFS produces a well-formed archive: it
// only round-trips if the tar writer is closed before the gzip writer.
func TestArchiveFSRoundTrip(t *testing.T) {
	dir, want := writeTree(t)

	var buf bytes.Buffer
	if err := ArchiveFS(context.Background(), dir, &buf); err != nil {
		t.Fatalf("ArchiveFS: %v", err)
	}

	assertContains(t, readTarGz(t, &buf), want)
}

// TestServeArchive covers the full download path: correct filename header, a
// streamable+valid archive body, accurate Content-Length, and temp-file cleanup.
func TestServeArchive(t *testing.T) {
	dir, want := writeTree(t)
	filename := "ponzu-test-servearchive.bak.tar.gz"
	tmpPath := filepath.Join(os.TempDir(), filename)
	os.Remove(tmpPath) // clear any leftover from a prior failed run

	rec := httptest.NewRecorder()
	if err := ServeArchive(context.Background(), rec, dir, filename); err != nil {
		t.Fatalf("ServeArchive: %v", err)
	}

	// Regression: the download must carry the real filename, not the bare
	// timestamp the old search/upload code mistakenly sent.
	wantCD := `attachment; filename="` + filename + `"`
	if got := rec.Header().Get("Content-Disposition"); got != wantCD {
		t.Errorf("Content-Disposition = %q, want %q", got, wantCD)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/octet-stream" {
		t.Errorf("Content-Type = %q, want application/octet-stream", got)
	}

	body := rec.Body.Bytes()
	if got := rec.Header().Get("Content-Length"); got != strconv.Itoa(len(body)) {
		t.Errorf("Content-Length = %q, want %d", got, len(body))
	}

	// Regression: the streamed body must be a valid, complete gzip+tar archive.
	assertContains(t, readTarGz(t, bytes.NewReader(body)), want)

	// Regression: the temp archive must be removed once served.
	if _, err := os.Stat(tmpPath); !os.IsNotExist(err) {
		t.Errorf("temp file %q not removed (stat err: %v)", tmpPath, err)
	}
}

// TestServeArchiveCancelCleanup proves a cancelled context aborts the archive
// AND leaves no temp file behind (no leak, no orphaned writer goroutine).
func TestServeArchiveCancelCleanup(t *testing.T) {
	dir, _ := writeTree(t)
	filename := "ponzu-test-cancel.bak.tar.gz"
	tmpPath := filepath.Join(os.TempDir(), filename)
	os.Remove(tmpPath)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before archiving begins

	rec := httptest.NewRecorder()
	err := ServeArchive(ctx, rec, dir, filename)
	if err == nil {
		t.Fatal("ServeArchive returned nil error on cancelled context")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("error = %v, want context.Canceled", err)
	}

	if _, err := os.Stat(tmpPath); !os.IsNotExist(err) {
		t.Errorf("temp file %q not removed after cancellation (stat err: %v)", tmpPath, err)
	}
}
