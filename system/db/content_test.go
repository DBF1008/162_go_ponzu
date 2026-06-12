package db

import (
	"net/url"
	"reflect"
	"testing"
)

func TestNormalizeMultiValueFields_Basic(t *testing.T) {
	data := url.Values{
		"tags.0": {"alpha"},
		"tags.1": {"beta"},
		"tags.2": {"gamma"},
	}

	NormalizeMultiValueFields(data)

	got := data["tags"]
	want := []string{"alpha", "beta", "gamma"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}

	// dotted keys should be removed
	for k := range data {
		if k == "tags.0" || k == "tags.1" || k == "tags.2" {
			t.Errorf("dotted key %q should have been removed", k)
		}
	}
}

func TestNormalizeMultiValueFields_MultipleFields(t *testing.T) {
	data := url.Values{
		"tags.0":       {"go"},
		"tags.1":       {"rust"},
		"categories.0": {"tech"},
		"categories.1": {"science"},
	}

	NormalizeMultiValueFields(data)

	gotTags := data["tags"]
	wantTags := []string{"go", "rust"}
	if !reflect.DeepEqual(gotTags, wantTags) {
		t.Errorf("tags: got %v, want %v", gotTags, wantTags)
	}

	gotCats := data["categories"]
	wantCats := []string{"tech", "science"}
	if !reflect.DeepEqual(gotCats, wantCats) {
		t.Errorf("categories: got %v, want %v", gotCats, wantCats)
	}
}

func TestNormalizeMultiValueFields_NoCrossContamination(t *testing.T) {
	// This is the exact scenario that triggered the bug in editUploadHandler:
	// all fields shared one inner map, so later fields overwrote earlier ones.
	data := url.Values{
		"fieldA.0": {"a1"},
		"fieldA.1": {"a2"},
		"fieldB.0": {"b1"},
		"fieldB.1": {"b2"},
		"fieldC.0": {"c1"},
	}

	NormalizeMultiValueFields(data)

	if got := data["fieldA"]; !reflect.DeepEqual(got, []string{"a1", "a2"}) {
		t.Errorf("fieldA: got %v, want [a1 a2]", got)
	}
	if got := data["fieldB"]; !reflect.DeepEqual(got, []string{"b1", "b2"}) {
		t.Errorf("fieldB: got %v, want [b1 b2]", got)
	}
	if got := data["fieldC"]; !reflect.DeepEqual(got, []string{"c1"}) {
		t.Errorf("fieldC: got %v, want [c1]", got)
	}
}

func TestNormalizeMultiValueFields_NonContiguousIndices(t *testing.T) {
	// Simulates deleting the middle item: indices 0 and 2 remain, 1 is gone.
	data := url.Values{
		"tags.0": {"first"},
		"tags.2": {"third"},
	}

	NormalizeMultiValueFields(data)

	got := data["tags"]
	want := []string{"first", "third"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v (non-contiguous indices should not lose data)", got, want)
	}
}

func TestNormalizeMultiValueFields_DeleteMiddlePreservesOrder(t *testing.T) {
	// 5 items, delete items at index 1 and 3 → indices 0, 2, 4 remain
	data := url.Values{
		"items.0": {"zero"},
		"items.2": {"two"},
		"items.4": {"four"},
	}

	NormalizeMultiValueFields(data)

	got := data["items"]
	want := []string{"zero", "two", "four"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestNormalizeMultiValueFields_OrderPreserved(t *testing.T) {
	// Even if map iteration delivers keys in random order,
	// the output must be sorted by numeric index.
	data := url.Values{
		"tags.2": {"third"},
		"tags.0": {"first"},
		"tags.1": {"second"},
	}

	NormalizeMultiValueFields(data)

	got := data["tags"]
	want := []string{"first", "second", "third"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v (order must be numeric)", got, want)
	}
}

func TestNormalizeMultiValueFields_SingleValue(t *testing.T) {
	data := url.Values{
		"tags.0": {"only"},
	}

	NormalizeMultiValueFields(data)

	got := data["tags"]
	want := []string{"only"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestNormalizeMultiValueFields_EmptyForm(t *testing.T) {
	data := url.Values{}

	// should not panic
	NormalizeMultiValueFields(data)

	if len(data) != 0 {
		t.Errorf("expected empty form, got %v", data)
	}
}

func TestNormalizeMultiValueFields_NoDottedKeys(t *testing.T) {
	data := url.Values{
		"title": {"Hello World"},
		"body":  {"Some content"},
	}

	NormalizeMultiValueFields(data)

	if data.Get("title") != "Hello World" {
		t.Errorf("title should be unchanged, got %q", data.Get("title"))
	}
	if data.Get("body") != "Some content" {
		t.Errorf("body should be unchanged, got %q", data.Get("body"))
	}
}

func TestNormalizeMultiValueFields_IgnoresNonNumericSuffix(t *testing.T) {
	// Keys like "config.version" where the part after the dot is not a number
	// should NOT be treated as multi-value fields.
	data := url.Values{
		"config.version": {"1.0"},
		"tags.0":         {"go"},
	}

	NormalizeMultiValueFields(data)

	// config.version should be untouched
	if data.Get("config.version") != "1.0" {
		t.Errorf("config.version should be unchanged, got %q", data.Get("config.version"))
	}

	// tags should be normalized
	got := data["tags"]
	want := []string{"go"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("tags: got %v, want %v", got, want)
	}
}

func TestNormalizeMultiValueFields_PreservesExistingNonDottedValues(t *testing.T) {
	// A field that already has a non-dotted value alongside dotted keys
	// for a different field should not be affected.
	data := url.Values{
		"title":  {"My Title"},
		"tags.0": {"go"},
		"tags.1": {"rust"},
	}

	NormalizeMultiValueFields(data)

	if data.Get("title") != "My Title" {
		t.Errorf("title should be unchanged, got %q", data.Get("title"))
	}

	got := data["tags"]
	want := []string{"go", "rust"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("tags: got %v, want %v", got, want)
	}
}

func TestNormalizeMultiValueFields_LargeIndices(t *testing.T) {
	// Indices like 10, 20 should sort numerically, not lexicographically
	data := url.Values{
		"tags.10": {"tenth"},
		"tags.2":  {"second"},
		"tags.20": {"twentieth"},
	}

	NormalizeMultiValueFields(data)

	got := data["tags"]
	want := []string{"second", "tenth", "twentieth"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v (numeric sort, not lexicographic)", got, want)
	}
}
