// Package agent defines how lupa runs one review through a coding-agent CLI.
package agent

import (
	"context"
	"sync"
	"time"
)

// Request is one isolated agent run.
type Request struct {
	// Dir is the working directory of the run: the detached worktree at the PR head.
	Dir          string
	SystemPrompt string
	Prompt       string
	// Timeout bounds the run; adapters apply their own default when it is zero.
	Timeout time.Duration
	// Model is optional; empty keeps the agent's default.
	Model string
	// Schema is an optional JSON Schema the final answer must follow, for agents that can enforce one.
	Schema string
}

// Response is an agent's final answer with the usage it reported.
type Response struct {
	Text         string
	CostUSD      float64
	Turns        int
	InputTokens  int
	OutputTokens int
}

// Agent runs one request to completion.
type Agent interface {
	Run(ctx context.Context, req Request) (Response, error)
}

// Fake is an Agent for pipeline tests: Reply produces the answer text, and calls are counted.
type Fake struct {
	Reply func(Request) (string, error)

	mu    sync.Mutex
	calls int
}

// Run returns Reply's text for req.
func (f *Fake) Run(_ context.Context, req Request) (Response, error) {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()
	text, err := f.Reply(req)
	if err != nil {
		return Response{}, err
	}
	return Response{Text: text}, nil
}

// Calls reports how many times Run was called.
func (f *Fake) Calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}
