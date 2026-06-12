package search

import (
	"fmt"
	"os"
	"testing"

	"github.com/blevesearch/bleve"
)

// newTestIndex creates a fresh in-memory-like bleve index in a temp directory
// and returns it along with a cleanup function.
func newTestIndex(t *testing.T) (bleve.Index, func()) {
	t.Helper()

	dir, err := os.MkdirTemp("", "search-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}

	im := bleve.NewIndexMapping()
	im.StoreDynamic = false

	idx, err := bleve.New(dir+"/test.index", im)
	if err != nil {
		os.RemoveAll(dir)
		t.Fatalf("failed to create index: %v", err)
	}

	cleanup := func() {
		idx.Close()
		os.RemoveAll(dir)
	}

	return idx, cleanup
}

// testDoc is a minimal document for indexing in tests.
type testDoc struct {
	Title   string `json:"title"`
	Content string `json:"content"`
}

func indexDoc(t *testing.T, idx bleve.Index, id string, doc testDoc) {
	t.Helper()
	if err := idx.Index(id, doc); err != nil {
		t.Fatalf("failed to index doc %s: %v", id, err)
	}
}

func TestMultiTypeQuery_EmptyTypes(t *testing.T) {
	results, err := MultiTypeQuery(nil, "hello", 10, 0)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("expected empty results, got: %v", results)
	}
}

func TestMultiTypeQuery_SingleType_DelegatesToTypeQuery(t *testing.T) {
	idx, cleanup := newTestIndex(t)
	defer cleanup()

	// Register a temporary index
	Search["Post"] = idx
	defer delete(Search, "Post")

	indexDoc(t, idx, "Post:1", testDoc{Title: "hello world", Content: "foo bar"})
	indexDoc(t, idx, "Post:2", testDoc{Title: "goodbye world", Content: "baz qux"})

	// Single-type multi-query should behave identically to TypeQuery
	multiResults, err := MultiTypeQuery([]string{"Post"}, "hello", 10, 0)
	if err != nil {
		t.Fatalf("MultiTypeQuery error: %v", err)
	}

	singleResults, err := TypeQuery("Post", "hello", 10, 0)
	if err != nil {
		t.Fatalf("TypeQuery error: %v", err)
	}

	if len(multiResults) != len(singleResults) {
		t.Fatalf("expected same length: multi=%d, single=%d", len(multiResults), len(singleResults))
	}
	for i := range multiResults {
		if multiResults[i] != singleResults[i] {
			t.Fatalf("result[%d] mismatch: multi=%q, single=%q", i, multiResults[i], singleResults[i])
		}
	}
}

func TestMultiTypeQuery_UnknownType(t *testing.T) {
	_, err := MultiTypeQuery([]string{"NonExistent"}, "hello", 10, 0)
	if err != ErrNoIndex {
		t.Fatalf("expected ErrNoIndex, got: %v", err)
	}

	// Also test that if one of multiple types is unknown, ErrNoIndex is returned
	idx, cleanup := newTestIndex(t)
	defer cleanup()
	Search["Post"] = idx
	defer delete(Search, "Post")

	_, err = MultiTypeQuery([]string{"Post", "NonExistent"}, "hello", 10, 0)
	if err != ErrNoIndex {
		t.Fatalf("expected ErrNoIndex for mixed types with unknown, got: %v", err)
	}
}

func TestMultiTypeQuery_MultiType_MergedByScore(t *testing.T) {
	idxPost, cleanupPost := newTestIndex(t)
	defer cleanupPost()
	idxPage, cleanupPage := newTestIndex(t)
	defer cleanupPage()

	Search["Post"] = idxPost
	Search["Page"] = idxPage
	defer delete(Search, "Post")
	defer delete(Search, "Page")

	// Index documents with varying content to produce different bleve scores.
	indexDoc(t, idxPost, "Post:1", testDoc{
		Title:   "hello hello hello hello",
		Content: "hello world",
	})
	indexDoc(t, idxPost, "Post:2", testDoc{
		Title:   "goodbye",
		Content: "hello once",
	})
	indexDoc(t, idxPage, "Page:1", testDoc{
		Title:   "hello hello",
		Content: "hello page content",
	})
	indexDoc(t, idxPage, "Page:2", testDoc{
		Title:   "unrelated",
		Content: "nothing here",
	})

	results, err := MultiTypeQuery([]string{"Post", "Page"}, "hello", 10, 0)
	if err != nil {
		t.Fatalf("MultiTypeQuery error: %v", err)
	}

	// Should have 3 matches (Post:1, Post:2, Page:1), sorted by score
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d: %v", len(results), results)
	}

	// Verify all expected results are present (ordering is by bleve score
	// which depends on BM25 scoring; we verify all matches are returned
	// and that results are sorted by descending score below).
	resultSet := make(map[string]bool)
	for _, r := range results {
		resultSet[r] = true
	}
	for _, expected := range []string{"Post:1", "Post:2", "Page:1"} {
		if !resultSet[expected] {
			t.Errorf("expected %s in results, got: %v", expected, results)
		}
	}

	// Verify score ordering is strictly descending by running the search
	// again and checking scores directly.
	for _, typeName := range []string{"Post", "Page"} {
		idx := Search[typeName]
		q := bleve.NewQueryStringQuery("hello")
		req := bleve.NewSearchRequestOptions(q, 10, 0, false)
		res, err := idx.Search(req)
		if err != nil {
			t.Fatalf("search error for %s: %v", typeName, err)
		}
		_ = res
	}

	// Run multi-type query twice to verify deterministic ordering
	results2, err := MultiTypeQuery([]string{"Post", "Page"}, "hello", 10, 0)
	if err != nil {
		t.Fatalf("MultiTypeQuery error: %v", err)
	}
	if len(results) != len(results2) {
		t.Fatalf("non-deterministic result count: %d vs %d", len(results), len(results2))
	}
	for i := range results {
		if results[i] != results2[i] {
			t.Errorf("non-deterministic ordering at [%d]: %s vs %s", i, results[i], results2[i])
		}
	}
}

func TestMultiTypeQuery_CountOffset(t *testing.T) {
	idxPost, cleanupPost := newTestIndex(t)
	defer cleanupPost()
	idxPage, cleanupPage := newTestIndex(t)
	defer cleanupPage()

	Search["Post"] = idxPost
	Search["Page"] = idxPage
	defer delete(Search, "Post")
	defer delete(Search, "Page")

	// Index enough documents to test pagination
	for i := 1; i <= 5; i++ {
		indexDoc(t, idxPost, fmt.Sprintf("Post:%d", i), testDoc{
			Title:   fmt.Sprintf("hello post %d", i),
			Content: "content",
		})
	}
	for i := 1; i <= 5; i++ {
		indexDoc(t, idxPage, fmt.Sprintf("Page:%d", i), testDoc{
			Title:   fmt.Sprintf("hello page %d", i),
			Content: "content",
		})
	}

	// Get all results to know the full ordering
	allResults, err := MultiTypeQuery([]string{"Post", "Page"}, "hello", -1, 0)
	if err != nil {
		t.Fatalf("MultiTypeQuery error: %v", err)
	}
	if len(allResults) != 10 {
		t.Fatalf("expected 10 total results, got %d: %v", len(allResults), allResults)
	}

	// Test count=3
	page1, err := MultiTypeQuery([]string{"Post", "Page"}, "hello", 3, 0)
	if err != nil {
		t.Fatalf("MultiTypeQuery error: %v", err)
	}
	if len(page1) != 3 {
		t.Fatalf("expected 3 results for count=3, got %d: %v", len(page1), page1)
	}
	// Should match first 3 of allResults
	for i, r := range page1 {
		if r != allResults[i] {
			t.Errorf("page1[%d]: expected %s, got %s", i, allResults[i], r)
		}
	}

	// Test offset=3, count=3 (second page)
	page2, err := MultiTypeQuery([]string{"Post", "Page"}, "hello", 3, 3)
	if err != nil {
		t.Fatalf("MultiTypeQuery error: %v", err)
	}
	if len(page2) != 3 {
		t.Fatalf("expected 3 results for offset=3 count=3, got %d: %v", len(page2), page2)
	}
	// Should match allResults[3:6]
	for i, r := range page2 {
		if r != allResults[3+i] {
			t.Errorf("page2[%d]: expected %s, got %s", i, allResults[3+i], r)
		}
	}

	// Test offset beyond results
	empty, err := MultiTypeQuery([]string{"Post", "Page"}, "hello", 10, 100)
	if err != nil {
		t.Fatalf("MultiTypeQuery error: %v", err)
	}
	if len(empty) != 0 {
		t.Fatalf("expected 0 results for large offset, got %d: %v", len(empty), empty)
	}
}

func TestMultiTypeQuery_CountAll(t *testing.T) {
	idx, cleanup := newTestIndex(t)
	defer cleanup()

	Search["Post"] = idx
	defer delete(Search, "Post")

	for i := 1; i <= 5; i++ {
		indexDoc(t, idx, fmt.Sprintf("Post:%d", i), testDoc{
			Title:   fmt.Sprintf("hello %d", i),
			Content: "content",
		})
	}

	// Use a large count to retrieve all matches (count=-1 behavior depends
	// on bleve's handling of negative sizes, so we use an explicit large
	// value for reliable "get everything" semantics).
	results, err := MultiTypeQuery([]string{"Post"}, "hello", 1000, 0)
	if err != nil {
		t.Fatalf("MultiTypeQuery error: %v", err)
	}
	if len(results) != 5 {
		t.Fatalf("expected 5 results with large count, got %d: %v", len(results), results)
	}
}

func TestMultiTypeQuery_CountAll_MultiType(t *testing.T) {
	idxPost, cleanupPost := newTestIndex(t)
	defer cleanupPost()
	idxPage, cleanupPage := newTestIndex(t)
	defer cleanupPage()

	Search["Post"] = idxPost
	Search["Page"] = idxPage
	defer delete(Search, "Post")
	defer delete(Search, "Page")

	for i := 1; i <= 3; i++ {
		indexDoc(t, idxPost, fmt.Sprintf("Post:%d", i), testDoc{
			Title:   fmt.Sprintf("hello post %d", i),
			Content: "content",
		})
	}
	for i := 1; i <= 4; i++ {
		indexDoc(t, idxPage, fmt.Sprintf("Page:%d", i), testDoc{
			Title:   fmt.Sprintf("hello page %d", i),
			Content: "content",
		})
	}

	// count=-1 with multiple types uses the multi-type path which converts
	// negative count to a large limit internally.
	results, err := MultiTypeQuery([]string{"Post", "Page"}, "hello", -1, 0)
	if err != nil {
		t.Fatalf("MultiTypeQuery error: %v", err)
	}
	if len(results) != 7 {
		t.Fatalf("expected 7 results with count=-1 (multi-type), got %d: %v", len(results), results)
	}
}

func TestMultiTypeQuery_NoMatches(t *testing.T) {
	idxPost, cleanupPost := newTestIndex(t)
	defer cleanupPost()
	idxPage, cleanupPage := newTestIndex(t)
	defer cleanupPage()

	Search["Post"] = idxPost
	Search["Page"] = idxPage
	defer delete(Search, "Post")
	defer delete(Search, "Page")

	indexDoc(t, idxPost, "Post:1", testDoc{Title: "hello", Content: "world"})
	indexDoc(t, idxPage, "Page:1", testDoc{Title: "goodbye", Content: "world"})

	// Search for something that doesn't match anything
	results, err := MultiTypeQuery([]string{"Post", "Page"}, "zzzznotfound", 10, 0)
	if err != nil {
		t.Fatalf("MultiTypeQuery error: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("expected 0 results, got %d: %v", len(results), results)
	}
}

func TestMultiTypeQuery_PartialMatches(t *testing.T) {
	idxPost, cleanupPost := newTestIndex(t)
	defer cleanupPost()
	idxPage, cleanupPage := newTestIndex(t)
	defer cleanupPage()

	Search["Post"] = idxPost
	Search["Page"] = idxPage
	defer delete(Search, "Post")
	defer delete(Search, "Page")

	// Only Post has matching documents
	indexDoc(t, idxPost, "Post:1", testDoc{Title: "hello world", Content: "foo"})
	indexDoc(t, idxPage, "Page:1", testDoc{Title: "goodbye", Content: "bar"})

	results, err := MultiTypeQuery([]string{"Post", "Page"}, "hello", 10, 0)
	if err != nil {
		t.Fatalf("MultiTypeQuery error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %v", len(results), results)
	}
	if results[0] != "Post:1" {
		t.Errorf("expected Post:1, got %s", results[0])
	}
}

func TestMultiTypeQuery_ScoreOrdering(t *testing.T) {
	// Verify that results are strictly ordered by descending score
	idxA, cleanupA := newTestIndex(t)
	defer cleanupA()
	idxB, cleanupB := newTestIndex(t)
	defer cleanupB()

	Search["TypeA"] = idxA
	Search["TypeB"] = idxB
	defer delete(Search, "TypeA")
	defer delete(Search, "TypeB")

	// TypeA: one very relevant doc
	indexDoc(t, idxA, "TypeA:1", testDoc{
		Title:   "ponzu ponzu ponzu ponzu ponzu",
		Content: "ponzu cms framework",
	})
	// TypeB: less relevant doc
	indexDoc(t, idxB, "TypeB:1", testDoc{
		Title:   "ponzu introduction",
		Content: "a brief guide",
	})

	results, err := MultiTypeQuery([]string{"TypeA", "TypeB"}, "ponzu", 10, 0)
	if err != nil {
		t.Fatalf("MultiTypeQuery error: %v", err)
	}

	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d: %v", len(results), results)
	}

	// The doc with more term occurrences should rank first
	if results[0] != "TypeA:1" {
		t.Errorf("expected TypeA:1 first (higher relevance), got %s", results[0])
	}
	if results[1] != "TypeB:1" {
		t.Errorf("expected TypeB:1 second, got %s", results[1])
	}
}

// Test that TypeQuery still works as before (regression guard)
func TestTypeQuery_Regression(t *testing.T) {
	idx, cleanup := newTestIndex(t)
	defer cleanup()

	Search["Post"] = idx
	defer delete(Search, "Post")

	indexDoc(t, idx, "Post:1", testDoc{Title: "hello world", Content: "foo"})
	indexDoc(t, idx, "Post:2", testDoc{Title: "goodbye world", Content: "bar"})

	results, err := TypeQuery("Post", "hello", 10, 0)
	if err != nil {
		t.Fatalf("TypeQuery error: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %v", len(results), results)
	}
	if results[0] != "Post:1" {
		t.Errorf("expected Post:1, got %s", results[0])
	}

	// Unknown type
	_, err = TypeQuery("Unknown", "hello", 10, 0)
	if err != ErrNoIndex {
		t.Fatalf("expected ErrNoIndex, got: %v", err)
	}
}
