// Package upload provides a re-usable file upload and storage utility for Ponzu
// systems to handle multipart form data.
package upload

import (
	"fmt"
	"log"
	"mime/multipart"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"time"

	"github.com/ponzu-cms/ponzu/system/admin/upload/cloud"
	"github.com/ponzu-cms/ponzu/system/db"
	"github.com/ponzu-cms/ponzu/system/item"
)

// StoreFiles stores file uploads at paths like /YYYY/MM/filename.ext
//
// Files are written through the configured storage backend (cloud.Provider):
// the local filesystem by default, or an S3-compatible object storage service
// when credentials are configured. Both the public API and the admin upload
// entry points funnel through this function, so both honor the selected
// backend and receive a stable, accessible URL.
func StoreFiles(req *http.Request) (map[string]string, error) {
	err := req.ParseMultipartForm(1024 * 1024 * 4) // maxMemory 4MB
	if err != nil {
		return nil, err
	}

	ts := req.FormValue("timestamp") // timestamp in milliseconds since unix epoch

	if ts == "" {
		ts = fmt.Sprintf("%d", int64(time.Nanosecond)*time.Now().UnixNano()/int64(time.Millisecond)) // Unix() returns seconds since unix epoch
	}

	// To use for FormValue name:urlPath
	urlPaths := make(map[string]string)

	if len(req.MultipartForm.File) == 0 {
		return urlPaths, nil
	}

	req.Form.Set("timestamp", ts)

	// parse the timestamp to derive the YYYY/MM storage key prefix
	i, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return nil, err
	}

	tm := time.Unix(int64(i/1000), int64(i%1000))

	// select the storage backend: S3-compatible object storage when configured,
	// otherwise the local filesystem.
	store := cloud.Provider()

	// loop over all files and save them through the storage backend
	for name, fds := range req.MultipartForm.File {
		filename, err := item.NormalizeString(fds[0].Filename)
		if err != nil {
			return nil, err
		}

		src, err := fds[0].Open()
		if err != nil {
			return nil, fmt.Errorf("Couldn't open uploaded file: %s", err)
		}

		// storage key relative to the uploads root: YYYY/MM/filename
		key := fmt.Sprintf("%d/%02d/%s", tm.Year(), tm.Month(), filename)

		res, err := store.Save(key, src, fds[0].Size, fds[0].Header.Get("Content-Type"))
		src.Close()
		if err != nil {
			return nil, err
		}

		// add name:urlPath to req.PostForm to be inserted into db
		urlPaths[name] = res.URL

		// add upload information to db (use the stored object's final name, which
		// may differ from the original on a local-disk name collision)
		go storeFileInfo(res.Size, path.Base(res.Key), res.URL, fds)
	}

	return urlPaths, nil
}

func storeFileInfo(size int64, filename, urlPath string, fds []*multipart.FileHeader) {
	data := url.Values{
		"name":           []string{filename},
		"path":           []string{urlPath},
		"content_type":   []string{fds[0].Header.Get("Content-Type")},
		"content_length": []string{fmt.Sprintf("%d", size)},
	}

	_, err := db.SetUpload("__uploads:-1", data)
	if err != nil {
		log.Println("Error saving file upload record to database:", err)
	}
}
