package intentdoc

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestBundleScopeOmissionPreservesCanonicalBytes(t *testing.T) {
	base, err := ParseBundle([]byte(validBundle))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := base.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	request, err := base.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	withEmpty := request
	withEmpty.Tasks = append([]TaskDraft(nil), request.Tasks...)
	withEmpty.Tasks[0].Module = nil
	withEmpty.Tasks[0].Category = nil
	encoded, err := json.Marshal(withEmpty)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseBundle(encoded)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := parsed.Canonical()
	if !bytes.Equal(raw, got) {
		t.Fatalf("omitted scope changed canonical bytes: %s != %s", got, raw)
	}
}

func TestBundleScopeChangesDigestAndRoundTrips(t *testing.T) {
	base, err := ParseBundle([]byte(validBundle))
	if err != nil {
		t.Fatal(err)
	}
	request, _ := base.Snapshot()
	request.Tasks = append([]TaskDraft(nil), request.Tasks...)
	module, category := "backend", "auth/session"
	request.Tasks[0].Module = &module
	request.Tasks[0].Category = &category
	raw, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	scoped, err := ParseBundle(raw)
	if err != nil {
		t.Fatal(err)
	}
	oldDigest, _ := base.Digest()
	newDigest, _ := scoped.Digest()
	if oldDigest == newDigest {
		t.Fatal("scope did not affect digest")
	}
	got, _ := scoped.Snapshot()
	if got.Tasks[0].Module == nil || got.Tasks[0].Category == nil || *got.Tasks[0].Module != "backend" || *got.Tasks[0].Category != "auth/session" {
		t.Fatalf("scope=%+v", got.Tasks[0])
	}
}

func TestBundleScopeRejectsMalformedValues(t *testing.T) {
	for _, scope := range []struct{ module, category string }{
		{"", "auth"}, {"Backend", ""}, {"backend/other", ""}, {"backend", "/auth"},
		{"backend", "auth/../secret"}, {"backend", "auth//secret"}, {"backend", ".hidden"},
		{"backend", "auth/.hidden"}, {"backend", `auth\\secret`},
		{"backend", "evidence/logs"}, {"backend", "auth\x00logs"},
	} {
		request, err := ParseBundle([]byte(validBundle))
		if err != nil {
			t.Fatal(err)
		}
		snapshot, _ := request.Snapshot()
		snapshot.Tasks = append([]TaskDraft(nil), snapshot.Tasks...)
		module, category := scope.module, scope.category
		snapshot.Tasks[0].Module, snapshot.Tasks[0].Category = &module, &category
		raw, err := json.Marshal(snapshot)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := ParseBundle(raw); err == nil {
			t.Errorf("accepted malformed scope module=%q category=%q", scope.module, scope.category)
		}
	}
}

func TestBundleScopeExplicitEmptyFieldsRemainDistinct(t *testing.T) {
	base, err := ParseBundle([]byte(validBundle))
	if err != nil {
		t.Fatal(err)
	}
	request, _ := base.Snapshot()
	request.Tasks = append([]TaskDraft(nil), request.Tasks...)
	empty := ""
	request.Tasks[0].Module = &empty
	request.Tasks[0].Category = &empty
	raw, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseBundle(raw)
	if err != nil {
		t.Fatal(err)
	}
	canonical, _ := parsed.Canonical()
	if !bytes.Contains(canonical, []byte(`"module":""`)) || !bytes.Contains(canonical, []byte(`"category":""`)) {
		t.Fatalf("explicit empty scope was normalized: %s", canonical)
	}
	baseDigest, _ := base.Digest()
	emptyDigest, _ := parsed.Digest()
	if baseDigest == emptyDigest {
		t.Fatal("explicit empty scope did not change digest")
	}
}

func TestBundleScopeRejectsStrictNullAndNonStringTypes(t *testing.T) {
	for _, value := range []string{"null", "1", "[]"} {
		raw := strings.Replace(validBundle, `"id":"TASK-001"`, `"id":"TASK-001","module":`+value, 1)
		if _, err := ParseBundle([]byte(raw)); err == nil {
			t.Errorf("accepted module type %s", value)
		}
	}
	for _, value := range []string{"null", "1", "[]"} {
		raw := strings.Replace(validBundle, `"id":"TASK-001"`, `"id":"TASK-001","category":`+value, 1)
		if _, err := ParseBundle([]byte(raw)); err == nil {
			t.Errorf("accepted category type %s", value)
		}
	}
}
