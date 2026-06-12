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
			backup.SetDBBackupHeaders(res, filename, int64(tx.Size()))

			// Wrap response writer to detect context cancellation on each
			// Write, allowing tx.WriteTo to exit promptly on cancel.
			cw := backup.ContextWriter(ctx, res)
			_, err := tx.WriteTo(cw)
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
