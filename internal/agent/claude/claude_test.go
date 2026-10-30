package claude

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

const successEnvelope = `{"type":"result","subtype":"success","is_error":false,"result":"{\"comments\":[]}","num_turns":3,"total_cost_usd":0.0123,"usage":{"input_tokens":1500,"output_tokens":42,"cache_read_input_tokens":7},"permission_denials":[],"session_id":"x"}`

// fakeClaude writes an executable that records its arguments, stdin and working
// directory into dir, then runs body. It returns the executable's path.
func fakeClaude(t *testing.T, dir, body string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake executable needs /bin/sh")
	}
	script := "#!/bin/sh\n" +
		"for a in \"$@\"; do printf '%s\\n' \"$a\"; done > '" + dir + "/args'\n" +
		"cat > '" + dir + "/stdin'\n" +
		"pwd > '" + dir + "/pwd'\n" +
		body + "\n"
	bin := filepath.Join(dir, "claude")
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
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
	base := []string{"-p", "--output-format", "json", "--tools", "Read,Grep,Glob", "--setting-sources", "", "--strict-mcp-config", "--no-session-persistence", "--safe-mode", "--system-prompt", "You review code."}
	tests := []struct {
		name   string
		model  string
		schema string
		want   []string
	}{
		{"default model", "", "", base},
		{"model set", "opus", "", append(slices.Clone(base), "--model", "opus")},
		{"schema set", "", `{"type":"object"}`, append(slices.Clone(base), "--json-schema", `{"type":"object"}`)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			bin := fakeClaude(t, dir, "printf '%s' '"+successEnvelope+"'")

			_, err := Adapter{Bin: bin}.Run(context.Background(), agent.Request{
				Dir: dir, SystemPrompt: "You review code.", Prompt: "SECRET-PROMPT\nline two", Model: tt.model, Schema: tt.schema,
			})
			if err != nil {
				t.Fatalf("Run: %v", err)
			}

			got := strings.Split(strings.TrimSuffix(readFile(t, filepath.Join(dir, "args")), "\n"), "\n")
			if !slices.Equal(got, tt.want) {
				t.Errorf("args = %q\nwant  %q", got, tt.want)
			}
			for _, a := range got {
				switch a {
				case "--dangerously-skip-permissions", "--allow-dangerously-skip-permissions", "bypassPermissions", "--permission-mode", "--bare":
					t.Errorf("forbidden argument %q", a)
				}
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

func TestRunMapsSuccessEnvelope(t *testing.T) {
	dir := t.TempDir()
	bin := fakeClaude(t, dir, "printf '%s' '"+successEnvelope+"'")

	got, err := Adapter{Bin: bin}.Run(context.Background(), agent.Request{Dir: dir, Prompt: "p"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := agent.Response{Text: `{"comments":[]}`, CostUSD: 0.0123, Turns: 3, InputTokens: 1507, OutputTokens: 42}
	if got != want {
		t.Errorf("Response = %+v, want %+v", got, want)
	}
}

func TestRunPrefersStructuredOutput(t *testing.T) {
	envelope := `{"type":"result","subtype":"success","is_error":false,"result":"Here it is: {\"a\":1}","structured_output":{"a":1},"usage":{"input_tokens":2,"cache_creation_input_tokens":1150,"cache_read_input_tokens":30,"output_tokens":5}}`
	dir := t.TempDir()
	bin := fakeClaude(t, dir, "printf '%s' '"+envelope+"'")

	got, err := Adapter{Bin: bin}.Run(context.Background(), agent.Request{Dir: dir, Prompt: "p", Schema: `{"type":"object"}`})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got.Text != `{"a":1}` {
		t.Errorf("Text = %q, want the structured output", got.Text)
	}
	if got.InputTokens != 1182 {
		t.Errorf("InputTokens = %d, want 1182 (input plus cache creation and reads)", got.InputTokens)
	}
}

func TestRunErrors(t *testing.T) {
	tests := []struct {
		name string
		body string
		want []string
	}{
		{
			name: "is_error envelope",
			body: `printf '%s' '{"type":"result","subtype":"success","is_error":true,"result":"Credit balance is too low"}'`,
			want: []string{"success", "Credit balance is too low"},
		},
		{
			name: "non-success subtype",
			body: `printf '%s' '{"type":"result","subtype":"error_max_turns","is_error":false,"result":""}'`,
			want: []string{"error_max_turns"},
		},
		{
			name: "not a result envelope",
			body: `printf '%s' '{"type":"system","subtype":"init"}'`,
			want: []string{"system"},
		},
		{
			name: "non-zero exit",
			body: "echo 'Invalid API key' >&2; exit 3",
			want: []string{"Invalid API key"},
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
			bin := fakeClaude(t, dir, tt.body)

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
	bin := fakeClaude(t, dir, "sleep 5")

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

func TestRunUsesTheAdapterModelUnlessTheRequestNamesOne(t *testing.T) {
	for _, tt := range []struct{ adapter, request, want string }{{"opus", "", "opus"}, {"opus", "sonnet", "sonnet"}} {
		dir := t.TempDir()
		bin := fakeClaude(t, dir, "printf '%s' '"+successEnvelope+"'")
		if _, err := (Adapter{Bin: bin, Model: tt.adapter}).Run(context.Background(), agent.Request{Dir: dir, Prompt: "p", Model: tt.request}); err != nil {
			t.Fatalf("Run: %v", err)
		}
		args := strings.Split(readFile(t, filepath.Join(dir, "args")), "\n")
		if i := slices.Index(args, "--model"); i < 0 || args[i+1] != tt.want || slices.Index(args[i+1:], "--model") >= 0 {
			t.Errorf("adapter %q, request %q: args = %q, want one --model %s", tt.adapter, tt.request, args, tt.want)
		}
	}
}
