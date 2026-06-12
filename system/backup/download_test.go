package backup

import (
	"context"
	"io/ioutil"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestDownload_Serve_Headers(t *testing.T) {
	// Create a temp file with known content.
	tmpFile, err := ioutil.TempFile("", "download-test-")
	if err != nil {
		t.Fatal(err)
	}
	content := []byte("test download content")
	if _, err := tmpFile.Write(content); err != nil {
		t.Fatal(err)
	}
	tmpFile.Close()
	defer os.Remove(tmpFile.Name())

	dl, err := NewFileDownload(tmpFile.Name(), "backup-123.bak.tar.gz", "application/octet-stream")
	if err != nil {
		t.Fatalf("NewFileDownload failed: %v", err)
	}

	rec := httptest.NewRecorder()
	if err := dl.Serve(rec); err != nil {
		t.Fatalf("Serve failed: %v", err)
	}

	res := rec.Result()
	defer res.Body.Close()

	// Check Content-Type.
	ct := res.Header.Get("Content-Type")
	if ct != "application/octet-stream" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/octet-stream")
	}

	// Check Content-Disposition has quoted filename.
	cd := res.Header.Get("Content-Disposition")
	expected := `attachment; filename="backup-123.bak.tar.gz"`
	if cd != expected {
		t.Errorf("Content-Disposition = %q, want %q", cd, expected)
	}

	// Check Content-Length matches actual content.
	cl := res.Header.Get("Content-Length")
	if cl != "21" {
		t.Errorf("Content-Length = %q, want %q (len=%d)", cl, "21", len(content))
	}

	// Verify body content matches.
	body, err := ioutil.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != string(content) {
		t.Errorf("body = %q, want %q", string(body), string(content))
	}
}

func TestNewFileDownload_CleanupOnClose(t *testing.T) {
	// Create a temp file.
	tmpFile, err := ioutil.TempFile("", "download-cleanup-test-")
	if err != nil {
		t.Fatal(err)
	}
	tmpFile.Write([]byte("cleanup test"))
	tmpFile.Close()

	tmpPath := tmpFile.Name()

	// Verify file exists before serving.
	if _, err := os.Stat(tmpPath); os.IsNotExist(err) {
		t.Fatal("temp file should exist before serving")
	}

	dl, err := NewFileDownload(tmpPath, "test.bak", "application/octet-stream")
	if err != nil {
		t.Fatalf("NewFileDownload failed: %v", err)
	}

	rec := httptest.NewRecorder()
	if err := dl.Serve(rec); err != nil {
		t.Fatalf("Serve failed: %v", err)
	}

	// After Serve, the temp file should be deleted by removingReadCloser.
	if _, err := os.Stat(tmpPath); !os.IsNotExist(err) {
		t.Errorf("temp file %q should have been deleted after Serve, but still exists", tmpPath)
		os.Remove(tmpPath) // cleanup for failed test
	}
}

func TestNewFileDownload_OpenError(t *testing.T) {
	_, err := NewFileDownload("/nonexistent/path/to/file.bak", "file.bak", "application/octet-stream")
	if err == nil {
		t.Error("expected error for nonexistent file, got nil")
	}
}

func TestFormatFilename(t *testing.T) {
	name := FormatFilename("uploads", ".bak.tar.gz")

	// Should start with prefix and end with extension.
	if !strings.HasPrefix(name, "uploads-") {
		t.Errorf("FormatFilename should start with 'uploads-', got: %q", name)
	}
	if !strings.HasSuffix(name, ".bak.tar.gz") {
		t.Errorf("FormatFilename should end with '.bak.tar.gz', got: %q", name)
	}

	// Middle part should be a numeric timestamp.
	middle := strings.TrimPrefix(name, "uploads-")
	middle = strings.TrimSuffix(middle, ".bak.tar.gz")
	if middle == "" {
		t.Error("FormatFilename timestamp part is empty")
	}
	for _, c := range middle {
		if c < '0' || c > '9' {
			t.Errorf("FormatFilename timestamp contains non-digit: %q in %q", string(c), name)
			break
		}
	}
}

func TestSetDBBackupHeaders(t *testing.T) {
	rec := httptest.NewRecorder()

	SetDBBackupHeaders(rec, "system-1234567890.db.bak", 1024)

	res := rec.Result()
	defer res.Body.Close()

	ct := res.Header.Get("Content-Type")
	if ct != "application/octet-stream" {
		t.Errorf("Content-Type = %q, want %q", ct, "application/octet-stream")
	}

	cd := res.Header.Get("Content-Disposition")
	expected := `attachment; filename="system-1234567890.db.bak"`
	if cd != expected {
		t.Errorf("Content-Disposition = %q, want %q", cd, expected)
	}

	cl := res.Header.Get("Content-Length")
	if cl != "1024" {
		t.Errorf("Content-Length = %q, want %q", cl, "1024")
	}
}

func TestContextWriter_CancelStopsWrite(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately.

	rec := httptest.NewRecorder()
	cw := ContextWriter(ctx, rec)

	n, err := cw.Write([]byte("should not write"))
	if err == nil {
		t.Error("expected error from cancelled context, got nil")
	}
	if n != 0 {
		t.Errorf("expected 0 bytes written, got %d", n)
	}
}

func TestContextWriter_SuccessWhenNotCancelled(t *testing.T) {
	ctx := context.Background()

	rec := httptest.NewRecorder()
	cw := ContextWriter(ctx, rec)

	data := []byte("hello world")
	n, err := cw.Write(data)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if n != len(data) {
		t.Errorf("wrote %d bytes, want %d", n, len(data))
	}

	body := rec.Body.String()
	if body != "hello world" {
		t.Errorf("body = %q, want %q", body, "hello world")
	}
}

// TestDownloadServeUnknownSize verifies that Serve omits Content-Length when Size is -1.
func TestDownloadServeUnknownSize(t *testing.T) {
	dl := &Download{
		Filename:    "stream.bak",
		Size:        -1,
		ContentType: "application/octet-stream",
		Body:        ioutil.NopCloser(strings.NewReader("streaming data")),
	}

	rec := httptest.NewRecorder()
	if err := dl.Serve(rec); err != nil {
		t.Fatalf("Serve failed: %v", err)
	}

	res := rec.Result()
	defer res.Body.Close()

	cl := res.Header.Get("Content-Length")
	if cl != "" {
		t.Errorf("Content-Length should be empty for unknown size, got %q", cl)
	}

	// Verify header still written correctly.
	cd := res.Header.Get("Content-Disposition")
	if cd != `attachment; filename="stream.bak"` {
		t.Errorf("Content-Disposition = %q", cd)
	}
}
