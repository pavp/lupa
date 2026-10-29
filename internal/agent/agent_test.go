package agent

import (
	"context"
	"errors"
	"testing"
)

func TestFakeReturnsReplyAndCountsCalls(t *testing.T) {
	f := &Fake{Reply: func(req Request) (string, error) { return "echo:" + req.Prompt, nil }}

	for _, prompt := range []string{"a", "b"} {
		resp, err := f.Run(context.Background(), Request{Prompt: prompt})
		if err != nil {
			t.Fatalf("Run(%q): %v", prompt, err)
		}
		if resp.Text != "echo:"+prompt {
			t.Errorf("Run(%q).Text = %q", prompt, resp.Text)
		}
	}
	if f.Calls() != 2 {
		t.Errorf("Calls() = %d, want 2", f.Calls())
	}
}

func TestFakePassesReplyError(t *testing.T) {
	want := errors.New("boom")
	f := &Fake{Reply: func(Request) (string, error) { return "", want }}

	if _, err := f.Run(context.Background(), Request{}); !errors.Is(err, want) {
		t.Fatalf("err = %v, want %v", err, want)
	}
	if f.Calls() != 1 {
		t.Errorf("Calls() = %d, want 1", f.Calls())
	}
}
