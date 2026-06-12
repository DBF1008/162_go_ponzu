package admin

import (
	"net/url"
	"reflect"
	"testing"
)

// TestRestructureMultiValueFields pins the behaviour of collapsing the indexed
// "name.N" inputs emitted by the editor repeaters into a single repeated key.
// Each case maps to a previously broken scenario when adding, reordering or
// deleting multi-select values.
func TestRestructureMultiValueFields(t *testing.T) {
	cases := []struct {
		name string
		in   url.Values
		want url.Values
	}{
		{
			name: "add: contiguous values collapse in order",
			in:   url.Values{"colors.0": {"red"}, "colors.1": {"green"}, "colors.2": {"blue"}},
			want: url.Values{"colors": {"red", "green", "blue"}},
		},
		{
			// indices must be ordered numerically, not lexically: "10" must come
			// after "2", proving the order survives a reorder/expansion.
			name: "reorder: numeric (not lexical) index ordering",
			in: url.Values{
				"colors.0": {"a"}, "colors.1": {"b"}, "colors.2": {"c"},
				"colors.9": {"j"}, "colors.10": {"k"}, "colors.11": {"l"},
			},
			want: url.Values{"colors": {"a", "b", "c", "j", "k", "l"}},
		},
		{
			// after deleting the middle option the remaining indices are
			// non-contiguous (1 missing); the gap must close and the tail at
			// index 3 must NOT be dropped.
			name: "delete: non-contiguous indices close the gap",
			in:   url.Values{"colors.0": {"red"}, "colors.2": {"blue"}, "colors.3": {"yellow"}},
			want: url.Values{"colors": {"red", "blue", "yellow"}},
		},
		{
			// the core "values bleed across fields" regression: two distinct
			// multi-value fields sharing indices must stay independent.
			name: "two fields do not cross-contaminate",
			in: url.Values{
				"colors.0": {"red"}, "colors.1": {"green"},
				"sizes.0": {"small"}, "sizes.1": {"large"},
			},
			want: url.Values{
				"colors": {"red", "green"},
				"sizes":  {"small", "large"},
			},
		},
		{
			// three fields, mismatched lengths, to exercise grouping under load.
			name: "three fields with mismatched lengths stay isolated",
			in: url.Values{
				"a.0": {"a0"}, "a.1": {"a1"}, "a.2": {"a2"},
				"b.0": {"b0"},
				"c.0": {"c0"}, "c.1": {"c1"},
			},
			want: url.Values{
				"a": {"a0", "a1", "a2"},
				"b": {"b0"},
				"c": {"c0", "c1"},
			},
		},
		{
			// an empty string is a legitimate stored value (the "None" select
			// option). It must be preserved at position 0 without shifting the
			// rest of the slice.
			name: "empty value at index 0 is preserved",
			in:   url.Values{"colors.0": {""}, "colors.1": {"blue"}, "colors.2": {"green"}},
			want: url.Values{"colors": {"", "blue", "green"}},
		},
		{
			name: "empty value in the middle is preserved",
			in:   url.Values{"colors.0": {"red"}, "colors.1": {""}, "colors.2": {"blue"}},
			want: url.Values{"colors": {"red", "", "blue"}},
		},
		{
			name: "single value collapses to a one-element slice",
			in:   url.Values{"colors.0": {"red"}},
			want: url.Values{"colors": {"red"}},
		},
		{
			name: "multiple values under one index are flattened in order",
			in:   url.Values{"colors.0": {"a", "b"}, "colors.1": {"c"}},
			want: url.Values{"colors": {"a", "b", "c"}},
		},
		{
			// scalar (non-indexed) fields must be left exactly as-is.
			name: "scalar fields are untouched",
			in:   url.Values{"title": {"hello"}, "id": {"1"}},
			want: url.Values{"title": {"hello"}, "id": {"1"}},
		},
		{
			// a literal dotted key that is not "name.<int>" must not be treated
			// as a multi-value field.
			name: "non-numeric dotted key is left untouched",
			in:   url.Values{"meta.title": {"hello"}},
			want: url.Values{"meta.title": {"hello"}},
		},
		{
			// a pre-existing plain value is kept and the indexed values are
			// appended after it.
			name: "plain value coexists with indexed values",
			in:   url.Values{"colors": {"pre"}, "colors.0": {"red"}, "colors.1": {"blue"}},
			want: url.Values{"colors": {"pre", "red", "blue"}},
		},
		{
			// scalar and multi-value fields mixed together in one form.
			name: "mixed scalar and multi-value form",
			in: url.Values{
				"title":    {"My Post"},
				"colors.0": {"red"}, "colors.1": {"green"},
				"slug": {"my-post"},
			},
			want: url.Values{
				"title":  {"My Post"},
				"colors": {"red", "green"},
				"slug":   {"my-post"},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			restructureMultiValueFields(tc.in)
			if !reflect.DeepEqual(tc.in, tc.want) {
				t.Errorf("restructureMultiValueFields()\n got: %#v\nwant: %#v", tc.in, tc.want)
			}
		})
	}
}

// TestRestructureMultiValueFields_NoIndexedKeysLeftBehind asserts that every
// "name.N" key is consumed, so the downstream schema decoder never sees a
// stray indexed key.
func TestRestructureMultiValueFields_NoIndexedKeysLeftBehind(t *testing.T) {
	form := url.Values{
		"colors.0": {"red"}, "colors.1": {"green"},
		"sizes.0": {"small"},
		"title":   {"keep me"},
	}

	restructureMultiValueFields(form)

	for k := range form {
		if k == "colors.0" || k == "colors.1" || k == "sizes.0" {
			t.Errorf("indexed key %q was not consumed", k)
		}
	}
	if _, ok := form["colors"]; !ok {
		t.Error("expected collapsed key \"colors\" to be present")
	}
	if _, ok := form["sizes"]; !ok {
		t.Error("expected collapsed key \"sizes\" to be present")
	}
	if got := form.Get("title"); got != "keep me" {
		t.Errorf("scalar field changed: got %q", got)
	}
}

// TestRestructureMultiValueFields_DeleteThenReadd simulates the editor flow of
// removing an option and then submitting again: the final slice must reflect
// exactly the values that remain, in their displayed order.
func TestRestructureMultiValueFields_DeleteThenReadd(t *testing.T) {
	// user originally had [red, green, blue]; deletes "green"; the repeater
	// re-indexes the survivors to 0,1 before submit.
	form := url.Values{"tags.0": {"red"}, "tags.1": {"blue"}}
	restructureMultiValueFields(form)

	want := []string{"red", "blue"}
	if !reflect.DeepEqual([]string(form["tags"]), want) {
		t.Fatalf("after delete: got %#v, want %#v", form["tags"], want)
	}

	// user then adds "yellow" at the end.
	form = url.Values{"tags.0": {"red"}, "tags.1": {"blue"}, "tags.2": {"yellow"}}
	restructureMultiValueFields(form)

	want = []string{"red", "blue", "yellow"}
	if !reflect.DeepEqual([]string(form["tags"]), want) {
		t.Fatalf("after re-add: got %#v, want %#v", form["tags"], want)
	}
}
