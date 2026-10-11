package taskflow

import (
	"context"
	"strings"
	"testing"
)

func TestCanonicalReviewDigestChangesBodyAndEvidenceOnly(t *testing.T) {
	base := "---\nid: TASK-001\ntitle: Probe\ntype: chore\nquality-review: pass\nquality-review-evidence: note\n---\nbody\n"
	first, err := CanonicalReviewDigest(base)
	if err != nil {
		t.Fatal(err)
	}
	same, err := CanonicalReviewDigest(strings.Replace(base, "quality-review: pass", "quality-review: waived", 1))
	if err != nil {
		t.Fatal(err)
	}
	if same != first {
		t.Fatal("excluded verdict field changed the digest")
	}
	evidence, err := CanonicalReviewDigest(strings.Replace(base, "quality-review-evidence: note", "quality-review-evidence: other", 1))
	if err != nil || evidence == first {
		t.Fatalf("evidence digest err=%v changed=%v", err, evidence != first)
	}
	body, err := CanonicalReviewDigest(strings.Replace(base, "body\n", "other\n", 1))
	if err != nil || body == first {
		t.Fatalf("body digest err=%v changed=%v", err, body != first)
	}
	if _, err := CanonicalReviewDigest("no frontmatter\n"); err == nil || !strings.Contains(err.Error(), "invalid or missing frontmatter") {
		t.Fatalf("missing frontmatter err=%v", err)
	}
}

func TestAdmissionStatusAndCompound(t *testing.T) {
	criteria := []Criterion{
		{Text: "compound", Command: "test -f a; test -f b"},
		{Text: "unsupported", Command: "rm -rf x"},
	}
	report := AdmitCriteria(context.Background(), t.TempDir(), "todo", "tree", criteria)
	if report.Status() != "incomplete" || report.Eligible != 2 || report.UnexecutedCompound != 1 || report.Unsupported != 1 {
		t.Fatalf("%#v", report)
	}
	outside := AdmitCriteria(context.Background(), t.TempDir(), "doing", "tree", criteria)
	if outside.Status() != "not-applicable" || outside.Eligible != 0 {
		t.Fatalf("%#v", outside)
	}
	if !strings.Contains(report.Format(), "Criterion admission: incomplete; 2 eligible") {
		t.Fatal(report.Format())
	}
}

func TestCheckConfigYAMLAbsentIsNotAnError(t *testing.T) {
	if err := CheckConfigYAML(t.TempDir()); err != nil {
		t.Fatal(err)
	}
}
