// Package claude runs a review through the Claude Code CLI, isolated and read-only.
package claude

import (
	"bytes"
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
// since tool subprocesses can outlive claude and keep them open.
const waitDelay = time.Second

// Adapter runs requests through the claude executable.
type Adapter struct {
	// Bin is the executable to run; empty means "claude" on PATH.
	Bin string
}

type envelope struct {
	Type             string          `json:"type"`
	Subtype          string          `json:"subtype"`
	IsError          bool            `json:"is_error"`
	Result           string          `json:"result"`
	StructuredOutput json.RawMessage `json:"structured_output"`
	NumTurns         int             `json:"num_turns"`
	TotalCostUSD     float64         `json:"total_cost_usd"`
	Usage            struct {
		InputTokens              int `json:"input_tokens"`
		CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
		CacheReadInputTokens     int `json:"cache_read_input_tokens"`
		OutputTokens             int `json:"output_tokens"`
	} `json:"usage"`
}

// Run executes req with read-only tools and none of the user's settings, MCP
// servers or sessions, and returns the parsed final answer.
func (a Adapter) Run(ctx context.Context, req agent.Request) (agent.Response, error) {
	bin := a.Bin
	if bin == "" {
		bin = "claude"
	}
	timeout := req.Timeout
	if timeout == 0 {
		timeout = DefaultTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// The tool allowlist is the isolation boundary: never add a permission-bypass mode,
	// and never --bare, which fails on a subscription login.
	tools := "Read,Grep,Glob"
	if req.NoTools {
		tools = ""
	}
	args := []string{
		"-p", "--output-format", "json",
		"--tools", tools,
		"--setting-sources", "",
		"--strict-mcp-config",
		"--no-session-persistence",
		// --setting-sources "" already keeps the reviewed repo's CLAUDE.md out (verified); safe mode also drops plugins and hooks.
		"--safe-mode",
		"--system-prompt", req.SystemPrompt,
	}
	if req.Model != "" {
		args = append(args, "--model", req.Model)
	}
	if req.Schema != "" {
		args = append(args, "--json-schema", req.Schema)
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = req.Dir
	cmd.Stdin = strings.NewReader(req.Prompt)
	cmd.WaitDelay = waitDelay
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return agent.Response{}, fmt.Errorf("claude run timed out after %s", timeout)
	}
	if err != nil {
		return agent.Response{}, fmt.Errorf("claude: %w: %s", err, strings.TrimSpace(stderr.String()))
	}

	var env envelope
	if err := json.Unmarshal(stdout.Bytes(), &env); err != nil {
		return agent.Response{}, fmt.Errorf("claude: stdout is not a json result envelope: %w", err)
	}
	if env.Type != "result" {
		return agent.Response{}, fmt.Errorf("claude: envelope type %q, want %q", env.Type, "result")
	}
	if env.IsError || env.Subtype != "success" {
		return agent.Response{}, fmt.Errorf("claude: run failed (subtype %q): %s", env.Subtype, env.Result)
	}
	text := env.Result
	if len(env.StructuredOutput) > 0 && string(env.StructuredOutput) != "null" {
		text = string(env.StructuredOutput)
	}
	u := env.Usage
	return agent.Response{
		Text:         text,
		CostUSD:      env.TotalCostUSD,
		Turns:        env.NumTurns,
		InputTokens:  u.InputTokens + u.CacheCreationInputTokens + u.CacheReadInputTokens,
		OutputTokens: u.OutputTokens,
	}, nil
}
