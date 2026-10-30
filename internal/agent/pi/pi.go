// Package pi runs a review through the Pi coding agent CLI, isolated and read-only.
package pi

import (
	"bufio"
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/pavp/lupa/internal/agent"
)

// DefaultTimeout bounds a run whose request sets no timeout.
const DefaultTimeout = 10 * time.Minute

// waitDelay caps how long Run waits for output pipes after the process is killed,
// since tool subprocesses can outlive pi and keep them open.
const waitDelay = time.Second

// Adapter runs requests through the pi executable.
type Adapter struct {
	// Bin is the executable to run; empty means "pi" on PATH.
	Bin string
	// Model is the provider/id used when a request names none; empty keeps Pi's default.
	Model string
	// Extensions are the only Pi extensions loaded, typically the one that provides the model; every other is off.
	Extensions []string
}

type event struct {
	Type    string `json:"type"`
	Message struct {
		Role string `json:"role"`
		// Content is a list of parts for an assistant message, and can be a plain string for others.
		Content json.RawMessage `json:"content"`
		Usage   struct {
			Input      int `json:"input"`
			Output     int `json:"output"`
			CacheRead  int `json:"cacheRead"`
			CacheWrite int `json:"cacheWrite"`
			Cost       struct {
				Total float64 `json:"total"`
			} `json:"cost"`
		} `json:"usage"`
		StopReason   string `json:"stopReason"`
		ErrorMessage string `json:"errorMessage"`
	} `json:"message"`
}

// Run executes req with read-only tools and none of the user's extensions, skills, context files or
// sessions, and returns the final answer. Pi cannot enforce req.Schema; callers validate the answer.
func (a Adapter) Run(ctx context.Context, req agent.Request) (agent.Response, error) {
	bin := a.Bin
	if bin == "" {
		bin = "pi"
	}
	timeout := req.Timeout
	if timeout == 0 {
		timeout = DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// The tool allowlist is the isolation boundary: never add bash, edit or write. User extensions can
	// autoformat, autofix and inject context, so only the ones named here load.
	args := []string{
		"-p", "--mode", "json",
		"--no-extensions", "--no-skills", "--no-context-files", "--no-prompt-templates", "--no-session", "--no-mcp",
		"--tools", "read,grep,find,ls",
		"--system-prompt", req.SystemPrompt,
	}
	for _, e := range a.Extensions {
		args = append(args, "-e", e)
	}
	if model := cmp.Or(req.Model, a.Model); model != "" {
		args = append(args, "--model", model)
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = req.Dir
	// With the prompt as an argument and stdin open, pi waits on stdin; the prompt goes there instead.
	cmd.Stdin = strings.NewReader(req.Prompt)
	cmd.WaitDelay = waitDelay
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return agent.Response{}, fmt.Errorf("pi run timed out after %s", timeout)
	}
	if err != nil {
		return agent.Response{}, fmt.Errorf("pi: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return parse(stdout.Bytes())
}

// parse reads pi's JSON events: the answer is the text of the last assistant message, and usage adds up
// over every assistant message. A provider error still exits 0, so the last message's stop reason decides.
func parse(out []byte) (agent.Response, error) {
	var resp agent.Response
	var last *event
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 0, 64*1024), 64*1024*1024)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var e event
		if err := json.Unmarshal(line, &e); err != nil {
			return agent.Response{}, fmt.Errorf("pi: stdout is not json events: %w", err)
		}
		switch {
		case e.Type == "turn_end":
			resp.Turns++
		case e.Type == "message_end" && e.Message.Role == "assistant":
			u := e.Message.Usage
			resp.CostUSD += u.Cost.Total
			resp.InputTokens += u.Input + u.CacheRead + u.CacheWrite
			resp.OutputTokens += u.Output
			last = &e
		}
	}
	if err := sc.Err(); err != nil {
		return agent.Response{}, fmt.Errorf("pi: reading events: %w", err)
	}
	if last == nil {
		return agent.Response{}, errors.New("pi: no assistant message in the output")
	}
	if r := last.Message.StopReason; r == "error" || r == "aborted" {
		return agent.Response{}, fmt.Errorf("pi: run ended with %s: %s", r, last.Message.ErrorMessage)
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(last.Message.Content, &parts); err != nil {
		return agent.Response{}, fmt.Errorf("pi: assistant message content: %w", err)
	}
	var text strings.Builder
	for _, c := range parts {
		if c.Type == "text" {
			text.WriteString(c.Text)
		}
	}
	resp.Text = text.String()
	return resp, nil
}
