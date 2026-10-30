package pi

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/pavp/lupa/internal/agent"
)

// successEvents is a two-turn run: a tool call, then the final answer, as `pi --mode json` prints them.
const successEvents = `{"type":"session","id":"s"}
{"type":"agent_start"}
{"type":"turn_start"}
{"type":"message_end","message":{"role":"system","content":""}}
{"type":"message_end","message":{"role":"user","content":[{"type":"text","text":"p"}]}}
{"type":"message_end","message":{"role":"assistant","content":[{"type":"toolCall","name":"read","arguments":{"path":"a.ts"}}],"usage":{"input":100,"output":10,"cacheRead":1000,"cacheWrite":0,"cost":{"total":0.25}},"stopReason":"toolUse"}}
{"type":"message_end","message":{"role":"toolResult","content":[{"type":"text","text":"file body"}]}}
{"type":"turn_end"}
{"type":"turn_start"}
{"type":"message_end","message":{"role":"assistant","content":[{"type":"thinking","thinking":"easy"},{"type":"text","text":"{\"comments\":"},{"type":"text","text":"[]}"}],"usage":{"input":200,"output":30,"cacheRead":1000,"cacheWrite":50,"cost":{"total":0.5}},"stopReason":"stop"}}
{"type":"turn_end"}
{"type":"agent_end","messages":[]}
{"type":"agent_settled","aborted":false}
`

// fakePi writes an executable that records its arguments, stdin and working directory into dir,
// then runs body. It returns the executable's path.
func fakePi(t *testing.T, dir, body string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake executable needs /bin/sh")
	}
	script := "#!/bin/sh\n" +
		"for a in \"$@\"; do printf '%s\\n' \"$a\"; done > '" + dir + "/args'\n" +
		"cat > '" + dir + "/stdin'\n" +
		"pwd > '" + dir + "/pwd'\n" +
		body + "\n"
	bin := filepath.Join(dir, "pi")
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}

// printing returns a fake body that prints events, written to a file so no quoting can bend them.
func printing(t *testing.T, dir, events string) string {
	t.Helper()
	path := filepath.Join(dir, "events")
	if err := os.WriteFile(path, []byte(events), 0o644); err != nil {
		t.Fatal(err)
	}
	return "cat '" + path + "'"
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestRunArguments(t *testing.T) {
	base := []string{"-p", "--mode", "json", "--no-extensions", "--no-skills", "--no-context-files", "--no-prompt-templates", "--no-session",
		"--no-mcp", "--no-approve", "--tools", "read,grep,find,ls", "--system-prompt", "You review code.", "--append-system-prompt", ""}
	tests := []struct {
		name    string
		adapter Adapter
		model   string
		schema  string
		want    []string
	}{
		{"defaults", Adapter{}, "", "", base},
		{"named extensions only", Adapter{Extensions: []string{"/ext/provider", "/ext/other"}}, "", "",
			append(slices.Clone(base), "-e", "/ext/provider", "-e", "/ext/other")},
		{"adapter model", Adapter{Model: "prov/flash"}, "", "", append(slices.Clone(base), "--model", "prov/flash")},
		{"request model wins", Adapter{Model: "prov/flash"}, "prov/pro", "", append(slices.Clone(base), "--model", "prov/pro")},
		{"schema is not passed", Adapter{}, "", `{"type":"object"}`, base},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			a := tt.adapter
			a.Bin = fakePi(t, dir, printing(t, dir, successEvents))

			_, err := a.Run(context.Background(), agent.Request{
				Dir: dir, SystemPrompt: "You review code.", Prompt: "SECRET-PROMPT\nline two", Model: tt.model, Schema: tt.schema,
			})
			if err != nil {
				t.Fatalf("Run: %v", err)
			}

			got := strings.Split(strings.TrimSuffix(readFile(t, filepath.Join(dir, "args")), "\n"), "\n")
			if !slices.Equal(got, tt.want) {
				t.Errorf("args = %q\nwant  %q", got, tt.want)
			}
			if i := slices.Index(got, "--tools"); i < 0 || i+1 == len(got) {
				t.Fatal("no tool allowlist")
			} else {
				for _, tool := range strings.Split(got[i+1], ",") {
					if !slices.Contains([]string{"read", "grep", "find", "ls"}, tool) {
						t.Errorf("tool %q is not read-only", tool)
					}
				}
			}
			for _, a := range got {
				if strings.Contains(a, "SECRET-PROMPT") {
					t.Errorf("prompt leaked into arguments: %q", a)
				}
			}
			if stdin := readFile(t, filepath.Join(dir, "stdin")); stdin != "SECRET-PROMPT\nline two" {
				t.Errorf("stdin = %q", stdin)
			}
			wantDir, _ := filepath.EvalSymlinks(dir)
			gotDir, _ := filepath.EvalSymlinks(strings.TrimSpace(readFile(t, filepath.Join(dir, "pwd"))))
			if gotDir != wantDir {
				t.Errorf("working dir = %q, want %q", gotDir, wantDir)
			}
		})
	}
}

func TestRunMapsEvents(t *testing.T) {
	dir := t.TempDir()
	bin := fakePi(t, dir, printing(t, dir, successEvents))

	got, err := Adapter{Bin: bin}.Run(context.Background(), agent.Request{Dir: dir, Prompt: "p"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := agent.Response{Text: `{"comments":[]}`, CostUSD: 0.75, Turns: 2, InputTokens: 2350, OutputTokens: 40}
	if got != want {
		t.Errorf("Response = %+v, want %+v", got, want)
	}
}

func TestRunErrors(t *testing.T) {
	tests := []struct {
		name   string
		events string
		body   string
		want   []string
	}{
		{
			name: "provider error exits 0",
			events: `{"type":"message_end","message":{"role":"assistant","content":[],"usage":{"input":0,"output":0,"cost":{"total":0}},"stopReason":"error","errorMessage":"Error 404: model not found"}}
{"type":"agent_settled","aborted":false}
`,
			want: []string{"error", "Error 404: model not found"},
		},
		{
			name: "aborted run",
			events: `{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"partial"}],"stopReason":"aborted"}}
{"type":"agent_settled","aborted":true}
`,
			want: []string{"aborted"},
		},
		{
			name:   "no assistant message",
			events: "{\"type\":\"session\"}\n{\"type\":\"agent_settled\",\"aborted\":false}\n",
			want:   []string{"no assistant message"},
		},
		{
			name: "non-zero exit",
			body: "echo 'Unknown provider \"x\"' >&2; exit 1",
			want: []string{"Unknown provider"},
		},
		{
			name: "garbage stdout",
			body: "echo 'not json at all'",
			want: []string{"json"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			body := tt.body
			if tt.events != "" {
				body = printing(t, dir, tt.events)
			}
			bin := fakePi(t, dir, body)

			_, err := Adapter{Bin: bin}.Run(context.Background(), agent.Request{Dir: dir, Prompt: "p"})
			if err == nil {
				t.Fatal("Run succeeded, want error")
			}
			for _, w := range tt.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("error %q does not mention %q", err, w)
				}
			}
		})
	}
}

func TestRunTimesOut(t *testing.T) {
	dir := t.TempDir()
	bin := fakePi(t, dir, "sleep 5")

	start := time.Now()
	_, err := Adapter{Bin: bin}.Run(context.Background(), agent.Request{Dir: dir, Prompt: "p", Timeout: 200 * time.Millisecond})
	elapsed := time.Since(start)

	if err == nil || !strings.Contains(err.Error(), "timed out after 200ms") {
		t.Fatalf("err = %v, want a timeout after 200ms", err)
	}
	if elapsed > 3*time.Second {
		t.Errorf("Run took %v, want it to stop near the limit", elapsed)
	}
}
