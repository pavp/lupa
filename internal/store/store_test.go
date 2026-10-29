package store

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSaveAndLast(t *testing.T) {
	s := Store{Root: filepath.Join(t.TempDir(), "lupa")}
	if _, ok := s.Last("feature/x"); ok {
		t.Fatal("a fresh store has no last map")
	}
	first := "1111111111111111111111111111111111111111"
	p, err := s.Save("feature/x", first, []byte(`{"n":1}`), []byte("# map 1\n"))
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(s.Root, "feature-x", "1111111"); p != want {
		t.Errorf("saved at %q, want %q", p, want)
	}
	for name, want := range map[string]string{"signals.json": `{"n":1}`, "map.md": "# map 1\n"} {
		if got, err := os.ReadFile(filepath.Join(p, name)); err != nil || string(got) != want {
			t.Errorf("%s = %q, %v; want %q", name, got, err, want)
		}
	}
	second := "2222222222222222222222222222222222222222"
	if _, err := s.Save("feature/x", second, []byte(`{}`), nil); err != nil {
		t.Fatal(err)
	}
	if got, ok := s.Last("feature/x"); !ok || got != second {
		t.Errorf("Last = %q, %v; want the second head", got, ok)
	}
	if _, ok := s.Last("other"); ok {
		t.Error("keys are independent")
	}
}

func TestPutWritesBesideTheMap(t *testing.T) {
	s := Store{Root: filepath.Join(t.TempDir(), "lupa")}
	head := "3333333333333333333333333333333333333333"
	p, err := s.Put("feature/x", head, "review.json", []byte(`{"r":1}`))
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(s.Root, "feature-x", "3333333", "review.json"); p != want {
		t.Errorf("written at %q, want %q", p, want)
	}
	if got, err := os.ReadFile(p); err != nil || string(got) != `{"r":1}` {
		t.Errorf("review.json = %q, %v", got, err)
	}
	if _, ok := s.Last("feature/x"); ok {
		t.Error("Put must not record a last map")
	}
}
