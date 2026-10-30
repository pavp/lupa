package typescript

import (
	"reflect"
	"testing"

	"github.com/pavp/lupa/internal/lang"
)

func TestDecls(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want []lang.Decl
	}{
		{"function spans its body", "import a from './a';\n\nexport function load(id) {\n  return a(id);\n}\n", []lang.Decl{{Name: "load", Line: 3, End: 5}}},
		{"consecutive declarations without semicolons", "const a = 1\nlet b = a\n  + 2\nvar c = b\n", []lang.Decl{{Name: "a", Line: 1, End: 1}, {Name: "b", Line: 2, End: 3}, {Name: "c", Line: 4, End: 4}}},
		{"arrow function constant", "export const run = async (x) => {\n  await x;\n};\nrun(1);\n", []lang.Decl{{Name: "run", Line: 1, End: 3}}},
		{"class with members", "export class Store {\n  get(k) {\n    return this.m[k];\n  }\n}\n", []lang.Decl{{Name: "Store", Line: 1, End: 5}}},
		{"default function", "export default function handler(req) {\n  return req;\n}\n", []lang.Decl{{Name: "handler", Default: true, Line: 1, End: 3}}},
		{"default expression", "const x = 1;\nexport default {\n  x,\n};\n", []lang.Decl{{Name: "x", Line: 1, End: 1}, {Name: "default", Default: true, Line: 2, End: 4}}},
		{"types and enums", "export interface User {\n  id: string;\n}\ntype Id =\n  | string\n  | number;\nexport enum Role { Admin }\n", []lang.Decl{{Name: "User", Line: 1, End: 3}, {Name: "Id", Line: 4, End: 6}, {Name: "Role", Line: 7, End: 7}}},
		{"nested declarations are not top level", "function outer() {\n  const inner = 1;\n  function deep() {}\n}\n", []lang.Decl{{Name: "outer", Line: 1, End: 4}}},
		{"template and comment do not shift lines", "const s = `a\n${ {b: 1}.b }\nc`;\n/* x\n y */\nfunction f() {}\n", []lang.Decl{{Name: "s", Line: 1, End: 3}, {Name: "f", Line: 6, End: 6}}},
		{"destructuring and re-exports declare nothing", "const { a } = obj;\nexport { a };\nexport * from './b';\n", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := (Provider{}).Decls([]byte(c.src)); !reflect.DeepEqual(got, c.want) {
				t.Errorf("Decls() = %+v, want %+v", got, c.want)
			}
		})
	}
}

func TestBindings(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want []lang.Binding
	}{
		{"default and named", "import React, { useState as useS, type FC } from 'react';", []lang.Binding{
			{Spec: "react", Imported: "default", Local: "React", Line: 1},
			{Spec: "react", Imported: "useState", Local: "useS", Line: 1},
			{Spec: "react", Imported: "FC", Local: "FC", Line: 1},
		}},
		{"multi-line named import", "import {\n  a,\n  b,\n} from './x';", []lang.Binding{
			{Spec: "./x", Imported: "a", Local: "a", Line: 2},
			{Spec: "./x", Imported: "b", Local: "b", Line: 3},
		}},
		{"namespace import", "import * as path from 'node:path';", []lang.Binding{{Spec: "node:path", Imported: "*", Local: "path", Line: 1}}},
		{"re-exports", "export { a, b as c } from './a';\nexport * from './b';", []lang.Binding{
			{Spec: "./a", Imported: "a", Local: "a", Line: 1, Reexport: true},
			{Spec: "./a", Imported: "b", Local: "c", Line: 1, Reexport: true},
			{Spec: "./b", Imported: "*", Local: "*", Line: 2, Reexport: true},
		}},
		{"side-effect, dynamic and local exports bind nothing", "import './p';\nconst m = import('./m');\nexport { m };\nexport const z = 1;", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := (Provider{}).Bindings([]byte(c.src)); !reflect.DeepEqual(got, c.want) {
				t.Errorf("Bindings() = %+v, want %+v", got, c.want)
			}
		})
	}
}

func TestUses(t *testing.T) {
	src := "import { load } from './load';\nimport x from './x';\n\nload(1);\nconst v = obj.load;\n// load(2)\nconst s = 'load';\nexport default () => load(x)\n  .then(load);\n"
	if got, want := (Provider{}).Uses([]byte(src), "load"), []int{4, 8, 9}; !reflect.DeepEqual(got, want) {
		t.Errorf("Uses() = %v, want %v", got, want)
	}
	if got := (Provider{}).Uses([]byte(src), "missing"); got != nil {
		t.Errorf("Uses(missing) = %v, want nil", got)
	}
}
