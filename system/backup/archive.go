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
// tarred and gzipped to the provided writer. The tar writer is always closed
// before the gzip writer so the archive is finalized correctly. Walking stops if
// ctx is cancelled.
func ArchiveFS(ctx context.Context, basedir string, w io.Writer) (err error) {
	gz := gzip.NewWriter(w)
	tarball := tar.NewWriter(gz)

	// Finalize in the right order: the tar writer wraps the gzip writer, so the
	// tar footer must be flushed into gz (tarball.Close) before the gzip footer
	// is written (gz.Close). Deferred calls run LIFO, so gz is registered first
	// and closed last. Surface the first close error if the walk itself succeeded.
	defer func() {
		if cerr := gz.Close(); cerr != nil && err == nil {
			err = cerr
		}
	}()
	defer func() {
		if cerr := tarball.Close(); cerr != nil && err == nil {
			err = cerr
		}
	}()

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

	return filepath.Walk(basedir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// stop processing if we get a cancellation signal
		if err := ctx.Err(); err != nil {
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

		if info.IsDir() {
			return nil
		}

		src, err := os.Open(path)
		if err != nil {
			return err
		}
		defer src.Close()

		_, err = io.Copy(tarball, src)
		return err
	})
}
