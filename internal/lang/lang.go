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

// Decl is a top-level declaration and the lines it spans, 1-based and inclusive.
// Default marks the module's default export, which importers bind as "default" whatever its Name.
type Decl struct {
	Name      string
	Default   bool
	Line, End int
}

// Binding is a name a file takes from a module: Imported is the exported name ("default" for a default
// import, "*" for a namespace or star), Local the name the file uses. Reexport marks `export … from`.
type Binding struct {
	Spec, Imported, Local string
	Line                  int
	Reexport              bool
}

// Symbols is what a provider may also offer to go below file level; without it the core stays at file level.
type Symbols interface {
	Decls(src []byte) []Decl
	Bindings(src []byte) []Binding
	// Uses returns the sorted lines that reference name, leaving out import clauses and member names (x.name).
	Uses(src []byte, name string) []int
}
