// Package merge combines scored search hits from multiple Ponzu content-type
// indexes into a single relevance-ordered, paginated result set. It is kept free
// of any search-engine or storage dependencies so the ranking and pagination
// logic can be exercised in isolation.
package merge

import "sort"

// Hit is a single search result: a Ponzu target ("Type:ID") and the relevance
// score the originating index assigned to it.
type Hit struct {
	ID    string
	Score float64
}

// Scored flattens the hits from every group and returns their IDs ordered by
// descending relevance score. Ties are broken by ID so that pagination across
// repeated calls is stable (Bleve breaks score ties by an internal, non-portable
// document id, which would make pages overlap or skip). The offset/count window
// is then applied to the merged ordering, matching the single-type search
// semantics:
//
//	count < 0  => every hit from offset onward ("all")
//	count == 0 => an empty page
//	offset < 0 => treated as 0
//
// The returned slice is always non-nil so callers can range over it directly.
func Scored(groups [][]Hit, count, offset int) []string {
	var all []Hit
	for _, g := range groups {
		all = append(all, g...)
	}

	sort.SliceStable(all, func(i, j int) bool {
		if all[i].Score != all[j].Score {
			return all[i].Score > all[j].Score
		}
		return all[i].ID < all[j].ID
	})

	if offset < 0 {
		offset = 0
	}

	ids := []string{}
	if offset >= len(all) {
		return ids
	}

	// Window the merged ordering. Comparing count to the remaining length
	// (rather than computing offset+count) keeps this safe from overflow when a
	// caller passes a very large count to mean "as many as exist".
	end := len(all)
	if count >= 0 && count < end-offset {
		end = offset + count
	}

	for _, h := range all[offset:end] {
		ids = append(ids, h.ID)
	}
	return ids
}
