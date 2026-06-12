package backup

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestArchiveFS_ProducesValidTarGz(t *testing.T) {
	// Create a temp directory with known files.
	dir, err := ioutil.TempDir("", "archive-test-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	// Create a subdirectory and files.
	subDir := filepath.Join(dir, "sub")
	if err := os.Mkdir(subDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := ioutil.WriteFile(filepath.Join(dir, "hello.txt"), []byte("hello world"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := ioutil.WriteFile(filepath.Join(subDir, "nested.txt"), []byte("nested content"), 0644); err != nil {
		t.Fatal(err)
	}

	// Archive the directory.
	tmpArchive, err := ioutil.TempFile("", "archive-out-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpArchive.Name())

	ctx := context.Background()
	if err := ArchiveFS(ctx, dir, tmpArchive); err != nil {
		t.Fatalf("ArchiveFS failed: %v", err)
	}
	tmpArchive.Close()

	// Re-open and read back with gzip + tar readers.
	f, err := os.Open(tmpArchive.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	gzReader, err := gzip.NewReader(f)
	if err != nil {
		t.Fatalf("gzip.NewReader failed (archive may be corrupt): %v", err)
	}
	defer gzReader.Close()

	tarReader := tar.NewReader(gzReader)

	found := make(map[string]bool)
	for {
		hdr, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("tar.Reader.Next failed: %v", err)
		}
		found[hdr.Name] = true
	}

	// Verify we got entries for our files.
	if len(found) < 3 {
		t.Errorf("expected at least 3 tar entries (dir, hello.txt, sub/nested.txt), got %d: %v", len(found), found)
	}

	// Check specific files exist in the archive.
	hasHelloTxt := false
	hasNestedTxt := false
	for name := range found {
		if strings.HasSuffix(name, "hello.txt") {
			hasHelloTxt = true
		}
		if strings.HasSuffix(name, "nested.txt") {
			hasNestedTxt = true
		}
	}
	if !hasHelloTxt {
		t.Error("archive missing hello.txt")
	}
	if !hasNestedTxt {
		t.Error("archive missing sub/nested.txt")
	}
}

func TestArchiveFS_ContextCancellation(t *testing.T) {
	// Create a temp directory with many files to increase the chance of
	// catching the cancellation mid-walk.
	dir, err := ioutil.TempDir("", "archive-cancel-test-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	for i := 0; i < 50; i++ {
		name := filepath.Join(dir, strings.Repeat("x", 10)+string(rune('a'+i%26))+".txt")
		if err := ioutil.WriteFile(name, []byte(strings.Repeat("data", 100)), 0644); err != nil {
			t.Fatal(err)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())

	goroutinesBefore := runtime.NumGoroutine()

	done := make(chan error, 1)
	go func() {
		// Cancel after a short delay.
		time.Sleep(time.Millisecond * 5)
		cancel()
	}()

	tmpArchive, err := ioutil.TempFile("", "archive-cancel-out-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpArchive.Name())

	go func() {
		err := ArchiveFS(ctx, dir, tmpArchive)
		tmpArchive.Close()
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil {
			// It's possible the archive completed before cancellation.
			// That's OK; the test is probabilistic.
			t.Log("archive completed before cancellation (OK for small dirs)")
		} else if err != context.Canceled {
			t.Errorf("expected context.Canceled, got: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ArchiveFS did not return within 5 seconds of cancellation")
	}

	// Allow goroutines to settle.
	time.Sleep(100 * time.Millisecond)
	goroutinesAfter := runtime.NumGoroutine()

	// Allow some slack (±3 goroutines for GC, test runner, etc.).
	if goroutinesAfter > goroutinesBefore+3 {
		t.Errorf("potential goroutine leak: before=%d, after=%d", goroutinesBefore, goroutinesAfter)
	}
}

func TestArchiveFS_EmptyDir(t *testing.T) {
	dir, err := ioutil.TempDir("", "archive-empty-test-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	tmpArchive, err := ioutil.TempFile("", "archive-empty-out-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpArchive.Name())

	ctx := context.Background()
	if err := ArchiveFS(ctx, dir, tmpArchive); err != nil {
		t.Fatalf("ArchiveFS on empty dir failed: %v", err)
	}
	tmpArchive.Close()

	// Verify the result is a valid (empty) tar.gz.
	f, err := os.Open(tmpArchive.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	gzReader, err := gzip.NewReader(f)
	if err != nil {
		t.Fatalf("gzip.NewReader failed on empty archive: %v", err)
	}
	defer gzReader.Close()

	tarReader := tar.NewReader(gzReader)

	// An empty directory still produces a tar entry for the root directory
	// itself (filepath.Walk visits the root). Verify there are no file entries.
	entryCount := 0
	for {
		hdr, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("tar.Reader.Next failed: %v", err)
		}
		if !hdr.FileInfo().IsDir() {
			t.Errorf("unexpected file entry in empty dir archive: %s", hdr.Name)
		}
		entryCount++
	}
	// Should have exactly 1 entry: the root directory.
	if entryCount > 1 {
		t.Errorf("expected at most 1 entry (root dir), got %d", entryCount)
	}
}
