package typescript

import (
	"reflect"
	"testing"
)

func TestImports(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want []string
	}{
		{"default import", `import React from "react";`, []string{"react"}},
		{"named and type imports", "import { a, type B } from './a';\nimport type { User } from '../types';", []string{"./a", "../types"}},
		{"namespace import", `import * as path from "node:path"`, []string{"node:path"}},
		{"side-effect import", `import "./polyfill.js";`, []string{"./polyfill.js"}},
		{"multi-line import", "import {\n  a,\n  b,\n} from \"@/lib\";", []string{"@/lib"}},
		{"export star", `export * from "./Button";`, []string{"./Button"}},
		{"named re-export", `export { formatDate as fmt } from "./date";`, []string{"./date"}},
		{"export type re-export", `export type { User } from "./types";`, []string{"./types"}},
		{"dynamic import", "const m = await import(\"./Lazy\");", []string{"./Lazy"}},
		{"require", "const { slug } = require('../utils');", []string{"../utils"}},
		{"line comment", "// import fake from \"./fake\";\nexport const x = 1;", nil},
		{"block comment", "/* require('./fake') */ export {};", nil},
		{"string mentioning an import", `const note = 'import nothing from "./also-fake"';`, nil},
		{"template literal", "const t = `import x from \"./fake\" ${require('./real')}`;", []string{"./real"}},
		{"regex with quotes", "const re = /['\"]import/g; import a from './a';", []string{"./a"}},
		{"division after a number", "const half = 10 / 2; const q = \"it's\"; import a from './a'; const r = 4 / 2;", []string{"./a"}},
		{"property named import", "loader.import('./x'); obj.require('./y');", nil},
		{"import.meta", "const u = import.meta.url;", nil},
		{"local export is not a re-export", "export const from = 1;\nexport function f() {}", nil},
		{"duplicates kept once in order", "import a from './a'; import b from './b'; export * from './a';", []string{"./a", "./b"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Imports([]byte(c.src))
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("Imports() = %q, want %q", got, c.want)
			}
		})
	}
}
