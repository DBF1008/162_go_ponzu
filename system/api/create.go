package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/ponzu-cms/ponzu/system/admin/upload"
	"github.com/ponzu-cms/ponzu/system/db"
	"github.com/ponzu-cms/ponzu/system/item"
)

// Createable accepts or rejects external POST requests to endpoints such as:
// /api/content/create?type=Review
type Createable interface {
	// Create enables external clients to submit content of a specific type
	Create(http.ResponseWriter, *http.Request) error
}

// Trustable allows external content to be auto-approved, meaning content sent
// as an Createable will be stored in the public content bucket
type Trustable interface {
	AutoApprove(http.ResponseWriter, *http.Request) error
}

func createContentHandler(res http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		res.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	err := req.ParseMultipartForm(1024 * 1024 * 4) // maxMemory 4MB
	if err != nil {
		log.Println("[Create] error:", err)
		res.WriteHeader(http.StatusInternalServerError)
		return
	}

	t := req.URL.Query().Get("type")
	if t == "" {
		res.WriteHeader(http.StatusBadRequest)
		return
	}

	p, found := item.Types[t]
	if !found {
		log.Println("[Create] attempt to submit unknown type:", t, "from:", req.RemoteAddr)
		res.WriteHeader(http.StatusNotFound)
		return
	}

	post := p()

	ext, ok := post.(Createable)
	if !ok {
		log.Println("[Create] rejected non-createable type:", t, "from:", req.RemoteAddr)
		res.WriteHeader(http.StatusBadRequest)
		return
	}

	// timestamps are stored as milliseconds since the unix epoch (UTC)
	ts := upload.NowMillis()
	if req.PostForm.Get("timestamp") == "" {
		req.PostForm.Set("timestamp", ts)
	}
	if req.PostForm.Get("updated") == "" {
		req.PostForm.Set("updated", ts)
	}

	hook, ok := post.(item.Hookable)
	if !ok {
		log.Println("[Create] error: Type", t, "does not implement item.Hookable or embed item.Item.")
		res.WriteHeader(http.StatusBadRequest)
		return
	}

	// store uploads, collapse multi-value fields, and decode into post so the
	// Hookable methods observe the values that will be saved
	err = upload.PrepareForm(req, post)
	if err != nil {
		var decErr *upload.DecodeError
		if errors.As(err, &decErr) {
			log.Println("[Create] error decoding post form:", t, err)
			res.WriteHeader(http.StatusBadRequest)
			return
		}
		log.Println("[Create] error preparing form:", t, err)
		res.WriteHeader(http.StatusInternalServerError)
		return
	}

	err = hook.BeforeAPICreate(res, req)
	if err != nil {
		log.Println("[Create] error calling BeforeCreate:", err)
		return
	}

	err = ext.Create(res, req)
	if err != nil {
		log.Println("[Create] error calling Accept:", err)
		return
	}

	err = hook.BeforeSave(res, req)
	if err != nil {
		log.Println("[Create] error calling BeforeSave:", err)
		return
	}

	// set specifier for db bucket in case content is/isn't Trustable
	var spec string

	// check if the content is Trustable should be auto-approved, if so the
	// content is immediately added to the public content API. If not, then it
	// is added to a "pending" list, only visible to Admins in the CMS and only
	// if the type implements editor.Mergable
	trusted, ok := post.(Trustable)
	if ok {
		err := trusted.AutoApprove(res, req)
		if err != nil {
			log.Println("[Create] error calling AutoApprove:", err)
			return
		}
	} else {
		spec = "__pending"
	}

	id, err := db.SetContent(t+spec+":-1", req.PostForm)
	if err != nil {
		log.Println("[Create] error calling SetContent:", err)
		res.WriteHeader(http.StatusInternalServerError)
		return
	}

	// set the target in the context so user can get saved value from db in hook
	ctx := context.WithValue(req.Context(), "target", fmt.Sprintf("%s:%d", t, id))
	req = req.WithContext(ctx)

	err = hook.AfterSave(res, req)
	if err != nil {
		log.Println("[Create] error calling AfterSave:", err)
		return
	}

	err = hook.AfterAPICreate(res, req)
	if err != nil {
		log.Println("[Create] error calling AfterAccept:", err)
		return
	}

	// create JSON response to send data back to client
	var data map[string]interface{}
	if spec != "" {
		spec = strings.TrimPrefix(spec, "__")
		data = map[string]interface{}{
			"status": spec,
			"type":   t,
		}
	} else {
		spec = "public"
		data = map[string]interface{}{
			"id":     id,
			"status": spec,
			"type":   t,
		}
	}

	resp := map[string]interface{}{
		"data": []map[string]interface{}{
			data,
		},
	}

	j, err := json.Marshal(resp)
	if err != nil {
		log.Println("[Create] error marshalling response to JSON:", err)
		res.WriteHeader(http.StatusInternalServerError)
		return
	}

	res.Header().Set("Content-Type", "application/json")
	_, err = res.Write(j)
	if err != nil {
		log.Println("[Create] error writing response:", err)
		return
	}

}
