package taskstore

import (
	"encoding/json"
	"strings"
	"testing"
)

func bundleRecordFixture(t *testing.T) bundleRecord {
	t.Helper()
	req, intent := bundleFixture(t)
	doc := parsedBundle(t, req)
	base := idLedger{SchemaVersion: 2, Reserved: []string{}}
	prepared, err := prepareTaskBundle(doc, nil, base, intent)
	if err != nil {
		t.Fatal(err)
	}
	request, err := doc.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	intentRaw, err := intent.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	baseRaw, err := ledgerBytes(base)
	if err != nil {
		t.Fatal(err)
	}
	targetRaw, err := ledgerBytes(prepared.Ledger)
	if err != nil {
		t.Fatal(err)
	}
	batchRaw, err := prepared.Batch.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	policyDigest, err := currentPolicy().Digest()
	if err != nil {
		t.Fatal(err)
	}
	record := bundleRecord{Status: "pending", RequestID: req.RequestID, RequestDigest: prepared.RequestDigest,
		Request: request, Intent: intentRaw, OriginalIDs: append([]byte(nil), baseRaw...), BaseIDs: baseRaw,
		TargetIDs: targetRaw, Batch: batchRaw, PolicyDigest: policyDigest, Namespace: "", Cards: []bundleCard{}}
	for i, item := range prepared.Cards {
		record.Cards = append(record.Cards, bundleCard{Key: req.Tasks[i].Key, ID: item.Entry.Card.ID, Path: item.Entry.Path, Raw: item.Raw})
	}
	if err := validateBundleRecord(record); err != nil {
		t.Fatalf("invalid fixture: %v", err)
	}
	return record
}

func TestBundleRecordRejectsInconsistentEvidence(t *testing.T) {
	t.Parallel()
	changes := map[string]func(*bundleRecord){
		"request-id":        func(r *bundleRecord) { r.RequestID = strings.Repeat("b", 32) },
		"request-digest":    func(r *bundleRecord) { r.RequestDigest = strings.Repeat("0", 64) },
		"request-canonical": func(r *bundleRecord) { r.Request = append(r.Request, ' ') },
		"intent-canonical":  func(r *bundleRecord) { r.Intent = append(r.Intent, ' ') },
		"intent-reference": func(r *bundleRecord) {
			r.Intent = []byte(strings.Replace(string(r.Intent), "0123456789abcdef0123456789abcdef", "abcdef0123456789abcdef0123456789", 1))
		},
		"base-canonical":       func(r *bundleRecord) { r.BaseIDs = append(r.BaseIDs, ' ') },
		"original-reservation": func(r *bundleRecord) { r.OriginalIDs = []byte(`{"schemaVersion":2,"reserved":["TASK-99"]}`) },
		"target-reservation":   func(r *bundleRecord) { r.TargetIDs = append([]byte(nil), r.BaseIDs...) },
		"card-key":             func(r *bundleRecord) { r.Cards[0].Key = "other" },
		"card-id":              func(r *bundleRecord) { r.Cards[0].ID = "TASK-2" },
		"card-path":            func(r *bundleRecord) { r.Cards[0].Path = "../TASK-1.md" },
		"card-bytes":           func(r *bundleRecord) { r.Cards[0].Raw = append(r.Cards[0].Raw, ' ') },
		"card-count":           func(r *bundleRecord) { r.Cards = []bundleCard{} },
		"batch-bytes":          func(r *bundleRecord) { r.Batch = append(r.Batch, ' ') },
		"policy-shape":         func(r *bundleRecord) { r.PolicyDigest = "bad" },
		"namespace":            func(r *bundleRecord) { r.Namespace = strings.Repeat("c", 32) },
	}
	for name, mutate := range changes {
		t.Run(name, func(t *testing.T) {
			record := bundleRecordFixture(t)
			before, err := json.Marshal(record)
			if err != nil {
				t.Fatal(err)
			}
			mutate(&record)
			after, err := json.Marshal(record)
			if err != nil {
				t.Fatal(err)
			}
			if string(before) == string(after) {
				t.Fatal("mutation did not change fixture")
			}
			if err := validateBundleRecord(record); err == nil {
				t.Fatal("inconsistent record accepted")
			}
		})
	}
}
