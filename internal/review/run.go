package review

import (
	"cmp"
	"context"
	"slices"
	"strings"
	"sync"
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
	// Parallel bounds how many groups are reviewed at once; zero means DefaultParallel, below one means one.
	Parallel int
	// Sentences of the PR description to check against ClaimPatches, the patches of the files a claim may cite.
	Sentences    []string
	ClaimPatches map[string]string
	// Threshold is the lowest score a finding needs to stay; ScoreModel is the scorer's model, empty for the agent's default.
	Threshold  int
	ScoreModel string
}

// DefaultThreshold is the score a finding needs by default: the best balance measured between noise and findings kept.
const DefaultThreshold = 6

// DefaultParallel is how many reviewer runs go at once when RunInput.Parallel is zero.
const DefaultParallel = 4

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

// RunOutput is the verified review: the findings that stay, the ones scored under the threshold, the dropped
// comments, the plan's coverage ledger updated by the run, the usage, the scorer's and claim check's notes, and
// the description's claims as checked, nil when there was no sentence to check.
type RunOutput struct {
	Claims   []ClaimCheck
	Findings []Anchored
	Low      []ScoredFinding
	Dropped  []Drop
	Ledger   []Entry
	Usage    Usage
	Notes    []string
}

const reasonUninspected = "the reviewer did not report inspecting it"

// Run reviews the planned groups, up to Parallel at a time, verifies and anchors their comments,
// then dedupes the findings of every group and scores them in one tool-free run. Results are merged
// in group order, whatever order the runs finish in. An unusable answer gets one retry; a failed
// group marks its files not reviewed and the other groups still run.
func Run(ctx context.Context, in RunInput) RunOutput {
	out := RunOutput{Ledger: slices.Clone(in.Plan.Ledger)}
	ag := meter{Agent: in.Agent, usage: &out.Usage, mu: &sync.Mutex{}}
	results := make([]groupResult, len(in.Plan.Groups))
	limit := make(chan struct{}, max(1, cmp.Or(in.Parallel, DefaultParallel)))
	var wg sync.WaitGroup
	var claims ClaimsResult
	if len(in.Sentences) > 0 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			limit <- struct{}{}
			defer func() { <-limit }()
			claims = CheckClaims(ctx, ClaimsInput{Agent: ag, Dir: in.Dir, Timeout: in.Timeout, Sentences: in.Sentences, Patches: in.ClaimPatches})
		}()
	}
	for i, g := range in.Plan.Groups {
		wg.Add(1)
		go func() {
			defer wg.Done()
			limit <- struct{}{}
			defer func() { <-limit }()
			results[i] = reviewOne(ctx, in, ag, g)
		}()
	}
	wg.Wait()

	var anchored []Anchored
	for i, r := range results {
		id := in.Plan.Groups[i].ID
		if r.reason != "" {
			out.Ledger = markGroup(out.Ledger, r.paths, id, r.reason)
			continue
		}
		out.Ledger = markGroup(out.Ledger, r.uninspected, id, reasonUninspected)
		out.Dropped = append(out.Dropped, r.dropped...)
		anchored = append(anchored, r.findings...)
	}

	merged := dedupe(anchored)
	for i := range merged {
		// A merge widens the survivor's range, which can now reach a changed line.
		if f := &merged[i].Finding; f.Merged > 0 {
			f.Causality = verify.Causality(f.Location, in.Patches)
			f.Level = verify.Level(f.Comment, f.Causality)
		}
	}
	// A pre-existing follow-up is about code the change does not touch, which the rubric scores 0, so it skips scoring.
	var toScore, followUps []Anchored
	for _, a := range merged {
		if a.Finding.Level == verify.LevelFollowUp {
			followUps = append(followUps, a)
		} else {
			toScore = append(toScore, a)
		}
	}
	// Only reviewed files reach the scorer: generated, binary and documentation patches would crowd its budget.
	reviewed := map[string]string{}
	for _, e := range in.Plan.Ledger {
		if e.Status == Reviewed {
			reviewed[e.Path] = in.Patches[e.Path]
		}
	}
	scored := Score(ctx, ScoreInput{Agent: ag, Dir: in.Dir, Timeout: in.Timeout, Model: in.ScoreModel, Threshold: in.Threshold,
		Budget: in.Plan.Budget, Findings: toScore, Patches: reviewed})
	out.Findings, out.Low = append(scored.Kept, followUps...), scored.Low
	if scored.Note != "" {
		out.Notes = append(out.Notes, scored.Note)
	}
	out.Notes = append(out.Notes, scored.Notes...)
	out.Claims = claims.Checks
	out.Notes = append(out.Notes, claims.Notes...)
	return out
}

// groupResult is what one group's review leaves for the merge; a non-empty reason means the
// group was not reviewed.
type groupResult struct {
	paths       []string
	reason      string
	uninspected []string
	dropped     []Drop
	findings    []Anchored
}

func reviewOne(ctx context.Context, in RunInput, ag agent.Agent, g Group) groupResult {
	patch, paths := groupPatch(g)
	p := BuildPrompt(PromptInput{Patch: patch, Paths: paths, Importers: in.Importers, Conventions: in.Conventions})
	r := groupResult{paths: paths}
	parsed, reason := reviewGroup(ctx, ag, agent.Request{Dir: in.Dir, SystemPrompt: p.System, Prompt: p.User, Timeout: in.Timeout, Schema: reviewSchema}, paths)
	if reason != "" {
		r.reason = reason
		return r
	}
	r.uninspected = parsed.Uninspected
	for _, d := range parsed.Dropped {
		r.dropped = append(r.dropped, Drop{Stage: StageSchema, Group: g.ID, Reason: d.Reason, Comment: verify.Comment{File: d.File}})
	}
	res := Anchor(ctx, AnchorInput{Agent: ag, Dir: in.Dir, Timeout: in.Timeout, Comments: parsed.Comments, Patches: in.Patches, Head: in.Head})
	for _, d := range res.Dropped {
		r.dropped = append(r.dropped, Drop{Stage: StageAnchor, Group: g.ID, Reason: d.Reason, Comment: d.Comment})
	}
	r.findings = res.Findings
	return r
}

// reviewGroup runs one reviewer and parses its answer, running it once more when the answer is
// unusable. A non-empty reason means the group was not reviewed.
func reviewGroup(ctx context.Context, ag agent.Agent, req agent.Request, paths []string) (verify.Result, string) {
	for attempt := 0; ; attempt++ {
		resp, err := ag.Run(ctx, req)
		if err != nil {
			return verify.Result{}, "agent run failed: " + err.Error()
		}
		parsed, err := verify.Parse(resp.Text, paths)
		if err == nil {
			return parsed, ""
		}
		if attempt == 1 {
			return verify.Result{}, "reviewer output unusable after one retry: " + err.Error()
		}
	}
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

// meter sums the usage of every call it forwards, from any number of goroutines.
type meter struct {
	agent.Agent
	usage *Usage
	mu    *sync.Mutex
}

func (m meter) Run(ctx context.Context, req agent.Request) (agent.Response, error) {
	resp, err := m.Agent.Run(ctx, req)
	m.mu.Lock()
	defer m.mu.Unlock()
	m.usage.Calls++
	m.usage.CostUSD += resp.CostUSD
	m.usage.InputTokens += resp.InputTokens
	m.usage.OutputTokens += resp.OutputTokens
	return resp, err
}
