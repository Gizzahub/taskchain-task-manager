package intentdoc

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

const validBundle = `{"schemaVersion":1,"kind":"task-bundle","requestId":"0123456789abcdef0123456789abcdef","batch":{"id":"BATCH-abcdef0123456789abcdef0123456789","revision":3,"intent":{"id":"INTENT-0123456789abcdef0123456789abcdef","revision":1,"digest":"04edcffd5de255854f0bdd4f68cc24e21ee8ad1d3a39b4645382f0d9066ad508"},"gap":"Publish the reviewed release","constraints":[],"authorizationRefs":[]},"tasks":[{"key":"first","id":"TASK-001","title":"Prepare release","dependsOn":[],"template":{"validationConfig":"{}","type":"task","priority":"normal","summary":"Prepare the release","criteria":["The release is prepared."]}},{"key":"second","id":"","title":"Publish release","dependsOn":[{"taskId":"","key":"first"},{"taskId":"TASK-9","key":""}]}]}`

func TestParseBundleCanonicalSnapshotAndDigest(t *testing.T) {
	doc, err := ParseBundle([]byte(validBundle))
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := doc.Canonical()
	if err != nil || len(canonical) == 0 || !bytes.Equal(canonical, []byte(validBundle)) {
		t.Fatalf("canonical=%s err=%v", canonical, err)
	}
	copyBytes := append([]byte(nil), canonical...)
	canonical[0] = 'x'
	again, _ := doc.Canonical()
	if !bytes.Equal(again, copyBytes) {
		t.Fatal("canonical bytes are not owned")
	}
	digest, err := doc.Digest()
	if err != nil || len(digest) != 64 {
		t.Fatalf("digest=%q err=%v", digest, err)
	}
	snapshot, err := doc.Snapshot()
	if err != nil || snapshot.Kind != "task-bundle" || snapshot.Batch.Revision != 3 || snapshot.Tasks[0].ID != "TASK-001" {
		t.Fatalf("snapshot=%+v err=%v", snapshot, err)
	}
	snapshot.Tasks[0].Title = "mutated"
	againSnapshot, _ := doc.Snapshot()
	if againSnapshot.Tasks[0].Title == "mutated" {
		t.Fatal("snapshot mutation leaked")
	}
}

func TestParseBundleRejectsStrictShapeIdentityAndDependencies(t *testing.T) {
	cases := map[string]string{
		"unknown":                         strings.Replace(validBundle, `}`, `,"extra":1}`, 1),
		"duplicate":                       strings.Replace(validBundle, `"schemaVersion":1`, `"schemaVersion":1,"schemaVersion":1`, 1),
		"null":                            strings.Replace(validBundle, `"tasks":[`, `"tasks":null`, 1),
		"missing request id":              strings.Replace(validBundle, `,"requestId":"0123456789abcdef0123456789abcdef"`, "", 1),
		"missing dependency field":        strings.Replace(validBundle, `{"taskId":"","key":"first"}`, `{"key":"first"}`, 1),
		"bad field case":                  strings.Replace(validBundle, `"requestId"`, `"RequestId"`, 1),
		"unpaired surrogate":              strings.Replace(validBundle, "Prepare release", `\ud800`, 1),
		"null template":                   strings.Replace(validBundle, `"template":{"validationConfig":"{}","type":"task","priority":"normal","summary":"Prepare the release","criteria":["The release is prepared."]}`, `"template":null`, 1),
		"bad request id":                  strings.Replace(validBundle, "0123456789abcdef0123456789abcdef", "0123456789ABCDEF0123456789abcdef", 1),
		"bad task id":                     strings.Replace(validBundle, "TASK-001", "PLAN-1", 1),
		"alias":                           strings.Replace(validBundle, `"key":"second","id":""`, `"key":"second","id":"TASK-1"`, 1),
		"both dependency refs":            strings.Replace(validBundle, `{"taskId":"","key":"first"}`, `{"taskId":"TASK-9","key":"first"}`, 1),
		"unknown dependency key":          strings.Replace(validBundle, `{"taskId":"","key":"first"}`, `{"taskId":"","key":"missing"}`, 1),
		"cross-form duplicate dependency": strings.Replace(validBundle, `{"taskId":"","key":"first"},{"taskId":"TASK-9","key":""}`, `{"taskId":"","key":"first"},{"taskId":"TASK-001","key":""}`, 1),
		"self dependency key":             strings.Replace(validBundle, `"dependsOn":[]`, `"dependsOn":[{"taskId":"","key":"first"}]`, 1),
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseBundle([]byte(raw)); err == nil {
				t.Fatal("invalid bundle accepted")
			}
		})
	}
	cycle := strings.Replace(validBundle, `"dependsOn":[]`, `"dependsOn":[{"taskId":"","key":"second"}]`, 1)
	if _, err := ParseBundle([]byte(cycle)); err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("cycle error=%v", err)
	}
}

func TestParseBundleRejectsMalformedAndTemplateBounds(t *testing.T) {
	for _, raw := range []string{"", "null", validBundle + " trailing", strings.Replace(validBundle, `"validationConfig":"{}"`, `"validationConfig":""`, 1), strings.Replace(validBundle, `"criteria":["The release is prepared."]`, `"criteria":[]`, 1), strings.Repeat("x", MaxDocumentBytes+1)} {
		if _, err := ParseBundle([]byte(raw)); err == nil {
			t.Fatalf("malformed bundle accepted: %q", raw[:min(len(raw), 24)])
		}
	}
	noTemplate := strings.Replace(validBundle, `,"template":{"validationConfig":"{}","type":"task","priority":"normal","summary":"Prepare the release","criteria":["The release is prepared."]}`, "", 1)
	noTemplateDoc, err := ParseBundle([]byte(noTemplate))
	if err != nil {
		t.Fatalf("optional template rejected: %v", err)
	}
	canonical, _ := noTemplateDoc.Canonical()
	if _, err := ParseBundle(canonical); err != nil {
		t.Fatalf("template omission did not round-trip: %v", err)
	}
	withNewline := strings.Replace(validBundle, `"validationConfig":"{}"`, `"validationConfig":"{}\r\n"`, 1)
	newlineDoc, err := ParseBundle([]byte(withNewline))
	if err != nil {
		t.Fatalf("trailing newline config rejected: %v", err)
	}
	baseDoc, _ := ParseBundle([]byte(validBundle))
	baseDigest, _ := baseDoc.Digest()
	newlineDigest, _ := newlineDoc.Digest()
	if baseDigest == newlineDigest {
		t.Fatal("inline config bytes were not preserved in digest")
	}
}

func TestZeroBundleDocumentFailsClosed(t *testing.T) {
	var zero BundleDocument
	if _, err := zero.Canonical(); err == nil {
		t.Fatal("zero canonical accepted")
	}
	if _, err := zero.Digest(); err == nil {
		t.Fatal("zero digest accepted")
	}
	if _, err := zero.Snapshot(); err == nil {
		t.Fatal("zero snapshot accepted")
	}
}

func TestBundleJSONRoundTripRetainsTaskOrderAndIDSpelling(t *testing.T) {
	doc, err := ParseBundle([]byte(validBundle))
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := doc.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Tasks[0].ID != "TASK-001" || snapshot.Tasks[1].Key != "second" {
		t.Fatalf("snapshot order/spelling lost: %+v", snapshot.Tasks)
	}
	if _, err := json.Marshal(snapshot); err != nil {
		t.Fatal(err)
	}
	canonical, err := doc.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	reparsed, err := ParseBundle(canonical)
	if err != nil {
		t.Fatal(err)
	}
	recanonical, _ := reparsed.Canonical()
	if !bytes.Equal(canonical, recanonical) {
		t.Fatal("canonical bundle did not round-trip")
	}
	digest, _ := doc.Digest()
	reparsedDigest, _ := reparsed.Digest()
	if digest != reparsedDigest {
		t.Fatal("bundle digest changed on round-trip")
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func FuzzParseBundle(f *testing.F) {
	f.Add([]byte(validBundle))
	f.Add([]byte(`{"schemaVersion":1,"kind":"task-bundle"}`))
	f.Add([]byte("{\"kind\":\"task-bundle\",\"tasks\":["))
	f.Fuzz(func(t *testing.T, raw []byte) {
		doc, err := ParseBundle(raw)
		if err != nil {
			return
		}
		canonical, err := doc.Canonical()
		if err != nil {
			t.Fatal(err)
		}
		roundTrip, err := ParseBundle(canonical)
		if err != nil {
			t.Fatal(err)
		}
		other, err := roundTrip.Canonical()
		if err != nil || !bytes.Equal(canonical, other) {
			t.Fatalf("canonical round trip failed: %v", err)
		}
	})
}
