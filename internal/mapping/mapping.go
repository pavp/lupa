// Package mapping turns a diff into the map's signals. It is pure: data in, data out.
package mapping

// Schema versions the signals JSON; consumers check it before reading anything else.
const Schema = "lupa.signals/v0"

// Signals is the map of one change, the shape written by `lupa map --json`.
type Signals struct {
	Schema string `json:"schema"`
	Base   string `json:"base"`
	Head   string `json:"head"`
}

// Build computes the signals of the change from base to head.
func Build(base, head string) Signals {
	return Signals{Schema: Schema, Base: base, Head: head}
}
