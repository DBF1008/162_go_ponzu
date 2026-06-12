package analytics

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/boltdb/bolt"
	"github.com/ponzu-cms/ponzu/system/backup"
)

// Backup writes a snapshot of the analytics database to an HTTP response. The
// output is discarded if we get a cancellation signal.
func Backup(ctx context.Context, res http.ResponseWriter) error {
	errChan := make(chan error, 1)

	go func() {
		errChan <- store.View(func(tx *bolt.Tx) error {
			filename := fmt.Sprintf("analytics-%d.db.bak", time.Now().Unix())
			backup.SetDownloadHeaders(res, filename, "application/octet-stream", tx.Size())

			_, err := tx.WriteTo(res)
			return err
		})
	}()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-errChan:
		return err
	}
}
