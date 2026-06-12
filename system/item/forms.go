package item

import (
	"fmt"
	"net/url"
	"strings"
)

// FormatMultiValueFields collapses the indexed form keys produced by repeating
// editor fields (e.g. checkboxes and repeaters) into a single multi-value key
// so they decode into slice struct fields. It rewrites data in place:
//
//	fieldX.0: value1, fieldX.1: value2  =>  fieldX: []string{value1, value2}
//
// This is the single source of truth shared by the content API create/update
// handlers, the admin edit handler, and the db storage layer (postToJSON and
// mergeData), so every content save path treats multi-value fields identically.
// It is safe to call more than once on the same data: once the indexed keys are
// collapsed there are none left to find, so subsequent calls are no-ops.
func FormatMultiValueFields(data url.Values) {
	// check for any multi-value fields (ex. checkbox fields)
	// and correctly format for db storage. Essentially, we need
	// fieldX.0: value1, fieldX.1: value2 => fieldX: []string{value1, value2}
	fieldOrderValue := make(map[string]map[string][]string)
	for k, v := range data {
		if strings.Contains(k, ".") {
			fo := strings.Split(k, ".")

			// put the order and the field value into map
			field := string(fo[0])
			order := string(fo[1])
			if len(fieldOrderValue[field]) == 0 {
				fieldOrderValue[field] = make(map[string][]string)
			}

			// orderValue is 0:[?type=Thing&id=1]
			orderValue := fieldOrderValue[field]
			orderValue[order] = v
			fieldOrderValue[field] = orderValue

			// discard the form value with name.N
			data.Del(k)
		}
	}

	// add/set the key & value to the data in order
	for f, ov := range fieldOrderValue {
		for i := 0; i < len(ov); i++ {
			position := fmt.Sprintf("%d", i)
			fieldValue := ov[position]

			if data.Get(f) == "" {
				for i, fv := range fieldValue {
					if i == 0 {
						data.Set(f, fv)
					} else {
						data.Add(f, fv)
					}
				}
			} else {
				for _, fv := range fieldValue {
					data.Add(f, fv)
				}
			}
		}
	}
}
