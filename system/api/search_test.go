package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/ponzu-cms/ponzu/system/item"
	"github.com/ponzu-cms/ponzu/system/search"

	"github.com/blevesearch/bleve"
)

// apiSearchPublic is a minimal public content type: it implements none of the
// optional Hideable/Pushable/Omittable interfaces, so the search handler treats
// it as a normal, public, searchable type.
type apiSearchPublic struct{}

// apiSearchHidden is a content type kept hidden from the public API. isHidden
// treats a nil error from Hide as "hidden", so this type is always skipped.
type apiSearchHidden struct{}

func (s *apiSearchHidden) Hide(http.ResponseWriter, *http.Request) error { return nil }

// registerSearchFixtures wires two public types and one hidden type into the
// global item.Types registry, and gives the public types empty in-memory Bleve
// indexes. Indexes are left empty on purpose: with no hits the handler never
// reaches db.ContentMulti, so these tests exercise the full multi-type request
// path (parse → validate → merge → respond) without needing a Bolt store. The
// relevance ordering and count/offset windowing of populated indexes are covered
// by the dependency-free system/search/merge tests.
func registerSearchFixtures(t *testing.T) {
	t.Helper()

	item.Types["apiSearchA"] = func() interface{} { return new(apiSearchPublic) }
	item.Types["apiSearchB"] = func() interface{} { return new(apiSearchPublic) }
	item.Types["apiSearchHidden"] = func() interface{} { return new(apiSearchHidden) }

	for _, typeName := range []string{"apiSearchA", "apiSearchB"} {
		idx, err := bleve.NewMemOnly(bleve.NewIndexMapping())
		if err != nil {
			t.Fatalf("failed to create in-memory index for %s: %v", typeName, err)
		}
		search.Search[typeName] = idx
	}
}

func doSearch(t *testing.T, rawQuery string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(http.MethodGet, "/api/search?"+rawQuery, nil)
	rec := httptest.NewRecorder()
	searchContentHandler(rec, req)
	return rec
}

func assertEmptyData(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()

	var body struct {
		Data []json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("response is not valid JSON: %v (body=%q)", err, rec.Body.String())
	}
	if len(body.Data) != 0 {
		t.Errorf("expected empty data array, got %d item(s) (body=%q)", len(body.Data), rec.Body.String())
	}
}

func TestSearchContentHandlerStatus(t *testing.T) {
	registerSearchFixtures(t)

	tests := []struct {
		name     string
		query    string
		wantCode int
	}{
		{
			name:     "invalid type in an otherwise valid set is rejected",
			query:    "type=apiSearchA,apiNotRegistered&q=anything",
			wantCode: http.StatusBadRequest,
		},
		{
			name:     "single invalid type is rejected",
			query:    "type=apiNotRegistered&q=anything",
			wantCode: http.StatusBadRequest,
		},
		{
			name:     "missing type is rejected",
			query:    "q=anything",
			wantCode: http.StatusBadRequest,
		},
		{
			name:     "missing query is rejected",
			query:    "type=apiSearchA",
			wantCode: http.StatusBadRequest,
		},
		{
			name:     "every requested type hidden yields not found",
			query:    "type=apiSearchHidden&q=anything",
			wantCode: http.StatusNotFound,
		},
		{
			name:     "mixed valid public types succeed",
			query:    "type=apiSearchA,apiSearchB&q=nomatch&count=5&offset=0",
			wantCode: http.StatusOK,
		},
		{
			name:     "repeated type params succeed",
			query:    "type=apiSearchA&type=apiSearchB&q=nomatch",
			wantCode: http.StatusOK,
		},
		{
			name:     "hidden types are skipped while public ones still resolve",
			query:    "type=apiSearchA,apiSearchHidden&q=nomatch",
			wantCode: http.StatusOK,
		},
		{
			name:     "single public type keeps working (backward compatible)",
			query:    "type=apiSearchA&q=nomatch",
			wantCode: http.StatusOK,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := doSearch(t, tc.query)
			if rec.Code != tc.wantCode {
				t.Fatalf("status = %d, want %d (body=%q)", rec.Code, tc.wantCode, rec.Body.String())
			}
			if tc.wantCode == http.StatusOK {
				assertEmptyData(t, rec)
			}
		})
	}
}

func TestParseTypes(t *testing.T) {
	tests := []struct {
		name   string
		values []string
		want   []string
	}{
		{"single type", []string{"Post"}, []string{"Post"}},
		{"comma separated", []string{"Post,Review"}, []string{"Post", "Review"}},
		{"repeated params", []string{"Post", "Review"}, []string{"Post", "Review"}},
		{"mixed comma and repeated", []string{"Post,Review", "Page"}, []string{"Post", "Review", "Page"}},
		{"trims whitespace", []string{" Post , Review "}, []string{"Post", "Review"}},
		{"dedupes preserving first-seen order", []string{"Post,Review", "Post"}, []string{"Post", "Review"}},
		{"drops empty segments", []string{"", "Post,,Review,"}, []string{"Post", "Review"}},
		{"all empty yields nil", []string{"", " , "}, nil},
		{"nil input yields nil", nil, nil},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := parseTypes(tc.values)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("parseTypes(%q) = %v, want %v", tc.values, got, tc.want)
			}
		})
	}
}

func TestTypeOfTarget(t *testing.T) {
	tests := []struct {
		target string
		want   string
	}{
		{"Post:123", "Post"},
		{"Review:abc-def", "Review"},
		{"NoColon", "NoColon"},
		{"", ""},
	}

	for _, tc := range tests {
		if got := typeOfTarget(tc.target); got != tc.want {
			t.Errorf("typeOfTarget(%q) = %q, want %q", tc.target, got, tc.want)
		}
	}
}
