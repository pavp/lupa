package verify

import (
	"reflect"
	"testing"
)

func finding(file, side string, start, end int, level, claim string) Finding {
	return Finding{
		Comment:  Comment{File: file, Claim: claim},
		Location: Location{File: file, Side: side, Start: start, End: end},
		Level:    level,
	}
}

func TestDedupe(t *testing.T) {
	const (
		nilDeref   = "The pointer user is dereferenced without a nil check"
		nilDerefB  = "User pointer dereferenced with no nil check here"
		nilDerefC  = "Missing nil check: user pointer dereferenced"
		unrelated  = "Retries loop forever when the server returns 500"
		otherWords = "Timeout ignored by the HTTP client configuration"
	)
	tests := []struct {
		name string
		in   []Finding
		want []Finding
	}{
		{
			name: "empty input",
			in:   nil,
			want: nil,
		},
		{
			name: "same lines and similar claims merge into the higher level and widen the range",
			in: []Finding{
				finding("a.go", "new", 10, 12, LevelNote, nilDeref),
				finding("a.go", "new", 11, 15, LevelBlocking, nilDerefB),
			},
			want: []Finding{
				{
					Comment:  Comment{File: "a.go", Claim: nilDerefB},
					Location: Location{File: "a.go", Side: "new", Start: 10, End: 15},
					Level:    LevelBlocking,
					Merged:   1,
				},
			},
		},
		{
			name: "similar claims in different files stay apart",
			in: []Finding{
				finding("a.go", "new", 10, 12, LevelNote, nilDeref),
				finding("b.go", "new", 10, 12, LevelNote, nilDerefB),
			},
			want: []Finding{
				finding("a.go", "new", 10, 12, LevelNote, nilDeref),
				finding("b.go", "new", 10, 12, LevelNote, nilDerefB),
			},
		},
		{
			name: "similar claims on different sides stay apart",
			in: []Finding{
				finding("a.go", "new", 10, 12, LevelNote, nilDeref),
				finding("a.go", "old", 10, 12, LevelNote, nilDerefB),
			},
			want: []Finding{
				finding("a.go", "new", 10, 12, LevelNote, nilDeref),
				finding("a.go", "old", 10, 12, LevelNote, nilDerefB),
			},
		},
		{
			name: "similar claims on non-overlapping lines stay apart",
			in: []Finding{
				finding("a.go", "new", 10, 12, LevelNote, nilDeref),
				finding("a.go", "new", 13, 20, LevelNote, nilDerefB),
			},
			want: []Finding{
				finding("a.go", "new", 10, 12, LevelNote, nilDeref),
				finding("a.go", "new", 13, 20, LevelNote, nilDerefB),
			},
		},
		{
			name: "overlapping lines with unrelated claims stay apart",
			in: []Finding{
				finding("a.go", "new", 10, 12, LevelNote, nilDeref),
				finding("a.go", "new", 10, 12, LevelNote, unrelated),
				finding("a.go", "new", 11, 11, LevelNote, otherWords),
			},
			want: []Finding{
				finding("a.go", "new", 10, 12, LevelNote, nilDeref),
				finding("a.go", "new", 10, 12, LevelNote, unrelated),
				finding("a.go", "new", 11, 11, LevelNote, otherWords),
			},
		},
		{
			name: "three-way chain merges into one",
			in: []Finding{
				finding("a.go", "new", 1, 3, LevelNote, nilDeref),
				finding("a.go", "new", 3, 6, LevelImportant, nilDerefB),
				finding("a.go", "new", 6, 9, LevelFollowUp, nilDerefC),
			},
			want: []Finding{
				{
					Comment:  Comment{File: "a.go", Claim: nilDerefB},
					Location: Location{File: "a.go", Side: "new", Start: 1, End: 9},
					Level:    LevelImportant,
					Merged:   2,
				},
			},
		},
		{
			name: "level tie keeps the earlier one",
			in: []Finding{
				finding("a.go", "new", 10, 12, LevelImportant, nilDeref),
				finding("a.go", "new", 8, 10, LevelImportant, nilDerefB),
			},
			want: []Finding{
				{
					Comment:  Comment{File: "a.go", Claim: nilDeref},
					Location: Location{File: "a.go", Side: "new", Start: 8, End: 12},
					Level:    LevelImportant,
					Merged:   1,
				},
			},
		},
		{
			name: "survivors keep their original order around a merge",
			in: []Finding{
				finding("b.go", "new", 1, 1, LevelNote, unrelated),
				finding("a.go", "new", 10, 12, LevelNote, nilDeref),
				finding("c.go", "new", 1, 1, LevelNote, otherWords),
				finding("a.go", "new", 10, 12, LevelBlocking, nilDerefB),
			},
			want: []Finding{
				finding("b.go", "new", 1, 1, LevelNote, unrelated),
				finding("c.go", "new", 1, 1, LevelNote, otherWords),
				{
					Comment:  Comment{File: "a.go", Claim: nilDerefB},
					Location: Location{File: "a.go", Side: "new", Start: 10, End: 12},
					Level:    LevelBlocking,
					Merged:   1,
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Dedupe(tt.in)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Dedupe() =\n%+v\nwant\n%+v", got, tt.want)
			}
		})
	}
}
