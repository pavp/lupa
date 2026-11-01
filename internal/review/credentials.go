package review

import (
	"fmt"

	"github.com/pavp/lupa/internal/credentials"
	"github.com/pavp/lupa/internal/mapping"
	"github.com/pavp/lupa/internal/verify"
)

// credentialFindings turns the map's credential sites into findings that code raises, never the model:
// a value with a provider's structure is blocking, anything else a possible secret to verify.
func credentialFindings(sites []mapping.CredentialSite) []Anchored {
	var out []Anchored
	for _, s := range sites {
		c := verify.Comment{File: s.File, Severity: "severe", QuoteSettles: true, Harm: "security",
			Claim:     fmt.Sprintf("Credential added (%s)", s.Kind),
			Mechanism: "anyone who can read the repository or its history can use it, even after a later commit removes it",
			Fix:       "Rotate it, remove it, and read it from the environment or a secret store"}
		level := verify.LevelBlocking
		if s.Confidence != credentials.High {
			c.Severity, c.QuoteSettles = "warning", false
			c.Claim = fmt.Sprintf("Possible secret, verify (%s)", s.Kind)
			level = verify.LevelImportant
		}
		loc := verify.Location{File: s.File, Side: "new", Start: s.Line, End: s.Line, Method: "credential"}
		out = append(out, Anchored{Finding: verify.Finding{Comment: c, Location: loc, Causality: verify.CausalityIntroduced, Level: level}})
	}
	return out
}
