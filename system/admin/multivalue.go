package admin

import (
	"net/url"
	"sort"
	"strconv"
	"strings"
)

// restructureMultiValueFields collapses indexed multi-value form fields of the
// form "name.N" (e.g. "categories.0", "categories.1") into a single repeated
// key ("categories") whose values are ordered by N. The editor repeaters emit
// one indexed input per value, but gorilla/schema and the rest of the save
// pipeline expect repeated keys for slice fields, so the indexed inputs must be
// collapsed before decoding/storing.
//
// The form is mutated in place. The conversion is robust to:
//   - multiple distinct multi-value fields in one form: each field is grouped
//     independently, so values never bleed from one field into another.
//   - non-contiguous indices left behind after an option is removed in the UI
//     (e.g. "0", "2", "3"): indices are sorted numerically and any gaps are
//     simply closed, preserving the remaining order.
//   - empty-string values: a "None" selection stores an empty string on
//     purpose, so it is kept at its position (including position 0) instead of
//     being treated as "unset".
func restructureMultiValueFields(form url.Values) {
	// group[field][index] holds the value(s) submitted for "field.index".
	group := make(map[string]map[int][]string)

	for k, v := range form {
		dot := strings.Index(k, ".")
		if dot < 0 {
			continue
		}

		field := k[:dot]
		idx, err := strconv.Atoi(k[dot+1:])
		if field == "" || err != nil || idx < 0 {
			// not an indexed multi-value key (e.g. a literal dotted name);
			// leave it untouched for the decoder to handle.
			continue
		}

		if group[field] == nil {
			group[field] = make(map[int][]string)
		}
		group[field][idx] = append(group[field][idx], v...)

		// discard the indexed key; it is replaced by the collapsed field below.
		// deleting during range over a map is safe in Go.
		form.Del(k)
	}

	for field, byIndex := range group {
		indices := make([]int, 0, len(byIndex))
		for idx := range byIndex {
			indices = append(indices, idx)
		}
		sort.Ints(indices)

		// preserve any value already stored under the plain field name, then
		// append the indexed values in ascending index order.
		ordered := append([]string(nil), form[field]...)
		for _, idx := range indices {
			ordered = append(ordered, byIndex[idx]...)
		}

		form[field] = ordered
	}
}
