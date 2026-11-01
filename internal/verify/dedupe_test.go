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
			got, _ := Dedupe(tt.in)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Dedupe() =\n%+v\nwant\n%+v", got, tt.want)
			}
		})
	}
}

func harmed(file string, start int, level, harm, claim string) Finding {
	f := finding(file, "new", start, start, level, claim)
	f.Comment.Harm = harm
	return f
}

func TestDedupeAcrossFiles(t *testing.T) {
	const (
		staleA = "useRemoveUser drops the invalidateQueries promise so the list goes stale"
		staleB = "useRemoveUser drops the invalidateQueries promise, the list stays stale"
	)
	tests := []struct {
		name string
		in   []Finding
		want []Finding
		also [][]Location
	}{
		{
			name: "one defect seen from two files merges and keeps both locations",
			in: []Finding{
				harmed("src/api/users.ts", 12, LevelImportant, "wrong_data", staleA),
				harmed("src/ui/DeleteButton.tsx", 30, LevelBlocking, "wrong_data", staleB),
			},
			want: []Finding{func() Finding {
				f := harmed("src/ui/DeleteButton.tsx", 30, LevelBlocking, "wrong_data", staleB)
				f.Merged = 1
				return f
			}()},
			also: [][]Location{{{File: "src/api/users.ts", Side: "new", Start: 12, End: 12}}},
		},
		{
			name: "the same symbol with a different claim stays apart",
			in: []Finding{
				harmed("src/api/users.ts", 12, LevelImportant, "wrong_data", staleA),
				harmed("src/ui/DeleteButton.tsx", 30, LevelImportant, "wrong_data", "useRemoveUser sends the id of the wrong user to the endpoint"),
			},
		},
		{
			name: "a different harm stays apart",
			in: []Finding{
				harmed("src/api/users.ts", 12, LevelImportant, "wrong_data", staleA),
				harmed("src/ui/DeleteButton.tsx", 30, LevelImportant, "failure", staleB),
			},
		},
		{
			name: "similar claims without a shared symbol stay apart",
			in: []Finding{
				harmed("src/api/users.ts", 12, LevelImportant, "wrong_data", "the hook drops the promise so the list goes stale"),
				harmed("src/ui/DeleteButton.tsx", 30, LevelImportant, "wrong_data", "the hook drops the promise, the list stays stale"),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			want, wantAlso := tt.want, tt.also
			if want == nil {
				want, wantAlso = tt.in, make([][]Location, len(tt.in))
			}
			got, also := Dedupe(tt.in)
			if !reflect.DeepEqual(got, want) {
				t.Errorf("Dedupe =\n%+v\nwant\n%+v", got, want)
			}
			if !reflect.DeepEqual(also, wantAlso) {
				t.Errorf("also = %+v, want %+v", also, wantAlso)
			}
		})
	}
}
