package item

import (
	"net/url"
	"reflect"
	"testing"
)

func TestFormatMultiValueFields(t *testing.T) {
	tests := []struct {
		name string
		in   url.Values
		want url.Values
	}{
		{
			name: "indexed keys collapse into an ordered slice",
			in:   url.Values{"tags.0": {"a"}, "tags.1": {"b"}, "tags.2": {"c"}},
			want: url.Values{"tags": {"a", "b", "c"}},
		},
		{
			name: "order follows the numeric index, not map iteration",
			in:   url.Values{"tags.2": {"c"}, "tags.0": {"a"}, "tags.1": {"b"}},
			want: url.Values{"tags": {"a", "b", "c"}},
		},
		{
			name: "non-indexed keys are left untouched",
			in:   url.Values{"name": {"x"}, "title": {"y"}},
			want: url.Values{"name": {"x"}, "title": {"y"}},
		},
		{
			name: "indexed values append after an existing scalar of the same name",
			in:   url.Values{"tags": {"pre"}, "tags.0": {"a"}, "tags.1": {"b"}},
			want: url.Values{"tags": {"pre", "a", "b"}},
		},
		{
			name: "multiple distinct fields collapse independently",
			in:   url.Values{"a.0": {"1"}, "a.1": {"2"}, "b.0": {"x"}, "name": {"n"}},
			want: url.Values{"a": {"1", "2"}, "b": {"x"}, "name": {"n"}},
		},
		{
			name: "multiple values at a single index are all kept",
			in:   url.Values{"tags.0": {"a", "b"}},
			want: url.Values{"tags": {"a", "b"}},
		},
		{
			name: "empty form stays empty",
			in:   url.Values{},
			want: url.Values{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			FormatMultiValueFields(tt.in)
			if !reflect.DeepEqual(tt.in, tt.want) {
				t.Errorf("FormatMultiValueFields() = %v, want %v", tt.in, tt.want)
			}
		})
	}
}

// TestFormatMultiValueFieldsIdempotent guards the property the db layer relies
// on: once the handlers have collapsed multi-value fields, calling the formatter
// again (e.g. inside postToJSON/mergeData) is a no-op.
func TestFormatMultiValueFieldsIdempotent(t *testing.T) {
	data := url.Values{"tags.0": {"a"}, "tags.1": {"b"}, "name": {"n"}}

	FormatMultiValueFields(data)
	once := url.Values{}
	for k, v := range data {
		cp := make([]string, len(v))
		copy(cp, v)
		once[k] = cp
	}

	FormatMultiValueFields(data)
	if !reflect.DeepEqual(data, once) {
		t.Errorf("second call changed the data: got %v, want %v (unchanged)", data, once)
	}
}
