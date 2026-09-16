package card

import (
	"bytes"
	"reflect"
	"testing"

	"gopkg.in/yaml.v3"
)

func FuzzFrontmatterStatusPreservation(f *testing.F) {
	for _, raw := range []string{
		"---\nid: TASK-1\nstatus: doing\n---\nbody\n",
		"---\nid: TASK-1\n---",
		"---\r\nid: TASK-1\r\n---",
		"---\n  id: TASK-1\n---\n",
		"---\nstatus: in\n  progress\n---\n",
		"---\nstatus: \"su\\u0070erseded\"\n---\n",
		"---\nx: &defaults {status: done, id: TASK-1}\n<<: *defaults\n---\n",
	} {
		f.Add([]byte(raw))
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		doc, err := Parse(raw)
		if err != nil {
			return
		}
		before := doc.View()
		out, changed, err := doc.SetFrontmatterStatus("superseded")
		if !bytes.Equal(raw, doc.Bytes()) || !reflect.DeepEqual(before, doc.View()) {
			t.Fatal("source document mutated")
		}
		if err != nil {
			return
		}
		if changed == bytes.Equal(raw, out) {
			t.Fatal("changed flag disagrees with bytes")
		}
		patched, err := Parse(out)
		if err != nil || patched.View().Status != "superseded" {
			t.Fatalf("invalid output: %q (%v)", out, err)
		}
		originalFM, originalBody, _ := splitFrontmatter(raw)
		patchedFM, patchedBody, _ := splitFrontmatter(out)
		if !bytes.Equal(originalBody, patchedBody) {
			t.Fatal("body changed")
		}
		var originalMeta, patchedMeta map[string]any
		if err := yaml.Unmarshal(originalFM, &originalMeta); err != nil {
			t.Fatal(err)
		}
		if err := yaml.Unmarshal(patchedFM, &patchedMeta); err != nil {
			t.Fatal(err)
		}
		delete(originalMeta, "status")
		delete(patchedMeta, "status")
		originalComparable, err := yaml.Marshal(originalMeta)
		if err != nil {
			t.Fatal(err)
		}
		patchedComparable, err := yaml.Marshal(patchedMeta)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(originalComparable, patchedComparable) {
			t.Fatal("unrelated metadata changed")
		}
		again, changed, err := patched.SetFrontmatterStatus("superseded")
		if err != nil || changed || !bytes.Equal(out, again) {
			t.Fatalf("patch not idempotent: %v", err)
		}
	})
}
