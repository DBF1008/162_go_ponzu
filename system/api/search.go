package api

import (
	"encoding/json"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/ponzu-cms/ponzu/system/db"
	"github.com/ponzu-cms/ponzu/system/item"
	"github.com/ponzu-cms/ponzu/system/search"
)

func searchContentHandler(res http.ResponseWriter, req *http.Request) {
	qs := req.URL.Query()

	// One or more types may be requested, via repeated type params
	// (?type=A&type=B) and/or comma-separated values (?type=A,B). A single type
	// behaves exactly as it always has; multiple types produce one result set
	// merged by relevance across the types.
	types := parseTypes(qs["type"])
	if len(types) == 0 {
		res.WriteHeader(http.StatusBadRequest)
		return
	}

	// Every requested type must be registered. Only public (non-hidden) types
	// are searched; hidden types are skipped rather than leaked.
	var publicTypes []string
	for _, t := range types {
		it, ok := item.Types[t]
		if !ok {
			res.WriteHeader(http.StatusBadRequest)
			return
		}

		hidden, err := isHidden(res, req, it())
		if err != nil {
			res.WriteHeader(http.StatusInternalServerError)
			return
		}
		if hidden {
			continue
		}

		publicTypes = append(publicTypes, t)
	}

	// nothing public to search (e.g. every requested type is hidden)
	if len(publicTypes) == 0 {
		res.WriteHeader(http.StatusNotFound)
		return
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

	// Execute the search across all requested public types, merging results by
	// relevance and applying count/offset to the combined set. If none of the
	// types has a search index, send 404.
	matches, err := search.TypesQuery(publicTypes, q, count, offset)
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

	// if we have matches, push the first as it's matched by relevance, using the
	// type of that specific match (results may span multiple types)
	if len(bb) > 0 {
		if it, ok := item.Types[typeOfTarget(matches[0])]; ok {
			push(res, req, it(), bb[0])
		}
	}

	// Omit each item's fields according to its own type before assembling the
	// set, since results may span multiple types with different omit rules.
	var result = []json.RawMessage{}
	for i := range bb {
		data := bb[i]
		if it, ok := item.Types[typeOfTarget(matches[i])]; ok {
			data, err = omitItem(res, req, it(), data)
			if err != nil {
				res.WriteHeader(http.StatusInternalServerError)
				return
			}
		}
		result = append(result, data)
	}

	j, err := fmtJSON(result...)
	if err != nil {
		res.WriteHeader(http.StatusInternalServerError)
		return
	}

	sendData(res, req, j)
}

// parseTypes extracts the set of requested content types from the repeated and/or
// comma-separated "type" query values, trimming whitespace, dropping empties, and
// de-duplicating while preserving the order in which types first appear.
func parseTypes(values []string) []string {
	var types []string
	seen := make(map[string]bool)
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			t := strings.TrimSpace(part)
			if t == "" || seen[t] {
				continue
			}
			seen[t] = true
			types = append(types, t)
		}
	}

	return types
}

// typeOfTarget returns the content type from a Ponzu target string ("Type:ID").
func typeOfTarget(target string) string {
	return strings.SplitN(target, ":", 2)[0]
}
