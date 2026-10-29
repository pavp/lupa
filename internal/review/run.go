package review

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/pavp/lupa/internal/agent"
	"github.com/pavp/lupa/internal/verify"
)

// RunInput is what the agent review of a planned change needs: the plan, the change's patches and
// importers by path, the base-branch conventions, the agent and its run settings, and a reader of
// any repo file at the head of the change.
type RunInput struct {
	Plan        Result
	Patches     map[string]string
	Importers   map[string][]string
	Conventions []Convention
	Agent       agent.Agent
	Dir         string
	Timeout     time.Duration
	Head        func(path string) (string, bool)
}

// The stages that drop a comment before it becomes a finding.
const (
	StageSchema = "schema"
	StageAnchor = "anchor"
)

// Drop is a comment removed before it became a finding, with the stage, group and reason. A
// schema drop carries only the comment's file: its other fields did not pass the schema.
type Drop struct {
	Stage   string
	Group   int
	Reason  string
	Comment verify.Comment
}

// Usage is the sum of every agent call of the run, including failed ones.
type Usage struct {
	Calls        int
	CostUSD      float64
	InputTokens  int
	OutputTokens int
}

// RunOutput is the verified review: the findings that stay, the refuted ones, the dropped comments,
// the plan's coverage ledger updated by the run, the usage and the filter's notes.
type RunOutput struct {
	Findings []Anchored
	Refuted  []RefutedFinding
	Dropped  []Drop
	Ledger   []Entry
	Usage    Usage
	Notes    []string
}

const reasonUninspected = "the reviewer did not report inspecting it"

// Run reviews each planned group in order, verifies and anchors its comments, then dedupes the
// findings of every group and filters them in one refuter run. A failed group marks its files
// not reviewed and the next group still runs.
func Run(ctx context.Context, in RunInput) RunOutput {
	out := RunOutput{Ledger: slices.Clone(in.Plan.Ledger)}
	ag := meter{Agent: in.Agent, usage: &out.Usage}
	var anchored []Anchored
	for _, g := range in.Plan.Groups {
		patch, paths := groupPatch(g)
		p := BuildPrompt(PromptInput{Patch: patch, Paths: paths, Importers: in.Importers, Conventions: in.Conventions})
		resp, err := ag.Run(ctx, agent.Request{Dir: in.Dir, SystemPrompt: p.System, Prompt: p.User, Timeout: in.Timeout})
		if err != nil {
			out.Ledger = markGroup(out.Ledger, paths, g.ID, "agent run failed: "+err.Error())
			continue
		}
		parsed, err := verify.Parse(resp.Text, paths)
		if err != nil {
			out.Ledger = markGroup(out.Ledger, paths, g.ID, "reviewer output unusable: "+err.Error())
			continue
		}
		out.Ledger = markGroup(out.Ledger, parsed.Uninspected, g.ID, reasonUninspected)
		for _, d := range parsed.Dropped {
			out.Dropped = append(out.Dropped, Drop{Stage: StageSchema, Group: g.ID, Reason: d.Reason, Comment: verify.Comment{File: d.File}})
		}
		res := Anchor(ctx, AnchorInput{Agent: ag, Dir: in.Dir, Timeout: in.Timeout, Comments: parsed.Comments, Patches: in.Patches, Head: in.Head})
		for _, d := range res.Dropped {
			out.Dropped = append(out.Dropped, Drop{Stage: StageAnchor, Group: g.ID, Reason: d.Reason, Comment: d.Comment})
		}
		anchored = append(anchored, res.Findings...)
	}

	filtered := Filter(ctx, FilterInput{Agent: ag, Dir: in.Dir, Timeout: in.Timeout, Findings: dedupe(anchored), Head: in.Head})
	out.Findings, out.Refuted = filtered.Kept, filtered.Refuted
	if filtered.Note != "" {
		out.Notes = append(out.Notes, filtered.Note)
	}
	out.Notes = append(out.Notes, filtered.Notes...)
	return out
}

// groupPatch joins the patches of a group's parts, each already the whole file or its header
// plus the part's hunks, and lists the group's paths once each in part order.
func groupPatch(g Group) (patch string, paths []string) {
	var b strings.Builder
	for _, p := range g.Parts {
		b.WriteString(p.Patch)
		if !slices.Contains(paths, p.Path) {
			paths = append(paths, p.Path)
		}
	}
	return b.String(), paths
}

// markGroup moves group id of each path's reviewed entry to a not-reviewed entry with the reason;
// a file whose other parts were reviewed in other groups keeps its reviewed entry for those.
func markGroup(ledger []Entry, paths []string, id int, reason string) []Entry {
	for _, path := range paths {
		i := slices.IndexFunc(ledger, func(e Entry) bool {
			return e.Path == path && e.Status == Reviewed && slices.Contains(e.Groups, id)
		})
		if i < 0 {
			continue
		}
		miss := Entry{Path: path, Status: NotReviewed, Reason: reason, Groups: []int{id}}
		rest := slices.DeleteFunc(slices.Clone(ledger[i].Groups), func(g int) bool { return g == id })
		if len(rest) == 0 {
			ledger[i] = miss
			continue
		}
		ledger[i].Groups = rest
		ledger = slices.Insert(ledger, i+1, miss)
	}
	return ledger
}

// dedupe merges duplicate findings and gives each survivor back its Relocated flag. Survivors
// keep their own comment and come out in input order, so they are matched as a subsequence.
func dedupe(fs []Anchored) []Anchored {
	plain := make([]verify.Finding, len(fs))
	for i, f := range fs {
		plain[i] = f.Finding
	}
	var out []Anchored
	i := 0
	for _, f := range verify.Dedupe(plain) {
		for i < len(fs) && fs[i].Finding.Comment != f.Comment {
			i++
		}
		relocated := i < len(fs) && fs[i].Relocated
		out = append(out, Anchored{Finding: f, Relocated: relocated})
		i++
	}
	return out
}

// meter sums the usage of every call it forwards; the run is sequential, so it needs no lock.
type meter struct {
	agent.Agent
	usage *Usage
}

func (m meter) Run(ctx context.Context, req agent.Request) (agent.Response, error) {
	resp, err := m.Agent.Run(ctx, req)
	m.usage.Calls++
	m.usage.CostUSD += resp.CostUSD
	m.usage.InputTokens += resp.InputTokens
	m.usage.OutputTokens += resp.OutputTokens
	return resp, err
}
