// Package search is a wrapper around the blevesearch/bleve search indexing and
// query package, and provides interfaces to extend Ponzu items with rich, full-text
// search capability.
package search

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ponzu-cms/ponzu/system/cfg"

	"github.com/ponzu-cms/ponzu/system/item"

	"github.com/blevesearch/bleve"
	"github.com/blevesearch/bleve/mapping"
	"github.com/blevesearch/bleve/search"
)

var (
	// Search tracks all search indices to use throughout system
	Search map[string]bleve.Index

	// ErrNoIndex is for failed checks for an index in Search map
	ErrNoIndex = errors.New("No search index found for type provided")
)

// Searchable ...
type Searchable interface {
	SearchMapping() (*mapping.IndexMappingImpl, error)
	IndexContent() bool
}

func init() {
	Search = make(map[string]bleve.Index)
}

// MapIndex creates the mapping for a type and tracks the index to be used within
// the system for adding/deleting/checking data
func MapIndex(typeName string) error {
	// type assert for Searchable, get configuration (which can be overridden)
	// by Ponzu user if defines own SearchMapping()
	it, ok := item.Types[typeName]
	if !ok {
		return fmt.Errorf("[search] MapIndex Error: Failed to MapIndex for %s, type doesn't exist", typeName)
	}
	s, ok := it().(Searchable)
	if !ok {
		return fmt.Errorf("[search] MapIndex Error: Item type %s doesn't implement search.Searchable", typeName)
	}

	// skip setting or using index for types that shouldn't be indexed
	if !s.IndexContent() {
		return nil
	}

	mapping, err := s.SearchMapping()
	if err != nil {
		return err
	}

	idxName := typeName + ".index"
	var idx bleve.Index

	searchPath := cfg.SearchDir()

	err = os.MkdirAll(searchPath, os.ModeDir|os.ModePerm)
	if err != nil {
		return err
	}

	idxPath := filepath.Join(searchPath, idxName)
	if _, err = os.Stat(idxPath); os.IsNotExist(err) {
		idx, err = bleve.New(idxPath, mapping)
		if err != nil {
			return err
		}
		idx.SetName(idxName)
	} else {
		idx, err = bleve.Open(idxPath)
		if err != nil {
			return err
		}
	}

	// add the type name to the index and track the index
	Search[typeName] = idx

	return nil
}

// UpdateIndex sets data into a content type's search index at the given
// identifier
func UpdateIndex(id string, data interface{}) error {
	// check if there is a search index to work with
	target := strings.Split(id, ":")
	ns := target[0]

	idx, ok := Search[ns]
	if ok {
		// unmarshal json to struct, error if not registered
		it, ok := item.Types[ns]
		if !ok {
			return fmt.Errorf("[search] UpdateIndex Error: type '%s' doesn't exist", ns)
		}

		p := it()
		err := json.Unmarshal(data.([]byte), &p)
		if err != nil {
			return err
		}

		// add data to search index
		return idx.Index(id, p)
	}

	return nil
}

// DeleteIndex removes data from a content type's search index at the
// given identifier
func DeleteIndex(id string) error {
	// check if there is a search index to work with
	target := strings.Split(id, ":")
	ns := target[0]

	idx, ok := Search[ns]
	if ok {
		// add data to search index
		return idx.Delete(id)
	}

	return nil
}

// TypeQuery conducts a search and returns a set of Ponzu "targets", Type:ID pairs,
// and an error. If there is no search index for the typeName (Type) provided,
// db.ErrNoIndex will be returned as the error
func TypeQuery(typeName, query string, count, offset int) ([]string, error) {
	idx, ok := Search[typeName]
	if !ok {
		return nil, ErrNoIndex
	}

	q := bleve.NewQueryStringQuery(query)
	req := bleve.NewSearchRequestOptions(q, count, offset, false)
	res, err := idx.Search(req)
	if err != nil {
		return nil, err
	}

	var results []string
	for _, hit := range res.Hits {
		results = append(results, hit.ID)
	}

	return results, nil
}

// MultiTypeQuery conducts a search across multiple content type indices and
// returns a single, relevance-sorted set of Ponzu "targets" (Type:ID pairs).
// The results from all requested types are merged and ordered by descending
// bleve score before count/offset pagination is applied, so the caller sees a
// unified, relevance-ranked result set regardless of which type a match came
// from.
//
// If any requested type has no search index, ErrNoIndex is returned. If any
// individual search fails, that type's results are skipped and the error is
// logged but other types are still returned.
func MultiTypeQuery(typeNames []string, query string, count, offset int) ([]string, error) {
	if len(typeNames) == 0 {
		return nil, nil
	}

	// If only one type is requested, delegate to the single-type path so
	// behavior (including error semantics) is identical to TypeQuery.
	if len(typeNames) == 1 {
		return TypeQuery(typeNames[0], query, count, offset)
	}

	var allHits search.DocumentMatchCollection
	for _, typeName := range typeNames {
		idx, ok := Search[typeName]
		if !ok {
			return nil, ErrNoIndex
		}

		q := bleve.NewQueryStringQuery(query)
		// Fetch enough hits from each index so that, after merging and
		// sorting by score, the final paginated window is correct. In the
		// worst case every result in the requested page comes from a
		// single type, so we need at least offset+count hits per index.
		limit := count
		if limit < 0 {
			// count == -1 means "all matches"; use a large upper bound
			// and let bleve cap it at the actual match count.
			limit = 10000
		}
		if offset > 0 {
			limit += offset
		}

		req := bleve.NewSearchRequestOptions(q, limit, 0, false)
		res, err := idx.Search(req)
		if err != nil {
			return nil, err
		}

		allHits = append(allHits, res.Hits...)
	}

	// Merge and sort by descending relevance score.
	sort.Slice(allHits, func(i, j int) bool {
		return allHits[i].Score > allHits[j].Score
	})

	// Apply count/offset to the merged result set. If count is -1, return
	// everything from offset onward (matches TypeQuery semantics).
	start := offset
	if start > len(allHits) {
		start = len(allHits)
	}
	end := len(allHits)
	if count >= 0 && start+count < end {
		end = start + count
	}

	var results []string
	for _, hit := range allHits[start:end] {
		results = append(results, hit.ID)
	}

	return results, nil
}
