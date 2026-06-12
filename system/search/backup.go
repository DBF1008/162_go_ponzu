package search

import (
	"context"
	"net/http"
	"os"
	"path/filepath"

	"github.com/ponzu-cms/ponzu/system/backup"
	"github.com/ponzu-cms/ponzu/system/cfg"
)

// Backup creates an archive of a project's search index and writes it
// to the response as a download.
func Backup(ctx context.Context, res http.ResponseWriter) error {
	filename := backup.FormatFilename("search", ".bak.tar.gz")
	bk := filepath.Join(os.TempDir(), filename)

	f, err := os.Create(bk)
	if err != nil {
		return err
	}

	err = backup.ArchiveFS(ctx, cfg.SearchDir(), f)
	f.Close()
	if err != nil {
		os.Remove(bk)
		return err
	}

	dl, err := backup.NewFileDownload(bk, filename, "application/octet-stream")
	if err != nil {
		os.Remove(bk)
		return err
	}

	return dl.Serve(res)
}
