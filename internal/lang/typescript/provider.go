package typescript

import (
	"path"
	"strings"

	"github.com/pavp/lupa/internal/lang"
)

// Provider is the TypeScript/JavaScript language provider.
type Provider struct{}

var _ lang.Provider = Provider{}

// Owns reports whether p is a TS/JS source file; declaration files and node_modules are not.
func (Provider) Owns(p string) bool {
	if strings.HasSuffix(p, ".d.ts") || strings.HasPrefix(p, "node_modules/") || strings.Contains(p, "/node_modules/") {
		return false
	}
	ext := path.Ext(p)
	for _, e := range codeExts {
		if ext == e {
			return true
		}
	}
	return false
}

// Imports returns the module specifiers src imports.
func (Provider) Imports(src []byte) []string { return Imports(src) }

// NewResolver builds the resolver for the repo's files.
func (Provider) NewResolver(files []string, readFile func(string) ([]byte, error)) (lang.Resolver, error) {
	return NewResolver(files, readFile)
}
