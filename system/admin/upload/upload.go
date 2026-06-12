// Package upload provides a re-usable file upload and storage utility for Ponzu
// systems to handle multipart form data.
package upload

import (
	"fmt"
	"log"
	"mime/multipart"
	"net/http"
	"net/url"
	"time"

	"github.com/ponzu-cms/ponzu/system/db"
	"github.com/ponzu-cms/ponzu/system/item"
	"github.com/ponzu-cms/ponzu/system/storage"
)

// StoreFiles stores file uploads at paths like /YYYY/MM/filename.ext
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

	store := storage.New()

	// loop over all files and save to the configured storage backend
	for name, fds := range req.MultipartForm.File {
		filename, err := item.NormalizeString(fds[0].Filename)
		if err != nil {
			return nil, err
		}

		src, err := fds[0].Open()
		if err != nil {
			err := fmt.Errorf("Couldn't open uploaded file: %s", err)
			return nil, err

		}
		defer src.Close()

		contentType := fds[0].Header.Get("Content-Type")

		// Store file via the configured backend (local disk or S3).
		// Local returns "/api/uploads/YYYY/MM/filename.ext";
		// S3 returns a full URL like "https://bucket.s3.region.amazonaws.com/...".
		urlPath, err := store.Store(src, filename, contentType)
		if err != nil {
			err := fmt.Errorf("Failed to store uploaded file: %s", err)
			return nil, err
		}

		urlPaths[name] = urlPath

		// add upload information to db
		go storeFileInfo(urlPath, filename, contentType, fds)
	}

	return urlPaths, nil
}

func storeFileInfo(urlPath, filename, contentType string, fds []*multipart.FileHeader) {
	data := url.Values{
		"name":           []string{filename},
		"path":           []string{urlPath},
		"content_type":   []string{contentType},
		"content_length": []string{fmt.Sprintf("%d", fds[0].Size)},
	}

	_, err := db.SetUpload("__uploads:-1", data)
	if err != nil {
		log.Println("Error saving file upload record to database:", err)
	}
}
