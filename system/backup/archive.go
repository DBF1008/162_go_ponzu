package backup

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"io"
	"os"
	"path/filepath"
)

// ArchiveFS walks the filesystem starting from basedir writing files encountered
// tarred and gzipped to the provided writer. Cancellation is checked at each
// file entry; if ctx is cancelled the walk stops and ctx.Err() is returned.
func ArchiveFS(ctx context.Context, basedir string, w io.Writer) error {
	gz := gzip.NewWriter(w)
	tarball := tar.NewWriter(gz)

	absPath, err := filepath.Abs(basedir)
	if err != nil {
		return err
	}

	info, err := os.Lstat(absPath)
	if err != nil {
		return err
	}

	if info.Mode()&os.ModeSymlink == os.ModeSymlink {
		// This is a symlink - we need to follow it
		bdir, err := os.Readlink(absPath)
		if err != nil {
			return err
		}
		basedir = bdir
	}

	walkFn := func(path string, info os.FileInfo, err error) error {
		// Check cancellation at each step synchronously — no goroutine needed.
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if err != nil {
			return err
		}

		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}

		hdr.Name = path

		err = tarball.WriteHeader(hdr)
		if err != nil {
			return err
		}

		if !info.IsDir() {
			src, err := os.Open(path)
			if err != nil {
				return err
			}
			defer src.Close()

			_, err = io.Copy(tarball, src)
			if err != nil {
				return err
			}

			err = tarball.Flush()
			if err != nil {
				return err
			}

			err = gz.Flush()
			if err != nil {
				return err
			}
		}

		return nil
	}

	err = filepath.Walk(basedir, walkFn)
	if err != nil {
		return err
	}

	// Correct close order: tar first (flushes end-of-archive blocks into
	// the gzip writer), then gzip (writes the gzip trailer).
	err = tarball.Close()
	if err != nil {
		return err
	}
	err = gz.Close()
	if err != nil {
		return err
	}

	return nil
}
