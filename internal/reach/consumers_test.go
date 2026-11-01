package reach

import (
	"io/fs"
	"slices"
	"strings"
	"testing"
)

func TestConsumers(t *testing.T) {
	repo := map[string]string{
		"src/auth.ts":      "export function authorize(c) {\n  if (c.backupCode) {\n    return login(c);\n  }\n  if (!c.totpCode) {\n    checkPassword(c);\n  }\n  return verifyTotp(c);\n}\n",
		"src/refs.ts":      "export function refs(result) {\n  return {\n    credentialId: undefined,\n    uid: result.uid,\n  };\n}\n",
		"src/cancel.ts":    "const select = {\n  credentialId: true,\n};\nexport function cancel(reference) {\n  const { credentialId, uid } = reference;\n  if (credentialId) remove(uid);\n}\n",
		"src/server.ts":    "import { HYDRATION_START } from './constants';\nexport const open = () => `<!--${HYDRATION_START}-->`;\n",
		"src/hydration.ts": "import { HYDRATION_START } from './constants';\nexport function removeNodes(node) {\n  let depth = 0;\n  if (node.data === HYDRATION_START) depth++;\n  return depth;\n}\n",
		"src/constants.ts": "export const HYDRATION_START = '[';\n",
	}
	patches := map[string]string{
		"src/auth.ts":   "--- a/src/auth.ts\n+++ b/src/auth.ts\n@@ -1,1 +1,4 @@\n export function authorize(c) {\n+  if (c.backupCode) {\n+    return login(c);\n+  }\n",
		"src/refs.ts":   "--- a/src/refs.ts\n+++ b/src/refs.ts\n@@ -3,1 +3,1 @@\n-    credentialId: result.credentialId,\n+    credentialId: undefined,\n",
		"src/server.ts": "--- a/src/server.ts\n+++ b/src/server.ts\n@@ -1,1 +1,2 @@\n+import { HYDRATION_START } from './constants';\n export const open = () => `<!--${HYDRATION_START}-->`;\n",
	}
	files := make([]string, 0, len(repo))
	for f := range repo {
		files = append(files, f)
	}
	slices.Sort(files)
	read := func(p string) ([]byte, error) {
		if s, ok := repo[p]; ok {
			return []byte(s), nil
		}
		return nil, fs.ErrNotExist
	}
	got, err := Consumers(ConsumerInput{
		Files: files, ReadFile: read, Patches: patches,
		Changed: []Symbol{{File: "src/auth.ts", Name: "authorize", Line: 1, End: 9}},
	})
	if err != nil {
		t.Fatal(err)
	}

	has := func(changed, file, why, code string) bool {
		return slices.ContainsFunc(got[changed], func(s Snippet) bool {
			return s.File == file && strings.Contains(s.Why, why) && strings.Contains(s.Code, code)
		})
	}
	if !has("src/auth.ts", "src/auth.ts", "rest of changed declaration authorize", "checkPassword(c);") {
		t.Errorf("auth.ts consumers = %+v, want the unchanged rest of authorize", got["src/auth.ts"])
	}
	if !has("src/refs.ts", "src/cancel.ts", "reads credentialId", "const { credentialId, uid } = reference;") {
		t.Errorf("refs.ts consumers = %+v, want cancel.ts reading credentialId", got["src/refs.ts"])
	}
	if !has("src/server.ts", "src/hydration.ts", "uses HYDRATION_START", "if (node.data === HYDRATION_START) depth++;") {
		t.Errorf("server.ts consumers = %+v, want hydration.ts comparing HYDRATION_START", got["src/server.ts"])
	}
	for changed, list := range got {
		for _, s := range list {
			if s.File == "src/cancel.ts" && s.Line <= 2 && s.End >= 2 && !strings.Contains(s.Code, "const {") {
				t.Errorf("%s: a select key that writes credentialId is not a reader: %+v", changed, s)
			}
			if s.File == "src/constants.ts" || s.File == "src/hydration.ts" && s.Line == 1 && s.End == 1 {
				t.Errorf("%s: a declaration or an import is not a use: %+v", changed, s)
			}
			if s.File == "src/refs.ts" && s.Line <= 3 && s.End >= 3 {
				t.Errorf("%s: a line inside the change is not a consumer: %+v", changed, s)
			}
		}
	}
}
