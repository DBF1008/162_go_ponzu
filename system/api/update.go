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

// Updateable accepts or rejects update POST requests to endpoints such as:
// /api/content/update?type=Review&id=1
type Updateable interface {
	// Update enabled external clients to update content of a specific type
	Update(http.ResponseWriter, *http.Request) error
}

func updateContentHandler(res http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		res.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	err := req.ParseMultipartForm(1024 * 1024 * 4) // maxMemory 4MB
	if err != nil {
		log.Println("[Update] error:", err)
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
		log.Println("[Update] attempt to update content unknown type:", t, "from:", req.RemoteAddr)
		res.WriteHeader(http.StatusNotFound)
		return
	}

	id := req.URL.Query().Get("id")
	if !db.IsValidID(id) {
		log.Println("[Update] attempt to update content with missing or invalid id from:", req.RemoteAddr)
		res.WriteHeader(http.StatusBadRequest)
		return
	}

	post := p()

	j, err := db.Content(t + ":" + id)
	if err != nil {
		log.Println("[Update] error getting content for type:", t, err)
		res.WriteHeader(http.StatusInternalServerError)
		return
	}

	err = json.Unmarshal(j, post)
	if err != nil {
		log.Println("[Update] error populating data in type:", t, err)
		res.WriteHeader(http.StatusInternalServerError)
		return
	}

	ext, ok := post.(Updateable)
	if !ok {
		log.Println("[Update] rejected non-updateable type:", t, "from:", req.RemoteAddr)
		res.WriteHeader(http.StatusBadRequest)
		return
	}

	// an update refreshes "updated"; "timestamp" is intentionally left untouched
	// so the db merge preserves the content's original creation time
	req.PostForm.Set("updated", upload.NowMillis())

	hook, ok := post.(item.Hookable)
	if !ok {
		log.Println("[Update] error: Type", t, "does not implement item.Hookable or embed item.Item.")
		res.WriteHeader(http.StatusBadRequest)
		return
	}

	// store uploads, collapse multi-value fields, and decode into post so the
	// Hookable methods observe the values that will be saved
	err = upload.PrepareForm(req, post)
	if err != nil {
		var decErr *upload.DecodeError
		if errors.As(err, &decErr) {
			log.Println("[Update] error decoding post form:", t, err)
			res.WriteHeader(http.StatusBadRequest)
			return
		}
		log.Println("[Update] error preparing form:", t, err)
		res.WriteHeader(http.StatusInternalServerError)
		return
	}

	err = hook.BeforeAPIUpdate(res, req)
	if err != nil {
		log.Println("[Update] error calling BeforeAPIUpdate:", err)
		if err == ErrNoAuth {
			// BeforeAPIUpdate can check user.IsValid(req) for auth
			res.WriteHeader(http.StatusUnauthorized)
		}
		return
	}

	err = ext.Update(res, req)
	if err != nil {
		log.Println("[Update] error calling Update:", err)
		if err == ErrNoAuth {
			// Update can check user.IsValid(req) or other forms of validation for auth
			res.WriteHeader(http.StatusUnauthorized)
		}
		return
	}

	err = hook.BeforeSave(res, req)
	if err != nil {
		log.Println("[Update] error calling BeforeSave:", err)
		return
	}

	// set specifier for db bucket in case content is/isn't Trustable
	var spec string

	_, err = db.UpdateContent(t+spec+":"+id, req.PostForm)
	if err != nil {
		log.Println("[Update] error calling UpdateContent:", err)
		res.WriteHeader(http.StatusInternalServerError)
		return
	}

	// set the target in the context so user can get saved value from db in hook
	ctx := context.WithValue(req.Context(), "target", fmt.Sprintf("%s:%s", t, id))
	req = req.WithContext(ctx)

	err = hook.AfterSave(res, req)
	if err != nil {
		log.Println("[Update] error calling AfterSave:", err)
		return
	}

	err = hook.AfterAPIUpdate(res, req)
	if err != nil {
		log.Println("[Update] error calling AfterAPIUpdate:", err)
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

	j, err = json.Marshal(resp)
	if err != nil {
		log.Println("[Update] error marshalling response to JSON:", err)
		res.WriteHeader(http.StatusInternalServerError)
		return
	}

	res.Header().Set("Content-Type", "application/json")
	_, err = res.Write(j)
	if err != nil {
		log.Println("[Update] error writing response:", err)
		return
	}

}
