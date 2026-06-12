package upload

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/ponzu-cms/ponzu/system/backup"
	"github.com/ponzu-cms/ponzu/system/cfg"
)

// Backup creates an archive of a project's uploads and writes it
// to the response as a download
func Backup(ctx context.Context, res http.ResponseWriter) error {
	filename := fmt.Sprintf("uploads-%d.bak.tar.gz", time.Now().Unix())
	return backup.ServeArchive(ctx, res, cfg.UploadDir(), filename)
}
