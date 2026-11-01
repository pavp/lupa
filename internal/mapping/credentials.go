package mapping

import (
	"fmt"
	"slices"
	"strings"

	"github.com/pavp/lupa/internal/credentials"
	"github.com/pavp/lupa/internal/verify"
)

// CredentialSite is an added line that holds a credential. It never carries the value, so the map
// can be saved and shown without leaking it.
type CredentialSite struct {
	File       string                 `json:"file"`
	Line       int                    `json:"line"`
	Kind       string                 `json:"kind"`
	Confidence credentials.Confidence `json:"confidence"`
}

// credentialSites returns the added lines that hold a credential, one site per line with its most
// confident match. The path does not lower a match: a live key in a fixture is leaked all the same.
func credentialSites(files []File, patches map[string]string) []CredentialSite {
	var out []CredentialSite
	for _, f := range files {
		for _, c := range verify.ChangedLines(patches[f.Path]) {
			if c.Side != "+" {
				continue
			}
			ms := credentials.Find(c.Text)
			if len(ms) == 0 {
				continue
			}
			best := ms[0]
			if i := slices.IndexFunc(ms, func(m credentials.Match) bool { return m.Confidence == credentials.High }); i >= 0 {
				best = ms[i]
			}
			out = append(out, CredentialSite{File: f.Path, Line: c.N, Kind: best.Kind, Confidence: best.Confidence})
		}
	}
	return out
}

// liveCredentials hands the change back when an added line holds a credential with a provider's
// structure: it needs rotating, not reviewing, and a later commit that removes it leaves it in the history.
func liveCredentials(sites []CredentialSite) (Gate, bool) {
	var where []string
	for _, s := range sites {
		if s.Confidence == credentials.High {
			where = append(where, fmt.Sprintf("%s at %s:%d", s.Kind, s.File, s.Line))
		}
	}
	if len(where) == 0 {
		return Gate{}, false
	}
	list := strings.Join(where, ", ")
	return Gate{HandBack, "adds a credential: " + list + ": rotate it",
		fmt.Sprintf("This change adds what looks like a live credential (%s). Could you rotate it and read it from the environment or a secret store? Removing it in a later commit leaves it in the history.", list)}, true
}
