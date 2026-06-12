// Package upload provides a re-usable file upload and storage utility for Ponzu
// systems to handle multipart form data.
package upload

import (
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/ponzu-cms/ponzu/system/cfg"

	"github.com/ponzu-cms/ponzu/system/db"
	"github.com/ponzu-cms/ponzu/system/item"

	"github.com/gorilla/schema"
)

// NowMillis returns the current time as a millisecond Unix timestamp string in
// UTC, the timestamp format Ponzu stores for content's timestamp/updated fields.
func NowMillis() string {
	return fmt.Sprintf("%d", time.Now().UTC().UnixNano()/int64(time.Millisecond))
}

// DecodeError wraps a failure to decode the form into a content item. Callers
// use it (via errors.As) to distinguish a malformed client submission (HTTP
// 400) from a server-side file-storage failure returned by PrepareForm.
type DecodeError struct {
	Err error
}

func (e *DecodeError) Error() string { return e.Err.Error() }

func (e *DecodeError) Unwrap() error { return e.Err }

// PrepareForm runs the steps every content save path shares before the data is
// handed to the storage layer: it stores any uploaded files and writes their
// URL paths back into the form, collapses multi-value fields, and decodes the
// resulting form into post so its Hookable methods observe the values that will
// be saved. It is the common pre-save step for the content API create/update
// handlers and the admin edit handler, keeping them consistent on upload fields
// and multi-value fields. The caller is responsible for setting any timestamps
// on req.PostForm beforehand (StoreFiles uses them to date the upload path).
//
// A failure to store files is returned as-is (a server error); a failure to
// decode the form is returned wrapped in *DecodeError (a client error).
func PrepareForm(req *http.Request, post interface{}) error {
	// StoreFiles parses the multipart form and persists any uploaded files
	urlPaths, err := StoreFiles(req)
	if err != nil {
		return err
	}

	for name, urlPath := range urlPaths {
		req.PostForm.Set(name, urlPath)
	}

	// collapse multi-value fields (ex. checkbox/repeater) into slice values
	item.FormatMultiValueFields(req.PostForm)

	// decode the form into post so its Hookable methods see proper values
	dec := schema.NewDecoder()
	dec.IgnoreUnknownKeys(true)
	dec.SetAliasTag("json")

	err = dec.Decode(post, req.PostForm)
	if err != nil {
		return &DecodeError{Err: err}
	}

	return nil
}

// StoreFiles stores file uploads at paths like /YYYY/MM/filename.ext
func StoreFiles(req *http.Request) (map[string]string, error) {
	err := req.ParseMultipartForm(1024 * 1024 * 4) // maxMemory 4MB
	if err != nil {
		return nil, err
	}

	ts := req.FormValue("timestamp") // timestamp in milliseconds since unix epoch

	if ts == "" {
		ts = NowMillis()
	}

	// To use for FormValue name:urlPath
	urlPaths := make(map[string]string)

	if len(req.MultipartForm.File) == 0 {
		return urlPaths, nil
	}

	req.Form.Set("timestamp", ts)

	// get or create upload directory to save files from request
	i, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return nil, err
	}

	tm := time.Unix(int64(i/1000), int64(i%1000))

	urlPathPrefix := "api"
	uploadDirName := "uploads"
	uploadDir := filepath.Join(cfg.UploadDir(), fmt.Sprintf("%d", tm.Year()), fmt.Sprintf("%02d", tm.Month()))
	err = os.MkdirAll(uploadDir, os.ModeDir|os.ModePerm)
	if err != nil {
		return nil, err
	}

	// loop over all files and save them to disk
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

		// check if file at path exists, if so, add timestamp to file
		absPath := filepath.Join(uploadDir, filename)

		if _, err := os.Stat(absPath); !os.IsNotExist(err) {
			filename = fmt.Sprintf("%d-%s", time.Now().Unix(), filename)
			absPath = filepath.Join(uploadDir, filename)
		}

		// save to disk (TODO: or check if S3 credentials exist, & save to cloud)
		dst, err := os.Create(absPath)
		if err != nil {
			err := fmt.Errorf("Failed to create destination file for upload: %s", err)
			return nil, err
		}

		// copy file from src to dst on disk
		var size int64
		if size, err = io.Copy(dst, src); err != nil {
			err := fmt.Errorf("Failed to copy uploaded file to destination: %s", err)
			return nil, err
		}

		// add name:urlPath to req.PostForm to be inserted into db
		urlPath := fmt.Sprintf("/%s/%s/%d/%02d/%s", urlPathPrefix, uploadDirName, tm.Year(), tm.Month(), filename)
		urlPaths[name] = urlPath

		// add upload information to db
		go storeFileInfo(size, filename, urlPath, fds)
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
