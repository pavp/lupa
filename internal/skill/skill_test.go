package skill

import (
	"bytes"
	"strings"
	"testing"
)

func TestTheSkillHasTheFrontmatterAgentsRead(t *testing.T) {
	front, _, ok := strings.Cut(strings.TrimPrefix(string(content), "---\n"), "\n---\n")
	if !ok || !strings.HasPrefix(string(content), "---\nname: lupa\n") {
		t.Fatalf("skill does not open with frontmatter naming lupa")
	}
	var description string
	for _, line := range strings.Split(front, "\n") {
		if d, found := strings.CutPrefix(line, "description: "); found {
			description = d
		}
	}
	if !strings.HasPrefix(description, "\"Trigger: ") || !strings.HasSuffix(description, "\"") || len(description) > 250 {
		t.Errorf("description %q: want one quoted line starting with Trigger:, at most 250 chars", description)
	}
	if !bytes.Contains(content, marker) {
		t.Error("skill lacks the marker that tells an install it wrote it")
	}
}
