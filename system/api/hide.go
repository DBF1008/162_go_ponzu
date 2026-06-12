package api

import (
	"net/http"

	"github.com/ponzu-cms/ponzu/system/item"
)

func hide(res http.ResponseWriter, req *http.Request, it interface{}) bool {
	hidden, err := isHidden(res, req, it)
	if err != nil {
		res.WriteHeader(http.StatusInternalServerError)
		return true
	}

	if hidden {
		res.WriteHeader(http.StatusNotFound)
		return true
	}

	return false
}

// isHidden reports whether it should be hidden from the public content API,
// without writing to res (unlike hide). This lets a caller evaluate several types
// within one request and skip the hidden ones rather than aborting the whole
// response. The returned error is non-nil only when Hide itself fails
// unexpectedly.
func isHidden(res http.ResponseWriter, req *http.Request, it interface{}) (bool, error) {
	h, ok := it.(item.Hideable)
	if !ok {
		return false, nil
	}

	err := h.Hide(res, req)
	if err == item.ErrAllowHiddenItem {
		return false, nil
	}

	if err != nil {
		return false, err
	}

	return true, nil
}
