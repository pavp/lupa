package verify

import "testing"

var causalityPatches = map[string]string{
	"src/a.go": lines(
		"diff --git a/src/a.go b/src/a.go",
		"--- a/src/a.go",
		"+++ b/src/a.go",
		"@@ -5,6 +5,7 @@ import \"fmt\"",
		" func Hello(name string) string {",
		` 	if name == "" {`,
		`-		name = "nobody"`,
		`+		name = "world"`,
		" 	}",
		"",
		"+	retried := false",
		` 	return fmt.Sprintf("hello %s", name)`,
		"@@ -40,2 +41,3 @@ func Sum(xs []int) int {",
		" 	for _, x := range xs {",
		"+		total += x",
		" 	}",
		`\ No newline at end of file`,
	),
	"img/logo.png": lines(
		"diff --git a/img/logo.png b/img/logo.png",
		"index 1111111..2222222 100644",
		"Binary files a/img/logo.png and b/img/logo.png differ",
	),
	"bin/run.sh": lines(
		"diff --git a/bin/run.sh b/bin/run.sh",
		"old mode 100644",
		"new mode 100755",
	),
}

func TestCausality(t *testing.T) {
	tests := []struct {
		name string
		loc  Location
		want string
	}{
		{"single added line", Location{File: "src/a.go", Side: "new", Start: 7, End: 7}, CausalityIntroduced},
		{"added line after a blank context line", Location{File: "src/a.go", Side: "new", Start: 10, End: 10}, CausalityIntroduced},
		{"multi-line span where only the last line is added", Location{File: "src/a.go", Side: "new", Start: 5, End: 7}, CausalityIntroduced},
		{"added line in a later hunk", Location{File: "src/a.go", Side: "new", Start: 42, End: 42}, CausalityIntroduced},
		{"context-only lines inside a hunk", Location{File: "src/a.go", Side: "new", Start: 5, End: 6}, CausalityBehaviorActivated},
		{"context line right after an added line", Location{File: "src/a.go", Side: "new", Start: 11, End: 11}, CausalityBehaviorActivated},
		{"lines of the changed file outside the hunks", Location{File: "src/a.go", Side: "new", Start: 20, End: 22}, CausalityPreExisting},
		{"full-file match outside the hunks", Location{File: "src/a.go", Side: "new", Start: 1, End: 1, Method: "full-file"}, CausalityPreExisting},
		{"removed code on the old side", Location{File: "src/a.go", Side: "old", Start: 7, End: 7, Method: "old-side"}, CausalityIntroduced},
		{"binary file", Location{File: "img/logo.png", Side: "new", Start: 1, End: 1}, CausalityUnknown},
		{"mode-only change without hunks", Location{File: "bin/run.sh", Side: "new", Start: 1, End: 1}, CausalityUnknown},
		{"file with no patch", Location{File: "src/missing.go", Side: "new", Start: 1, End: 1}, CausalityUnknown},
		{"location without a side", Location{File: "src/a.go", Start: 7, End: 7}, CausalityPreExisting},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Causality(tt.loc, causalityPatches); got != tt.want {
				t.Errorf("Causality(%+v) = %q, want %q", tt.loc, got, tt.want)
			}
		})
	}
}

func TestLevelIgnoresHarm(t *testing.T) {
	tests := []struct {
		harm, severity string
		want           string
	}{
		{"failure", "warning", LevelNote},
		{"none", "severe", LevelBlocking},
		{"", "severe", LevelBlocking},
	}
	for _, tt := range tests {
		c := Comment{Harm: tt.harm, Severity: tt.severity, QuoteSettles: true}
		if got := Level(c, CausalityIntroduced); got != tt.want {
			t.Errorf("Level(harm %q, %s) = %q, want %q", tt.harm, tt.severity, got, tt.want)
		}
	}
}

func TestObservable(t *testing.T) {
	for _, h := range []string{"wrong_data", "failure", "security", "broken_caller"} {
		if !Observable(h) {
			t.Errorf("Observable(%q) = false, want true", h)
		}
	}
	for _, h := range []string{"slow", "none", ""} {
		if Observable(h) {
			t.Errorf("Observable(%q) = true, want false", h)
		}
	}
}

func TestLevel(t *testing.T) {
	tests := []struct {
		severity  string
		settles   bool
		causality string
		want      string
	}{
		{"severe", true, CausalityPreExisting, LevelFollowUp},
		{"severe", false, CausalityPreExisting, LevelFollowUp},
		{"warning", true, CausalityPreExisting, LevelFollowUp},
		{"warning", false, CausalityPreExisting, LevelFollowUp},
		{"severe", true, CausalityIntroduced, LevelBlocking},
		{"severe", true, CausalityBehaviorActivated, LevelBlocking},
		{"severe", false, CausalityIntroduced, LevelImportant},
		{"severe", false, CausalityBehaviorActivated, LevelImportant},
		{"severe", true, CausalityUnknown, LevelImportant},
		{"severe", false, CausalityUnknown, LevelImportant},
		{"warning", true, CausalityIntroduced, LevelNote},
		{"warning", false, CausalityIntroduced, LevelNote},
		{"warning", true, CausalityBehaviorActivated, LevelNote},
		{"warning", false, CausalityBehaviorActivated, LevelNote},
		{"warning", true, CausalityUnknown, LevelNote},
		{"warning", false, CausalityUnknown, LevelNote},
	}
	for _, tt := range tests {
		c := Comment{Severity: tt.severity, QuoteSettles: tt.settles, Claim: "the model calls this blocking"}
		if got := Level(c, tt.causality); got != tt.want {
			t.Errorf("Level(%s, settles=%v, %s) = %q, want %q", tt.severity, tt.settles, tt.causality, got, tt.want)
		}
	}
}
