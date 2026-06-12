package merge

import (
	"reflect"
	"testing"
)

func TestScored(t *testing.T) {
	// Two content types, each ranked within its own index. Merged by descending
	// score the global order is: Post:1(0.9), Review:1(0.7), Post:2(0.5), Review:2(0.3).
	post := []Hit{{ID: "Post:1", Score: 0.9}, {ID: "Post:2", Score: 0.5}}
	review := []Hit{{ID: "Review:1", Score: 0.7}, {ID: "Review:2", Score: 0.3}}
	mixed := [][]Hit{post, review}

	tests := []struct {
		name   string
		groups [][]Hit
		count  int
		offset int
		want   []string
	}{
		{
			name:   "mixed types ordered by relevance",
			groups: mixed,
			count:  -1,
			offset: 0,
			want:   []string{"Post:1", "Review:1", "Post:2", "Review:2"},
		},
		{
			name:   "count limits the merged page",
			groups: mixed,
			count:  2,
			offset: 0,
			want:   []string{"Post:1", "Review:1"},
		},
		{
			name:   "offset pages through the merged set",
			groups: mixed,
			count:  2,
			offset: 1,
			want:   []string{"Review:1", "Post:2"},
		},
		{
			name:   "offset with default count walks past the boundary",
			groups: mixed,
			count:  10,
			offset: 2,
			want:   []string{"Post:2", "Review:2"},
		},
		{
			name:   "negative count returns everything from offset",
			groups: mixed,
			count:  -1,
			offset: 2,
			want:   []string{"Post:2", "Review:2"},
		},
		{
			name:   "zero count yields an empty page",
			groups: mixed,
			count:  0,
			offset: 0,
			want:   []string{},
		},
		{
			name:   "offset past the end yields an empty page",
			groups: mixed,
			count:  5,
			offset: 10,
			want:   []string{},
		},
		{
			name: "ties are broken by id for stable pagination",
			groups: [][]Hit{
				{{ID: "Type:z", Score: 1.0}},
				{{ID: "Type:a", Score: 1.0}, {ID: "Type:m", Score: 1.0}},
			},
			count:  -1,
			offset: 0,
			want:   []string{"Type:a", "Type:m", "Type:z"},
		},
		{
			name:   "single group matches plain sort-and-slice",
			groups: [][]Hit{{{ID: "x", Score: 0.1}, {ID: "y", Score: 0.9}, {ID: "z", Score: 0.5}}},
			count:  2,
			offset: 0,
			want:   []string{"y", "z"},
		},
		{
			name:   "no groups yields a non-nil empty slice",
			groups: nil,
			count:  10,
			offset: 0,
			want:   []string{},
		},
		{
			name:   "empty groups yield a non-nil empty slice",
			groups: [][]Hit{{}, {}},
			count:  10,
			offset: 0,
			want:   []string{},
		},
		{
			name:   "negative offset is treated as zero",
			groups: mixed,
			count:  1,
			offset: -5,
			want:   []string{"Post:1"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Scored(tc.groups, tc.count, tc.offset)
			if got == nil {
				t.Fatalf("Scored returned nil, want non-nil slice")
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Scored(%v, count=%d, offset=%d) = %v, want %v",
					tc.groups, tc.count, tc.offset, got, tc.want)
			}
		})
	}
}

// TestScoredDoesNotMutateInput guards against the windowing logic accidentally
// reordering a caller's slices in place.
func TestScoredDoesNotMutateInput(t *testing.T) {
	group := []Hit{{ID: "a", Score: 0.1}, {ID: "b", Score: 0.9}}
	before := append([]Hit(nil), group...)

	Scored([][]Hit{group}, -1, 0)

	if !reflect.DeepEqual(group, before) {
		t.Errorf("Scored mutated its input: got %v, want %v", group, before)
	}
}
