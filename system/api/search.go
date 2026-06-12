package api

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/ponzu-cms/ponzu/system/db"
	"github.com/ponzu-cms/ponzu/system/item"
	"github.com/ponzu-cms/ponzu/system/search"

	"github.com/tidwall/sjson"
)

func searchContentHandler(res http.ResponseWriter, req *http.Request) {
	qs := req.URL.Query()

	// Support both single-type (?type=X) and multi-type (?type=X&type=Y)
	// queries. Query()["type"] returns nil when the parameter is absent and
	// a slice (possibly of length 1) when present.
	types := qs["type"]
	if len(types) == 0 {
		res.WriteHeader(http.StatusBadRequest)
		return
	}

	// Validate every requested type and collect the corresponding instances.
	// Reject the entire request if any type is unknown or hidden so callers
	// get a clear signal rather than silently missing results.
	instances := make([]interface{}, len(types))
	for i, t := range types {
		it, ok := item.Types[t]
		if !ok {
			res.WriteHeader(http.StatusBadRequest)
			return
		}

		inst := it()
		if hide(res, req, inst) {
			return
		}

		instances[i] = inst
	}

	q, err := url.QueryUnescape(qs.Get("q"))
	if err != nil {
		res.WriteHeader(http.StatusInternalServerError)
		return
	}

	// q must be set
	if q == "" {
		res.WriteHeader(http.StatusBadRequest)
		return
	}

	count, err := strconv.Atoi(qs.Get("count")) // int: determines number of posts to return (10 default, -1 is all)
	if err != nil {
		if qs.Get("count") == "" {
			count = 10
		} else {
			res.WriteHeader(http.StatusInternalServerError)
			return
		}
	}

	offset, err := strconv.Atoi(qs.Get("offset")) // int: multiplier of count for pagination (0 default)
	if err != nil {
		if qs.Get("offset") == "" {
			offset = 0
		} else {
			res.WriteHeader(http.StatusInternalServerError)
			return
		}
	}

	// execute search for query provided; use MultiTypeQuery which merges
	// results across all requested types by descending relevance score
	// before applying count/offset pagination
	matches, err := search.MultiTypeQuery(types, q, count, offset)
	if err == search.ErrNoIndex {
		res.WriteHeader(http.StatusNotFound)
		return
	}
	if err != nil {
		log.Println("[search] Error:", err)
		res.WriteHeader(http.StatusInternalServerError)
		return
	}

	// respond with json formatted results
	bb, err := db.ContentMulti(matches)
	if err != nil {
		log.Println("[search] Error:", err)
		res.WriteHeader(http.StatusInternalServerError)
		return
	}

	// Build a fast lookup from type name to instance, used below for
	// per-item push/omit which may differ across types in a multi-type
	// result set.
	typeInst := make(map[string]interface{}, len(types))
	for i, t := range types {
		typeInst[t] = instances[i]
	}

	// If we have matches, push the first as it's matched by highest
	// relevance. Determine its type from the target's "Type:ID" form so
	// the correct Pushable implementation is used.
	if len(bb) > 0 && len(matches) > 0 {
		ns := strings.SplitN(matches[0], ":", 2)[0]
		if inst, ok := typeInst[ns]; ok {
			push(res, req, inst, bb[0])
		}
	}

	var result = []json.RawMessage{}
	for i := range bb {
		result = append(result, bb[i])
	}

	j, err := fmtJSON(result...)
	if err != nil {
		res.WriteHeader(http.StatusInternalServerError)
		return
	}

	// Apply Omit per-type. For single-type requests this matches the
	// previous behavior exactly; for multi-type requests each item is
	// processed with its own type's Omittable implementation.
	j, err = omitMultiType(res, req, matches, typeInst, j)
	if err != nil {
		res.WriteHeader(http.StatusInternalServerError)
		return
	}

	sendData(res, req, j)
}

// omitMultiType applies Omittable field removal to a JSON response containing
// results from one or more content types. For each item in the "data" array it
// looks up the originating type from the corresponding target string and, if
// that type implements item.Omittable, removes the declared fields.
func omitMultiType(res http.ResponseWriter, req *http.Request, targets []string, typeInst map[string]interface{}, data []byte) ([]byte, error) {
	for i, target := range targets {
		ns := strings.SplitN(target, ":", 2)[0]
		inst, ok := typeInst[ns]
		if !ok {
			continue
		}

		om, ok := inst.(item.Omittable)
		if !ok {
			continue
		}

		fields, err := om.Omit(res, req)
		if err != nil {
			return nil, err
		}

		// Remove each declared field from this specific item in the
		// response's "data" array. The path uses the item's index so
		// only that item is affected.
		for _, field := range fields {
			data, err = sjson.DeleteBytes(data, fmt.Sprintf("data.%d.%s", i, field))
			if err != nil {
				log.Println("Error omitting field:", field, "from item.Omittable:", om)
				return nil, err
			}
		}
	}

	return data, nil
}
