// Package lang declares what the core needs from a language provider; providers live in subpackages.
package lang

// Status says how an import specifier resolved.
type Status int

const (
	// Resolved: the specifier names a source file in the repo.
	Resolved Status = iota + 1
	// External: a package or builtin outside the repo.
	External
	// Unresolved: the specifier names a repo file (relative path, alias, workspace package) that does not exist.
	Unresolved
)

// Resolver maps an import specifier, written in the file from, to the repo file it names.
type Resolver interface {
	Resolve(from, spec string) (string, Status)
}

// Provider is one language: which files it owns, how to find their imports, and how to resolve them.
type Provider interface {
	Owns(path string) bool
	Imports(src []byte) []string
	NewResolver(files []string, readFile func(path string) ([]byte, error)) (Resolver, error)
}
