package roleprotocol

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

// mutate decodes raw JSON (numbers kept as literals), applies edit and
// re-encodes it. Key order becomes sorted, which every schema except a
// stored binding accepts.
func mutate(t *testing.T, raw []byte, edit func(m map[string]any)) []byte {
	t.Helper()
	m := decodeMap(t, raw)
	edit(m)
	out, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func decodeMap(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var m map[string]any
	if err := d.Decode(&m); err != nil {
		t.Fatal(err)
	}
	return m
}

func sub(m map[string]any, path ...string) map[string]any {
	for _, p := range path {
		m = m[p].(map[string]any)
	}
	return m
}

// rewriteEvidence edits one evidence document and rebinds every reference to
// its new raw hash, so the edit reaches the document validators rather than
// the key/hash check.
func rewriteEvidence(t *testing.T, support map[string]any, digest string, edit func(doc map[string]any)) {
	t.Helper()
	evidence := sub(support, "evidence")
	raw, err := base64.StdEncoding.DecodeString(evidence[digest].(string))
	if err != nil {
		t.Fatal(err)
	}
	doc := decodeMap(t, raw)
	edit(doc)
	out, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	next := rawDigest(out)
	delete(evidence, digest)
	evidence[next] = base64.StdEncoding.EncodeToString(out)
	acceptance := sub(support, "acceptance")
	for _, key := range []string{"reportSHA256", "reviewSHA256"} {
		if acceptance[key] == digest {
			acceptance[key] = next
		}
	}
	for _, r := range support["routes"].([]any) {
		if route := r.(map[string]any); route["evidenceSHA256"] == digest {
			route["evidenceSHA256"] = next
		}
	}
}

func refuse(t *testing.T, name string, err error) {
	t.Helper()
	if err == nil {
		t.Errorf("%s: accepted, want refusal", name)
	}
}

func TestParseJSONRefusals(t *testing.T) {
	deep := strings.Repeat("[", maxJSONDepth+2) + strings.Repeat("]", maxJSONDepth+2)
	for name, raw := range map[string]string{
		"empty":               ``,
		"whitespace only":     ` `,
		"invalid utf8":        "\"\xff\"",
		"trailing data":       `{} {}`,
		"trailing garbage":    `1x`,
		"duplicate key":       `{"a":1,"a":2}`,
		"escaped duplicate":   `{"a":1,"a":2}`,
		"lone high surrogate": `"\ud800"`,
		"high then non-low":   `"\ud800A"`,
		"lone low surrogate":  `"\udc00"`,
		"control byte":        "\"a\x01\"",
		"bad escape":          `"\x"`,
		"short unicode":       `"\u12"`,
		"bad unicode":         `"\u12zz"`,
		"too deep":            deep,
		"bad literal":         `tru`,
		"leading zero":        `01`,
		"bare minus":          `-`,
		"empty fraction":      `1.`,
		"empty exponent":      `1e`,
		"unterminated string": `"abc`,
		"unterminated object": `{"a":1`,
		"unterminated array":  `[1`,
		"non-string key":      `{1:2}`,
		"missing colon":       `{"a" 1}`,
		"missing comma":       `[1 2]`,
		"trailing comma":      `[1,]`,
		"single quotes":       `'a'`,
	} {
		_, err := parseJSON([]byte(raw), 1024, "x")
		refuse(t, name, err)
	}
	refuse(t, "over limit", func() error { _, err := parseJSON([]byte(`"aaaa"`), 5, "x"); return err }())
	if _, err := parseJSON([]byte(` {"a":[true,false,null,-1.5e+3,"😀\n"]} `), 1024, "x"); err != nil {
		t.Fatalf("valid JSON refused: %v", err)
	}
}

func TestBase64Refusals(t *testing.T) {
	for name, s := range map[string]string{
		"unpadded":         "YQ",
		"non-canonical":    "YR==",
		"embedded newline": "YQ==\n",
		"url alphabet":     "-_8=",
		"bad char":         "Y*==",
	} {
		_, err := decodeBase64(s, 64, "x")
		refuse(t, name, err)
	}
	refuse(t, "over limit", func() error { _, err := decodeBase64("YWFh", 2, "x"); return err }())
}

func TestSelectorRefusals(t *testing.T) {
	for name, raw := range map[string]string{
		"bad contract":     `{"contract":"task-manager:role-selection:v2","baseProfile":"section-9","amendment":"none"}`,
		"bad base":         `{"contract":"task-manager:role-selection:v1","baseProfile":"section-11","amendment":"none"}`,
		"bad amendment":    `{"contract":"task-manager:role-selection:v1","baseProfile":"section-9","amendment":"section-10"}`,
		"missing field":    `{"contract":"task-manager:role-selection:v1","baseProfile":"section-9"}`,
		"unknown field":    `{"contract":"task-manager:role-selection:v1","baseProfile":"section-9","amendment":"none","x":1}`,
		"non-string value": `{"contract":"task-manager:role-selection:v1","baseProfile":9,"amendment":"none"}`,
		"array":            `[]`,
	} {
		_, err := ParseSelector([]byte(raw))
		refuse(t, name, err)
	}
	reordered := `{"baseProfile":"section-9","contract":"task-manager:role-selection:v1","amendment":"none"}`
	if _, err := ParseSelector([]byte(reordered)); err != nil {
		t.Fatalf("selector input accepts any key order: %v", err)
	}
	_, err := canonicalSelector([]byte(reordered))
	refuse(t, "history selector not canonical", err)
	_, err = canonicalSelector([]byte(reordered[:0] + `{"contract":"task-manager:role-selection:v1","baseProfile":"section-9","amendment":"none"}` + "\n"))
	refuse(t, "history selector with LF", err)
}

func TestBindingRefusals(t *testing.T) {
	d := strings.Repeat("a", 64)
	for name, raw := range map[string]string{
		"key order":            `{"policyDigest":"` + d + `","kind":"unselected","selectionDigest":null}`,
		"selected with null":   `{"kind":"selected","policyDigest":"` + d + `","selectionDigest":null}`,
		"unselected with sel":  `{"kind":"unselected","policyDigest":"` + d + `","selectionDigest":"` + d + `"}`,
		"uppercase digest":     `{"kind":"unselected","policyDigest":"` + strings.ToUpper(d) + `","selectionDigest":null}`,
		"unknown kind":         `{"kind":"implicit","policyDigest":"` + d + `","selectionDigest":null}`,
		"missing selection":    `{"kind":"unselected","policyDigest":"` + d + `"}`,
		"short policy digest":  `{"kind":"unselected","policyDigest":"aa","selectionDigest":null}`,
		"selection not string": `{"kind":"selected","policyDigest":"` + d + `","selectionDigest":1}`,
	} {
		n, err := parseJSON([]byte(raw), maxBindingBytes, "b")
		if err == nil {
			_, err = decodeBinding(n, "b")
		}
		refuse(t, name, err)
	}
}

func TestHistoryRefusals(t *testing.T) {
	f := loadFixture(t)
	h := f.boundHistory()
	pk, sk, bk := sortedKeys(h.Policies), sortedKeys(h.Selections), sortedKeys(h.Bindings)
	good := historyJSON(h, pk, sk, bk)
	legacy, _, _, _ := LegacySourceHistory([][]byte{f.policy}, f.policyHash)
	bogus := strings.Repeat("0", 64)

	_, err := ParseHistory(good, HistoryLegacySource)
	refuse(t, "legacy mode with selections", err)
	_, err = ParseHistory(historyJSON(legacy, pk, nil, sortedKeys(legacy.Bindings)), HistoryBound)
	refuse(t, "bound mode without selections", err)
	_, err = ParseHistory(good, HistoryMode(0))
	refuse(t, "unknown mode", err)
	_, err = ParseHistory(bytes.Replace(good, []byte(`"version":1`), []byte(`"version":2`), 1), HistoryBound)
	refuse(t, "version 2", err)
	_, err = ParseHistory(bytes.Replace(good, []byte(`"version":1`), []byte(`"version":1,"extra":0`), 1), HistoryBound)
	refuse(t, "unknown field", err)
	_, err = ParseHistory(bytes.Replace(good, []byte(`"selections":{`), []byte(`"selections":[{`), 1), HistoryBound)
	refuse(t, "selections not object", err)

	cases := map[string]func(h *History){
		"policy key mismatch": func(h *History) { h.Policies[bogus] = h.Policies[f.policyHash] },
		"non-canonical policy": func(h *History) {
			raw := append(append([]byte(nil), f.policy...), '\n')
			h.Policies[rawDigest(raw)] = raw
		},
		"selection key mismatch": func(h *History) { h.Selections[bogus] = f.selRaw[0] },
		"binding key mismatch": func(h *History) {
			h.Bindings[bogus] = h.Bindings[f.bindDigest[0]]
		},
		"orphan selection": func(h *History) { delete(h.Bindings, f.bindDigest[1]) },
		"missing policy": func(h *History) {
			b := UnselectedBinding(bogus)
			d, _ := b.Digest()
			h.Bindings[d] = b
		},
		"missing selection": func(h *History) { delete(h.Selections, f.selDigest[0]) },
		"no bindings":       func(h *History) { h.Bindings = map[string]Binding{} },
		"no policies":       func(h *History) { h.Policies = map[string][]byte{} },
	}
	for name, edit := range cases {
		c := f.boundHistory()
		edit(&c)
		refuse(t, name, c.Validate(HistoryBound))
	}
	withSelected := f.boundHistory()
	withSelected.Selections = map[string][]byte{}
	refuse(t, "legacy with selected binding", withSelected.Validate(HistoryLegacySource))
	orphanPolicy := f.boundHistory()
	other := append(append([]byte(nil), f.policy[:len(f.policy)-1]...), ' ', '}')
	orphanPolicy.Policies[rawDigest(other)] = other
	refuse(t, "orphan or non-canonical extra policy", orphanPolicy.Validate(HistoryBound))

	_, _, _, err = LegacySourceHistory([][]byte{f.policy}, bogus)
	refuse(t, "legacy active policy not in history", err)
	_, _, _, err = LegacySourceHistory(nil, f.policyHash)
	refuse(t, "legacy without policies", err)
	refuse(t, "current binding absent", h.ValidateCurrent(bogus, f.policyHash, false))
	refuse(t, "current binding other policy", h.ValidateCurrent(f.bindDigest[1], bogus, false))
	altered := f.boundHistory()
	altered.Selections = map[string][]byte{}
	refuse(t, "extension drops selections", ValidateHistoryExtension(f.boundHistory(), altered))
}

func TestSupportRefusals(t *testing.T) {
	f := loadFixture(t)
	raw := []byte(f.v.Support.Input)
	report := f.support.Acceptance.ReportSHA256
	review := f.support.Acceptance.ReviewSHA256
	route := f.support.Routes[0].EvidenceSHA256
	cases := map[string]func(m map[string]any){
		"version 2":        func(m map[string]any) { m["version"] = json.Number("2") },
		"protocol 0":       func(m map[string]any) { m["protocol"] = json.Number("0") },
		"unknown field":    func(m map[string]any) { m["extra"] = true },
		"missing field":    func(m map[string]any) { delete(m, "acceptance") },
		"bad commit oid":   func(m map[string]any) { m["sourceCommit"] = "ABC" },
		"empty profiles":   func(m map[string]any) { m["profiles"] = []any{} },
		"unknown profile":  func(m map[string]any) { m["profiles"] = []any{"section-11/none"} },
		"unsorted profile": func(m map[string]any) { m["profiles"] = []any{"section-9/none", "section-10/none"} },
		"duplicate profile": func(m map[string]any) {
			m["profiles"] = []any{"section-10/none", "section-10/none"}
		},
		"route missing": func(m map[string]any) { m["routes"] = m["routes"].([]any)[1:] },
		"route order": func(m map[string]any) {
			r := m["routes"].([]any)
			r[0], r[1] = r[1], r[0]
		},
		"refused entry allowed": func(m map[string]any) {
			for _, r := range m["routes"].([]any) {
				if r.(map[string]any)["entry"] == "store.bundle" {
					r.(map[string]any)["decision"] = "allow"
				}
			}
		},
		"bad decision": func(m map[string]any) { m["routes"].([]any)[0].(map[string]any)["decision"] = "maybe" },
		"empty command": func(m map[string]any) {
			sub(m, "acceptance")["command"] = ""
		},
		"command over limit": func(m map[string]any) {
			sub(m, "acceptance")["command"] = strings.Repeat("c", maxCommandBytes+1)
		},
		"report command differs": func(m map[string]any) { sub(m, "acceptance")["command"] = "other" },
		"evidence missing":       func(m map[string]any) { delete(sub(m, "evidence"), review) },
		"evidence unreferenced": func(m map[string]any) {
			extra := []byte("x")
			sub(m, "evidence")[rawDigest(extra)] = base64.StdEncoding.EncodeToString(extra)
		},
		"evidence key mismatch": func(m map[string]any) {
			e := sub(m, "evidence")
			e[review] = e[report]
		},
		"evidence not base64": func(m map[string]any) { sub(m, "evidence")[review] = "!!" },
		"evidence not object": func(m map[string]any) { m["evidence"] = []any{} },
		"review verdict FAIL": func(m map[string]any) {
			rewriteEvidence(t, m, review, func(d map[string]any) { d["verdict"] = "FAIL" })
		},
		"review grade economy": func(m map[string]any) {
			rewriteEvidence(t, m, review, func(d map[string]any) { d["grade"] = "economy" })
		},
		"review other report": func(m map[string]any) {
			rewriteEvidence(t, m, review, func(d map[string]any) { d["reportSHA256"] = strings.Repeat("0", 64) })
		},
		"review other artifact": func(m map[string]any) {
			rewriteEvidence(t, m, review, func(d map[string]any) { d["artifactSHA256"] = strings.Repeat("0", 64) })
		},
		"route evidence exit 1": func(m map[string]any) {
			rewriteEvidence(t, m, route, func(d map[string]any) { d["exitCode"] = json.Number("1") })
		},
		"route evidence not passed": func(m map[string]any) {
			rewriteEvidence(t, m, route, func(d map[string]any) {
				d["entries"].([]any)[0].(map[string]any)["passed"] = false
			})
		},
		"route evidence other decision": func(m map[string]any) {
			rewriteEvidence(t, m, route, func(d map[string]any) {
				d["entries"].([]any)[0].(map[string]any)["decision"] = "allow"
			})
		},
		"route evidence missing profile": func(m map[string]any) {
			rewriteEvidence(t, m, route, func(d map[string]any) { d["entries"] = d["entries"].([]any)[1:] })
		},
		"route evidence extra field": func(m map[string]any) {
			rewriteEvidence(t, m, route, func(d map[string]any) { d["note"] = "x" })
		},
		"route evidence other source": func(m map[string]any) {
			rewriteEvidence(t, m, route, func(d map[string]any) { d["sourceTree"] = strings.Repeat("1", 40) })
		},
	}
	if f.support.Routes[0].Decision != "refuse" {
		t.Fatal("fixture assumption: first route refuses")
	}
	for name, edit := range cases {
		_, err := ParseSupport(mutate(t, raw, edit))
		refuse(t, name, err)
	}
	_, err := ParseSupport(append(raw, make([]byte, maxSupportBytes)...))
	refuse(t, "over 256KiB", err)
	if _, err := ParseSupport(mutate(t, raw, func(map[string]any) {})); err != nil {
		t.Fatalf("re-encoded support refused: %v", err)
	}
}

func TestAckRefusals(t *testing.T) {
	f := loadFixture(t)
	raw := []byte(f.v.Ack.Input)
	d := strings.Repeat("a", 64)
	cases := map[string]func(m map[string]any){
		"version 2":            func(m map[string]any) { m["version"] = json.Number("2") },
		"unknown field":        func(m map[string]any) { m["note"] = "x" },
		"empty operator":       func(m map[string]any) { m["operator"] = "" },
		"long operator":        func(m map[string]any) { m["operator"] = strings.Repeat("o", maxOperatorBytes+1) },
		"control in operator":  func(m map[string]any) { m["operator"] = "a\tb" },
		"offset time":          func(m map[string]any) { m["issuedAt"] = "2026-10-20T00:00:00+00:00" },
		"fractional time":      func(m map[string]any) { m["issuedAt"] = "2026-10-20T00:00:00.0Z" },
		"invalid date":         func(m map[string]any) { m["issuedAt"] = "2026-02-30T00:00:00Z" },
		"expiry equals issue":  func(m map[string]any) { m["expiresAt"] = m["issuedAt"] },
		"expiry before issue":  func(m map[string]any) { m["expiresAt"] = "2026-10-19T23:59:59Z" },
		"lifetime over 24h":    func(m map[string]any) { m["expiresAt"] = "2026-10-21T00:00:01Z" },
		"relative root":        func(m map[string]any) { m["root"] = "fixture/repo"; m["participants"] = []any{"fixture/repo"} },
		"unclean root":         func(m map[string]any) { m["root"] = "/fixture/repo/"; m["participants"] = []any{"/fixture/repo/"} },
		"dot-dot root":         func(m map[string]any) { m["root"] = "/fixture/../repo"; m["participants"] = []any{"/fixture/../repo"} },
		"dot board":            func(m map[string]any) { m["board"] = "." },
		"empty board":          func(m map[string]any) { m["board"] = "" },
		"escaping board":       func(m map[string]any) { m["board"] = "../tasks" },
		"absolute board":       func(m map[string]any) { m["board"] = "/tasks" },
		"git board":            func(m map[string]any) { m["board"] = "a/.GIT/b" },
		"backslash board":      func(m map[string]any) { m["board"] = `a\b` },
		"trailing slash board": func(m map[string]any) { m["board"] = "tasks/" },
		"bad head":             func(m map[string]any) { m["head"] = "4444" },
		"bad snapshot":         func(m map[string]any) { m["snapshot"] = "x" },
		"participants other":   func(m map[string]any) { m["participants"] = []any{"/other"} },
		"participants two": func(m map[string]any) {
			m["participants"] = []any{"/fixture/repo", "/fixture/repo"}
		},
		"unsupportedStopped false": func(m map[string]any) { m["unsupportedStopped"] = false },
		"inflightDrained missing":  func(m map[string]any) { delete(m, "inflightDrained") },
		"supportAccepted string":   func(m map[string]any) { m["supportAccepted"] = "true" },
		"target binding mismatch": func(m map[string]any) {
			sub(m, "target")["selectionDigest"] = d
		},
		"target extra field":  func(m map[string]any) { sub(m, "target")["journal"] = d },
		"claims true no hash": func(m map[string]any) { sub(m, "original", "claims")["sha256"] = nil },
		"claims false with hash": func(m map[string]any) {
			sub(m, "original", "claims")["exists"] = false
		},
		"original missing ids": func(m map[string]any) { delete(sub(m, "original"), "ids") },
		"legacy with support":  func(m map[string]any) { sub(m, "source")["supportDigest"] = d },
		"bound with null": func(m map[string]any) {
			sub(m, "source")["kind"] = SourceBound
		},
		"source unknown kind": func(m map[string]any) { sub(m, "source")["kind"] = "legacy" },
		"source bad authority": func(m map[string]any) {
			sub(m, "source")["authorityId"] = strings.Repeat("6", 31)
		},
		"source missing adoption": func(m map[string]any) { delete(sub(m, "source"), "adoptionDigest") },
	}
	for name, edit := range cases {
		_, err := ParseAck(mutate(t, raw, edit))
		refuse(t, name, err)
	}
	_, err := ParseAck(append(raw, make([]byte, maxAckBytes)...))
	refuse(t, "over 64KiB", err)

	bound := mutate(t, raw, func(m map[string]any) {
		s := sub(m, "source")
		s["kind"], s["supportDigest"], s["adoptionDigest"] = SourceBound, d, d
	})
	if _, err := ParseAck(bound); err != nil {
		t.Fatalf("bound source refused: %v", err)
	}
	nested := mutate(t, raw, func(m map[string]any) { m["board"] = "boards/main" })
	if _, err := ParseAck(nested); err != nil {
		t.Fatalf("nested board refused: %v", err)
	}
}

func TestAckWindow(t *testing.T) {
	f := loadFixture(t)
	a := f.ack
	if err := a.CheckWindow(a.IssuedAt); err != nil {
		t.Fatalf("issuedAt is inside the window: %v", err)
	}
	if err := a.CheckWindow(a.ExpiresAt.Add(-1)); err != nil {
		t.Fatalf("just before expiry is inside the window: %v", err)
	}
	refuse(t, "at expiry", a.CheckWindow(a.ExpiresAt))
	refuse(t, "before issue", a.CheckWindow(a.IssuedAt.Add(-1)))
	if err := a.CheckWindow(time.Time{}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("an unreadable clock must be unavailable, got %v", err)
	}
}
