package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/blevesearch/bleve"
	"github.com/blevesearch/bleve/mapping"
	"github.com/boltdb/bolt"
	"github.com/ponzu-cms/ponzu/system/db"
	"github.com/ponzu-cms/ponzu/system/item"
	"github.com/ponzu-cms/ponzu/system/search"
)

// testItem is a minimal content type for testing.
type testItem struct {
	item.Item
	Title   string `json:"title"`
	Content string `json:"content"`
	Secret  string `json:"secret"`
}

func (t *testItem) IndexContent() bool { return true }

func (t *testItem) SearchMapping() (*mapping.IndexMappingImpl, error) {
	m := bleve.NewIndexMapping()
	m.StoreDynamic = false
	return m, nil
}

// testItemOmittable is a test type that implements Omittable.
type testItemOmittable struct {
	item.Item
	Title   string `json:"title"`
	Content string `json:"content"`
	Secret  string `json:"secret"`
}

func (t *testItemOmittable) IndexContent() bool { return true }

func (t *testItemOmittable) SearchMapping() (*mapping.IndexMappingImpl, error) {
	m := bleve.NewIndexMapping()
	m.StoreDynamic = false
	return m, nil
}

func (t *testItemOmittable) Omit(res http.ResponseWriter, req *http.Request) ([]string, error) {
	return []string{"secret"}, nil
}

// hiddenItem is a test type that implements Hideable and always hides.
type hiddenItem struct {
	testItem
}

func (h *hiddenItem) Hide(res http.ResponseWriter, req *http.Request) error {
	return fmt.Errorf("hidden")
}

// setupTestTypes registers test types in item.Types and returns a cleanup func.
func setupTestTypes() func() {
	origTypes := make(map[string]func() interface{})
	for k, v := range item.Types {
		origTypes[k] = v
	}

	item.Types["Post"] = func() interface{} { return &testItem{} }
	item.Types["Page"] = func() interface{} { return &testItem{} }
	item.Types["OmittablePost"] = func() interface{} { return &testItemOmittable{} }
	item.Types["HiddenPost"] = func() interface{} { return &hiddenItem{} }

	return func() {
		for k := range item.Types {
			delete(item.Types, k)
		}
		for k, v := range origTypes {
			item.Types[k] = v
		}
	}
}

// Shared DB setup: since db.Init() guards against re-initialization and
// db.Close() doesn't reset the internal store pointer, we use a single
// shared DB instance across all tests in this package.
var (
	testDBOnce    sync.Once
	testDBTmpDir  string
	testDBCleanup func()
)

func ensureTestDB(t *testing.T) {
	t.Helper()
	testDBOnce.Do(func() {
		var err error
		testDBTmpDir, err = os.MkdirTemp("", "api-search-test-*")
		if err != nil {
			t.Fatalf("failed to create temp dir: %v", err)
		}

		// Register test types BEFORE db.Init() so that the buckets
		// are created for them during initialization.
		item.Types["Post"] = func() interface{} { return &testItem{} }
		item.Types["Page"] = func() interface{} { return &testItem{} }
		item.Types["OmittablePost"] = func() interface{} { return &testItemOmittable{} }
		item.Types["HiddenPost"] = func() interface{} { return &hiddenItem{} }

		os.Setenv("PONZU_DATA_DIR", testDBTmpDir)
		os.Setenv("PONZU_SEARCH_DIR", testDBTmpDir+"/search")

		db.Init()

		testDBCleanup = func() {
			db.Close()
			os.Unsetenv("PONZU_DATA_DIR")
			os.Unsetenv("PONZU_SEARCH_DIR")
			os.RemoveAll(testDBTmpDir)
		}
	})
}

// setupTestSearchIndex creates bleve indices for the given types and returns cleanup.
func setupTestSearchIndex(t *testing.T, types ...string) func() {
	t.Helper()

	for _, typeName := range types {
		err := search.MapIndex(typeName)
		if err != nil {
			t.Fatalf("failed to map index for %s: %v", typeName, err)
		}
	}

	// Clear any existing data from previous tests
	clearTestData(t, types...)

	return func() {
		// Clean up data but keep indices (they're reused across tests)
		clearTestData(t, types...)
		for _, typeName := range types {
			if idx, ok := search.Search[typeName]; ok {
				idx.Close()
				delete(search.Search, typeName)
			}
		}
	}
}

// clearTestData removes all documents from the specified types' BoltDB buckets
// and bleve indices to prevent data leakage between tests.
func clearTestData(t *testing.T, types ...string) {
	t.Helper()

	store := db.Store()
	if store != nil {
		for _, typeName := range types {
			// Clear BoltDB bucket
			store.Update(func(tx *bolt.Tx) error {
				b := tx.Bucket([]byte(typeName))
				if b == nil {
					return nil
				}
				// Delete all keys in the bucket
				c := b.Cursor()
				var keys [][]byte
				for k, _ := c.First(); k != nil; k, _ = c.Next() {
					keys = append(keys, k)
				}
				for _, k := range keys {
					b.Delete(k)
				}
				return nil
			})

			// Clear bleve index - delete all documents
			if idx, ok := search.Search[typeName]; ok {
				// Use a match-all query to find and delete all documents
				q := bleve.NewMatchAllQuery()
				req := bleve.NewSearchRequest(q)
				req.Size = 10000
				res, err := idx.Search(req)
				if err == nil {
					for _, hit := range res.Hits {
						idx.Delete(hit.ID)
					}
				}
			}
		}
	}
}

// storeTestContent stores a test document in both the BoltDB and bleve index.
func storeTestContent(t *testing.T, typeName string, id int, title, content, secret string) {
	t.Helper()

	doc := &testItem{
		Title:   title,
		Content: content,
		Secret:  secret,
	}
	doc.ID = id

	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("failed to marshal doc: %v", err)
	}

	// Store in BoltDB
	store := db.Store()
	if store == nil {
		t.Fatal("db store is nil")
	}
	err = store.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(typeName))
		if b == nil {
			return fmt.Errorf("bucket %s not found", typeName)
		}
		return b.Put([]byte(fmt.Sprintf("%d", id)), data)
	})
	if err != nil {
		t.Fatalf("failed to store content: %v", err)
	}

	// Index in bleve
	target := fmt.Sprintf("%s:%d", typeName, id)
	idx, ok := search.Search[typeName]
	if !ok {
		t.Fatalf("no search index for type %s", typeName)
	}
	if err := idx.Index(target, doc); err != nil {
		t.Fatalf("failed to index doc: %v", err)
	}
}

func TestSearchContentHandler_MissingType(t *testing.T) {
	cleanup := setupTestTypes()
	defer cleanup()

	req := httptest.NewRequest("GET", "/api/search?q=hello", nil)
	rec := httptest.NewRecorder()

	searchContentHandler(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestSearchContentHandler_UnknownType(t *testing.T) {
	cleanup := setupTestTypes()
	defer cleanup()

	req := httptest.NewRequest("GET", "/api/search?type=NonExistent&q=hello", nil)
	rec := httptest.NewRecorder()

	searchContentHandler(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rec.Code)
	}
}

func TestSearchContentHandler_MixedValidAndInvalidType(t *testing.T) {
	cleanup := setupTestTypes()
	defer cleanup()

	req := httptest.NewRequest("GET", "/api/search?type=Post&type=NonExistent&q=hello", nil)
	rec := httptest.NewRecorder()

	searchContentHandler(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for mixed valid/invalid types, got %d", rec.Code)
	}
}

func TestSearchContentHandler_MissingQuery(t *testing.T) {
	cleanup := setupTestTypes()
	defer cleanup()

	req := httptest.NewRequest("GET", "/api/search?type=Post", nil)
	rec := httptest.NewRecorder()

	searchContentHandler(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for missing q, got %d", rec.Code)
	}
}

func TestSearchContentHandler_EmptyQuery(t *testing.T) {
	cleanup := setupTestTypes()
	defer cleanup()

	req := httptest.NewRequest("GET", "/api/search?type=Post&q=", nil)
	rec := httptest.NewRecorder()

	searchContentHandler(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for empty q, got %d", rec.Code)
	}
}

func TestSearchContentHandler_InvalidCount(t *testing.T) {
	cleanup := setupTestTypes()
	defer cleanup()

	req := httptest.NewRequest("GET", "/api/search?type=Post&q=hello&count=abc", nil)
	rec := httptest.NewRecorder()

	searchContentHandler(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("expected 500 for invalid count, got %d", rec.Code)
	}
}

func TestSearchContentHandler_InvalidOffset(t *testing.T) {
	cleanup := setupTestTypes()
	defer cleanup()

	req := httptest.NewRequest("GET", "/api/search?type=Post&q=hello&offset=xyz", nil)
	rec := httptest.NewRecorder()

	searchContentHandler(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("expected 500 for invalid offset, got %d", rec.Code)
	}
}

func TestSearchContentHandler_HiddenType(t *testing.T) {
	cleanup := setupTestTypes()
	defer cleanup()

	req := httptest.NewRequest("GET", "/api/search?type=HiddenPost&q=hello", nil)
	rec := httptest.NewRecorder()

	searchContentHandler(rec, req)

	// Hidden types return 500 (from hide() when error is not ErrAllowHiddenItem)
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("expected 500 for hidden type, got %d", rec.Code)
	}
}

func TestSearchContentHandler_NoSearchIndex(t *testing.T) {
	cleanup := setupTestTypes()
	defer cleanup()
	// Don't set up search index - should get 404

	req := httptest.NewRequest("GET", "/api/search?type=Post&q=hello", nil)
	rec := httptest.NewRecorder()

	searchContentHandler(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404 for no search index, got %d", rec.Code)
	}
}

func TestSearchContentHandler_SingleType_HappyPath(t *testing.T) {
	ensureTestDB(t)
	cleanupTypes := setupTestTypes()
	defer cleanupTypes()

	cleanupIdx := setupTestSearchIndex(t, "Post")
	defer cleanupIdx()

	// Store test documents
	storeTestContent(t, "Post", 1, "hello world", "test content", "")
	storeTestContent(t, "Post", 2, "goodbye world", "test content", "")
	storeTestContent(t, "Post", 3, "hello again", "test content", "")

	req := httptest.NewRequest("GET", "/api/search?type=Post&q=hello", nil)
	rec := httptest.NewRecorder()

	searchContentHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}

	var resp map[string][]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v\nbody: %s", err, rec.Body.String())
	}

	data, ok := resp["data"]
	if !ok {
		t.Fatal("expected 'data' key in response")
	}

	if len(data) != 2 {
		t.Errorf("expected 2 results for 'hello', got %d", len(data))
	}
}

func TestSearchContentHandler_MultiType_HappyPath(t *testing.T) {
	ensureTestDB(t)
	cleanupTypes := setupTestTypes()
	defer cleanupTypes()

	cleanupIdx := setupTestSearchIndex(t, "Post", "Page")
	defer cleanupIdx()

	// Store test documents in both types
	storeTestContent(t, "Post", 1, "hello post one", "post content", "")
	storeTestContent(t, "Post", 2, "hello post two", "post content", "")
	storeTestContent(t, "Page", 1, "hello page one", "page content", "")
	storeTestContent(t, "Page", 2, "goodbye page", "page content", "")

	req := httptest.NewRequest("GET", "/api/search?type=Post&type=Page&q=hello", nil)
	rec := httptest.NewRecorder()

	searchContentHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}

	var resp map[string][]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v\nbody: %s", err, rec.Body.String())
	}

	data, ok := resp["data"]
	if !ok {
		t.Fatal("expected 'data' key in response")
	}

	// Should have 3 matches for "hello" (2 posts + 1 page)
	if len(data) != 3 {
		t.Errorf("expected 3 results, got %d", len(data))
	}

	// Verify results contain both types
	hasPost := false
	hasPage := false
	for _, raw := range data {
		s := string(raw)
		if strings.Contains(s, "post content") {
			hasPost = true
		}
		if strings.Contains(s, "page content") {
			hasPage = true
		}
	}
	if !hasPost {
		t.Error("expected Post results in multi-type search")
	}
	if !hasPage {
		t.Error("expected Page results in multi-type search")
	}
}

func TestSearchContentHandler_MultiType_CountOffset(t *testing.T) {
	ensureTestDB(t)
	cleanupTypes := setupTestTypes()
	defer cleanupTypes()

	cleanupIdx := setupTestSearchIndex(t, "Post", "Page")
	defer cleanupIdx()

	// Index 3 posts and 3 pages, all matching "hello"
	for i := 1; i <= 3; i++ {
		storeTestContent(t, "Post", i, fmt.Sprintf("hello post %d", i), "post content", "")
		storeTestContent(t, "Page", i, fmt.Sprintf("hello page %d", i), "page content", "")
	}

	// Test with count=2
	req := httptest.NewRequest("GET", "/api/search?type=Post&type=Page&q=hello&count=2", nil)
	rec := httptest.NewRecorder()
	searchContentHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}

	var resp map[string][]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if len(resp["data"]) != 2 {
		t.Errorf("expected 2 results with count=2, got %d", len(resp["data"]))
	}

	// Test with offset=2, count=2 (second page)
	req2 := httptest.NewRequest("GET", "/api/search?type=Post&type=Page&q=hello&count=2&offset=2", nil)
	rec2 := httptest.NewRecorder()
	searchContentHandler(rec2, req2)

	if rec2.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec2.Code)
	}

	var resp2 map[string][]json.RawMessage
	if err := json.Unmarshal(rec2.Body.Bytes(), &resp2); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if len(resp2["data"]) != 2 {
		t.Errorf("expected 2 results with offset=2 count=2, got %d", len(resp2["data"]))
	}

	// Verify page 1 and page 2 don't overlap
	page1IDs := make(map[string]bool)
	for _, raw := range resp["data"] {
		page1IDs[string(raw)] = true
	}
	for _, raw := range resp2["data"] {
		if page1IDs[string(raw)] {
			t.Error("page 1 and page 2 should not have overlapping results")
		}
	}
}

func TestSearchContentHandler_MultiType_WithOmittable(t *testing.T) {
	ensureTestDB(t)
	cleanupTypes := setupTestTypes()
	defer cleanupTypes()

	cleanupIdx := setupTestSearchIndex(t, "OmittablePost")
	defer cleanupIdx()

	// Store document with a secret field
	doc := &testItemOmittable{
		Title:   "hello omittable",
		Content: "content",
		Secret:  "should-be-omitted",
	}
	doc.ID = 1

	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("failed to marshal: %v", err)
	}

	store := db.Store()
	err = store.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte("OmittablePost"))
		if b == nil {
			return fmt.Errorf("bucket not found")
		}
		return b.Put([]byte("1"), data)
	})
	if err != nil {
		t.Fatalf("failed to store: %v", err)
	}

	idx, ok := search.Search["OmittablePost"]
	if !ok {
		t.Fatal("OmittablePost search index not found")
	}

	// Index the document and verify it's searchable
	if err := idx.Index("OmittablePost:1", doc); err != nil {
		t.Fatalf("failed to index: %v", err)
	}

	// Verify the document is in the index
	q := bleve.NewQueryStringQuery("hello")
	req := bleve.NewSearchRequest(q)
	res, err := idx.Search(req)
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	if len(res.Hits) == 0 {
		t.Fatal("expected at least 1 hit in bleve index before handler test")
	}

	httpReq := httptest.NewRequest("GET", "/api/search?type=OmittablePost&q=hello", nil)
	rec := httptest.NewRecorder()
	searchContentHandler(rec, httpReq)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d\nbody: %s", rec.Code, rec.Body.String())
	}

	body := rec.Body.String()
	if strings.Contains(body, "should-be-omitted") {
		t.Error("expected 'secret' field to be omitted from response")
	}
	if !strings.Contains(body, "hello omittable") {
		t.Error("expected title to be present in response")
	}
}

func TestSearchContentHandler_MultiType_MixedOmittable(t *testing.T) {
	// Test that omit works correctly when some types are Omittable and others aren't
	ensureTestDB(t)
	cleanupTypes := setupTestTypes()
	defer cleanupTypes()

	cleanupIdx := setupTestSearchIndex(t, "Post", "OmittablePost")
	defer cleanupIdx()

	// Store a regular Post (no omit)
	storeTestContent(t, "Post", 1, "hello regular", "regular content", "regular-secret")

	// Store an OmittablePost (should omit "secret")
	doc := &testItemOmittable{
		Title:   "hello omittable",
		Content: "omittable content",
		Secret:  "omittable-secret",
	}
	doc.ID = 1
	data, _ := json.Marshal(doc)

	store := db.Store()
	store.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte("OmittablePost"))
		return b.Put([]byte("1"), data)
	})
	search.Search["OmittablePost"].Index("OmittablePost:1", doc)

	req := httptest.NewRequest("GET", "/api/search?type=Post&type=OmittablePost&q=hello", nil)
	rec := httptest.NewRecorder()
	searchContentHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}

	body := rec.Body.String()
	// Regular Post's secret should be present (not Omittable)
	if !strings.Contains(body, "regular-secret") {
		t.Error("expected regular Post's secret to be present")
	}
	// OmittablePost's secret should be omitted
	if strings.Contains(body, "omittable-secret") {
		t.Error("expected OmittablePost's secret to be omitted")
	}
}

func TestSearchContentHandler_SingleType_BackwardCompat(t *testing.T) {
	// Verify that single-type requests still work exactly as before
	ensureTestDB(t)
	cleanupTypes := setupTestTypes()
	defer cleanupTypes()

	cleanupIdx := setupTestSearchIndex(t, "Post")
	defer cleanupIdx()

	storeTestContent(t, "Post", 1, "hello world", "content", "")
	storeTestContent(t, "Post", 2, "goodbye world", "content", "")

	// Single type, default count/offset
	req := httptest.NewRequest("GET", "/api/search?type=Post&q=hello", nil)
	rec := httptest.NewRecorder()
	searchContentHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}

	var resp map[string][]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if len(resp["data"]) != 1 {
		t.Errorf("expected 1 result, got %d", len(resp["data"]))
	}

	// Verify response has correct Content-Type
	ct := rec.Header().Get("Content-Type")
	if ct != "application/json" {
		t.Errorf("expected Content-Type application/json, got %s", ct)
	}
}

func TestSearchContentHandler_MultiType_NoMatches(t *testing.T) {
	ensureTestDB(t)
	cleanupTypes := setupTestTypes()
	defer cleanupTypes()

	cleanupIdx := setupTestSearchIndex(t, "Post", "Page")
	defer cleanupIdx()

	storeTestContent(t, "Post", 1, "hello world", "content", "")
	storeTestContent(t, "Page", 1, "goodbye page", "content", "")

	// Search for something that doesn't match
	req := httptest.NewRequest("GET", "/api/search?type=Post&type=Page&q=zzzznotfound", nil)
	rec := httptest.NewRecorder()
	searchContentHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}

	var resp map[string][]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if len(resp["data"]) != 0 {
		t.Errorf("expected 0 results, got %d", len(resp["data"]))
	}
}

func TestSearchContentHandler_DefaultCount(t *testing.T) {
	// Verify default count=10 behavior
	ensureTestDB(t)
	cleanupTypes := setupTestTypes()
	defer cleanupTypes()

	cleanupIdx := setupTestSearchIndex(t, "Post")
	defer cleanupIdx()

	// Store 15 documents, all matching "hello"
	for i := 1; i <= 15; i++ {
		storeTestContent(t, "Post", i, fmt.Sprintf("hello post %d", i), "content", "")
	}

	// No count specified - should default to 10
	req := httptest.NewRequest("GET", "/api/search?type=Post&q=hello", nil)
	rec := httptest.NewRecorder()
	searchContentHandler(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}

	var resp map[string][]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if len(resp["data"]) != 10 {
		t.Errorf("expected 10 results (default count), got %d", len(resp["data"]))
	}
}
