package card

import (
	"bytes"
	"testing"
)

func TestSetFrontmatterStatusPatchesScalarAndPreservesSource(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"plain", "---\nstatus: doing # keep\nunknown: [a, b] # comment\n---\n# body\n", "---\nstatus: superseded # keep\nunknown: [a, b] # comment\n---\n# body\n"},
		{"single quoted", "---\n'status': 'done'\ntitle: keep\n---\nbody\n", "---\n'status': 'superseded'\ntitle: keep\n---\nbody\n"},
		{"double quoted", "---\nstatus: \"done\"\n# header comment\n---\nbody", "---\nstatus: \"superseded\"\n# header comment\n---\nbody"},
		{"crlf", "---\r\nstatus: pending\r\ntitle: keep\r\n---\r\nbody\r\n", "---\r\nstatus: superseded\r\ntitle: keep\r\n---\r\nbody\r\n"},
		{"trailing spaces", "---\nstatus: doing   \ntitle: keep\n---\nbody\n", "---\nstatus: superseded   \ntitle: keep\n---\nbody\n"},
		{"escaped double quote", "---\nstatus: \"do\\\"ne\"\ntitle: keep\n---\nbody\n", "---\nstatus: \"superseded\"\ntitle: keep\n---\nbody\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, err := Parse([]byte(tt.in))
			if err != nil {
				t.Fatal(err)
			}
			got, changed, err := d.SetFrontmatterStatus("superseded")
			if err != nil || !changed {
				t.Fatalf("patch changed=%v err=%v", changed, err)
			}
			if string(got) != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
			if string(d.Bytes()) != tt.in {
				t.Fatal("document source mutated")
			}
		})
	}
}

func TestSetFrontmatterStatusInsertsBeforeDelimiter(t *testing.T) {
	for _, tt := range []struct {
		name string
		in   string
		want string
	}{
		{"newline", "---\ntitle: keep\n---\nbody\n", "---\ntitle: keep\nstatus: superseded\n---\nbody\n"},
		{"no final header newline", "---\ntitle: keep\n---\nbody", "---\ntitle: keep\nstatus: superseded\n---\nbody"},
		{"crlf", "---\r\ntitle: keep\r\n---\r\nbody\r\n", "---\r\ntitle: keep\r\nstatus: superseded\r\n---\r\nbody\r\n"},
		{"indented mapping", "---\n  title: keep\n---\nbody\n", "---\n  title: keep\n  status: superseded\n---\nbody\n"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			d, err := Parse([]byte(tt.in))
			if err != nil {
				t.Fatal(err)
			}
			got, changed, err := d.SetFrontmatterStatus("superseded")
			if err != nil || !changed || string(got) != tt.want {
				t.Fatalf("got=%q changed=%v err=%v", got, changed, err)
			}
		})
	}
}

func TestSetFrontmatterStatusIsIdempotentAndRestricted(t *testing.T) {
	d, err := Parse([]byte("---\nstatus: 'superseded'\ntitle: keep\n---\nbody\n"))
	if err != nil {
		t.Fatal(err)
	}
	got, changed, err := d.SetFrontmatterStatus("superseded")
	if err != nil || changed || !bytes.Equal(got, d.Bytes()) {
		t.Fatalf("got=%q changed=%v err=%v", got, changed, err)
	}
	if _, _, err := d.SetFrontmatterStatus("done"); err == nil {
		t.Fatal("accepted unsupported status")
	}
}

func TestSetFrontmatterStatusRejectsAmbiguousYAML(t *testing.T) {
	for _, raw := range []string{
		"---\n{status: done}\n---\nbody\n",
		"---\nstatus: &old done\n---\nbody\n",
		"---\nstatus: *old\n---\nbody\n",
		"---\nstatus: |\n  done\n---\nbody\n",
		"---\nstatus: [done]\n---\nbody\n",
		"---\n? [status]\n: done\n---\nbody\n",
		"---\nstatus: 'done\n---\nbody\n",
		"---\nstatus: in\n  progress\n---\nbody\n",
		"---\n&root\nstatus: done\n---\nbody\n",
	} {
		d, err := Parse([]byte(raw))
		if err != nil {
			continue
		}
		before := d.Bytes()
		if _, _, err := d.SetFrontmatterStatus("superseded"); err == nil {
			t.Fatalf("accepted unsupported YAML: %q", raw)
		}
		if !bytes.Equal(before, d.Bytes()) {
			t.Fatal("document source mutated after rejection")
		}
	}
}
