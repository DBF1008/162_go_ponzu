package db

import (
	"encoding/json"
	"net/url"
	"reflect"
	"testing"

	"github.com/ponzu-cms/ponzu/system/item"
)

// contentTestTaggable is a minimal content type used to exercise the shared
// save helpers directly, without standing up a bolt store.
type contentTestTaggable struct {
	item.Item
	Name string   `json:"name"`
	Tags []string `json:"tags"`
}

func init() {
	item.Types["contentTestTaggable"] = func() interface{} { return new(contentTestTaggable) }
}

// TestMergeDataCollapsesMultiValueFields covers the drift fix: the merge path
// (UpdateContent) must collapse fieldX.N indexed keys into a slice just like the
// insert/replace path (postToJSON) does.
func TestMergeDataCollapsesMultiValueFields(t *testing.T) {
	existing, err := json.Marshal(contentTestTaggable{
		Item: item.Item{ID: 1, Slug: "old-slug", Timestamp: 111, Updated: 111},
		Name: "old",
		Tags: []string{"x"},
	})
	if err != nil {
		t.Fatal(err)
	}

	data := url.Values{
		"name":   {"new"},
		"tags.0": {"a"},
		"tags.1": {"b"},
	}

	j, err := mergeData("contentTestTaggable", data, existing)
	if err != nil {
		t.Fatalf("mergeData returned error: %v", err)
	}

	var got contentTestTaggable
	if err := json.Unmarshal(j, &got); err != nil {
		t.Fatal(err)
	}

	if want := []string{"a", "b"}; !reflect.DeepEqual(got.Tags, want) {
		t.Errorf("Tags = %v, want %v (merge path must collapse fieldX.N into a slice)", got.Tags, want)
	}
	if got.Name != "new" {
		t.Errorf("Name = %q, want %q", got.Name, "new")
	}
}

// TestMergeDataPreservesCreationTimestamp covers the timestamp fix the API
// update handler relies on: when the form omits "timestamp" (and only refreshes
// "updated"), the merge must keep the content's original creation time.
func TestMergeDataPreservesCreationTimestamp(t *testing.T) {
	existing, err := json.Marshal(contentTestTaggable{
		Item: item.Item{ID: 1, Slug: "s", Timestamp: 111, Updated: 111},
		Name: "old",
	})
	if err != nil {
		t.Fatal(err)
	}

	data := url.Values{
		"name":    {"new"},
		"updated": {"222"},
	}

	j, err := mergeData("contentTestTaggable", data, existing)
	if err != nil {
		t.Fatalf("mergeData returned error: %v", err)
	}

	var got contentTestTaggable
	if err := json.Unmarshal(j, &got); err != nil {
		t.Fatal(err)
	}

	if got.Timestamp != 111 {
		t.Errorf("Timestamp = %d, want 111 (creation time must be preserved on merge)", got.Timestamp)
	}
	if got.Updated != 222 {
		t.Errorf("Updated = %d, want 222 (an update must refresh the updated time)", got.Updated)
	}
}

// TestPostToJSONCollapsesMultiValueFields guards that the insert/replace path
// (used by SetContent for the API create and admin edit handlers) still
// collapses multi-value fields after the shared formatter extraction. A slug is
// provided so the slug-dedup branch (which needs the store) is skipped.
func TestPostToJSONCollapsesMultiValueFields(t *testing.T) {
	data := url.Values{
		"name":   {"n"},
		"tags.0": {"a"},
		"tags.1": {"b"},
		"slug":   {"provided-slug"},
	}

	j, err := postToJSON("contentTestTaggable", data)
	if err != nil {
		t.Fatalf("postToJSON returned error: %v", err)
	}

	var got contentTestTaggable
	if err := json.Unmarshal(j, &got); err != nil {
		t.Fatal(err)
	}

	if want := []string{"a", "b"}; !reflect.DeepEqual(got.Tags, want) {
		t.Errorf("Tags = %v, want %v", got.Tags, want)
	}
}
