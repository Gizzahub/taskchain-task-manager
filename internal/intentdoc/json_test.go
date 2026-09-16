package intentdoc

import (
	"bytes"
	"strings"
	"testing"
)

func TestStrictJSONFailures(t *testing.T) {
	for name, raw := range map[string]string{
		"empty": "", "array": "[]", "boolean": "true", "null": "null",
		"duplicate":         strings.Replace(validIntent, `"revision":1`, `"revision":1,"revision":2`, 1),
		"escaped duplicate": strings.Replace(validIntent, `"revision":1`, `"revision":1,"\u0072evision":2`, 1),
		"case":              strings.Replace(validIntent, `"revision":1`, `"Revision":1`, 1),
		"unknown":           strings.Replace(validIntent, `"revision":1`, `"revision":1,"extra":true`, 1),
		"nested unknown":    strings.Replace(validIntent, `"key":"verified"`, `"key":"verified","extra":"x"`, 1),
		"nested duplicate":  strings.Replace(validIntent, `"key":"verified"`, `"key":"verified","key":"other"`, 1),
		"missing":           strings.Replace(validIntent, `"constraints":[],`, "", 1),
		"float":             strings.Replace(validIntent, `"revision":1`, `"revision":1.0`, 1),
		"exponent":          strings.Replace(validIntent, `"revision":1`, `"revision":1e0`, 1),
		"negative zero":     strings.Replace(validIntent, `"revision":1`, `"revision":-0`, 1),
		"overflow":          strings.Replace(validIntent, `"revision":1`, `"revision":4294967296`, 1),
		"string number":     strings.Replace(validIntent, `"revision":1`, `"revision":"1"`, 1),
		"list type":         strings.Replace(validIntent, `"constraints":[]`, `"constraints":{}`, 1),
		"list item type":    strings.Replace(validIntent, `"constraints":[]`, `"constraints":[false]`, 1),
		"null list":         strings.Replace(validIntent, `"constraints":[]`, `"constraints":null`, 1),
		"unpaired high":     strings.Replace(validIntent, "Synthetic goal", `\ud800`, 1),
		"unpaired low":      strings.Replace(validIntent, "Synthetic goal", `\udc00`, 1),
		"high not low":      strings.Replace(validIntent, "Synthetic goal", `\ud800\u0041`, 1),
		"bad unicode":       strings.Replace(validIntent, "Synthetic goal", `\uZZZZ`, 1),
		"two objects":       validIntent + validIntent,
		"trailing":          validIntent + " trailing",
		"deep":              strings.Repeat("[", 18) + "0" + strings.Repeat("]", 18),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse([]byte(raw)); err == nil {
				t.Fatal("malformed JSON accepted")
			}
		})
	}
	if _, err := Parse(append([]byte(validIntent), 0xff)); err == nil {
		t.Fatal("invalid UTF-8 accepted")
	}
	for _, title := range []string{`\ud83d\ude80`, `literal \\ud800`, "실제 목표", "replacement �"} {
		raw := strings.Replace(validIntent, "Synthetic goal", title, 1)
		if _, err := Parse([]byte(raw)); err != nil {
			t.Fatalf("valid Unicode %s: %v", title, err)
		}
	}
}

func TestDocumentSizeAndCanonicalExpansion(t *testing.T) {
	raw := append([]byte(validIntent), bytes.Repeat([]byte(" "), MaxDocumentBytes-len(validIntent))...)
	if _, err := Parse(raw); err != nil {
		t.Fatalf("exact input limit: %v", err)
	}
	if _, err := Parse(append(raw, ' ')); err == nil {
		t.Fatal("oversize accepted")
	}
	// HTML escaping expands every '<' to six canonical bytes. Each individual
	// field remains below its limit and the input stays below the document cap.
	item := `"` + strings.Repeat("<", 12000) + `"`
	expanded := strings.Replace(validIntent, `"constraints":[]`, `"constraints":[`+strings.Join([]string{item, item, item, item}, ",")+`]`, 1)
	if _, err := Parse([]byte(expanded)); err == nil || !strings.Contains(err.Error(), "canonical") {
		t.Fatalf("canonical expansion: %v", err)
	}
}

func FuzzDocumentRoundTrip(f *testing.F) {
	f.Add([]byte(validIntent))
	f.Add([]byte(`{}`))
	f.Fuzz(func(t *testing.T, raw []byte) {
		doc, err := Parse(raw)
		if err != nil {
			return
		}
		canonical, err := doc.Canonical()
		if err != nil {
			t.Fatal(err)
		}
		again, err := Parse(canonical)
		if err != nil {
			t.Fatal(err)
		}
		roundtrip, err := again.Canonical()
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(canonical, roundtrip) {
			t.Fatal("canonical is not stable")
		}
	})
}
