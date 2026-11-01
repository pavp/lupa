package review

import (
	"context"

	"github.com/pavp/lupa/internal/agent"
	"github.com/pavp/lupa/internal/credentials"
)

// redacting replaces credentials in every prompt before its agent sees it.
type redacting struct{ agent.Agent }

func (r redacting) Run(ctx context.Context, req agent.Request) (agent.Response, error) {
	req.SystemPrompt, req.Prompt = credentials.Redact(req.SystemPrompt), credentials.Redact(req.Prompt)
	return r.Agent.Run(ctx, req)
}
