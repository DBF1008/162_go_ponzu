package backup

import (
	"fmt"
	"net/http"
	"strconv"
)

// SetDownloadHeaders configures res to serve a file download named filename with
// the given content type and size in bytes. It is the single place download
// response headers are built so every backup and export source emits a
// consistent, correctly-named attachment. The filename is quoted (and escaped)
// in the Content-Disposition header.
func SetDownloadHeaders(res http.ResponseWriter, filename, contentType string, size int64) {
	res.Header().Set("Content-Type", contentType)
	res.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	res.Header().Set("Content-Length", strconv.FormatInt(size, 10))
}
