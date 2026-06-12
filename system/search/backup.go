package search

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/ponzu-cms/ponzu/system/backup"
	"github.com/ponzu-cms/ponzu/system/cfg"
)

// Backup creates an archive of a project's search index and writes it
// to the response as a download
func Backup(ctx context.Context, res http.ResponseWriter) error {
	filename := fmt.Sprintf("search-%d.bak.tar.gz", time.Now().Unix())
	return backup.ServeArchive(ctx, res, cfg.SearchDir(), filename)
}
