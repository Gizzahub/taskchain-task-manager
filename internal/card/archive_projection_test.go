package card

import (
	"reflect"
	"strings"
	"testing"
)

const archiveProjectionCard = "---\nreview_status: conditional\nreview_evidence: checked by reviewer\nresolution: fixed\nlinks: [TASK-090, ISSUE-7]\nkids: TASK-12\nunknown: keep\n---\n\n# Preserve me\n"

func TestProjectArchiveMetadataMapsTypedFieldsWithoutChangingBytes(t *testing.T) {
	raw := []byte(archiveProjectionCard)
	doc, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	got, err := doc.ProjectArchiveMetadata(ArchiveFieldMapping{
		QualityReview:         "review_status",
		QualityReviewEvidence: "review_evidence",
		Resolution:            "resolution",
		PromotedTo:            "links",
		Children:              "kids",
	})
	if err != nil {
		t.Fatal(err)
	}
	quality := "conditional"
	evidence := "checked by reviewer"
	resolution := "fixed"
	want := ArchiveMetadata{QualityReview: &quality, QualityReviewEvidence: &evidence, Resolution: &resolution, PromotedTo: []string{"TASK-090", "ISSUE-7"}, Children: []string{"TASK-12"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("projection=%+v, want %+v", got, want)
	}
	if !reflect.DeepEqual(raw, doc.Bytes()) {
		t.Fatal("projection changed source bytes")
	}
}

func TestProjectArchiveMetadataAbsentOptionalFieldsHaveNoDefaults(t *testing.T) {
	doc, err := Parse([]byte("---\nid: TASK-1\ntitle: x\n---\nbody\n"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := doc.ProjectArchiveMetadata(ArchiveFieldMapping{
		QualityReview: "quality-review", QualityReviewEvidence: "quality-review-evidence",
		Resolution: "resolution", PromotedTo: "promoted-to", Children: "children",
	})
	if err != nil || got.QualityReview != nil || got.QualityReviewEvidence != nil || got.Resolution != nil || got.PromotedTo != nil || got.Children != nil {
		t.Fatalf("absent projection=%+v err=%v", got, err)
	}
}

func TestProjectArchiveMetadataRejectsAmbiguousAndInvalidMappings(t *testing.T) {
	doc, err := Parse([]byte("---\nreview: pass\n---\nbody\n"))
	if err != nil {
		t.Fatal(err)
	}
	cases := []ArchiveFieldMapping{
		{QualityReview: "review", Resolution: "review"},
		{QualityReview: " review"},
		{QualityReview: "review:key"},
	}
	for _, mapping := range cases {
		if _, err := doc.ProjectArchiveMetadata(mapping); err == nil {
			t.Fatalf("mapping accepted: %+v", mapping)
		}
	}
}

func TestProjectArchiveMetadataRejectsMalformedReviewAndReferences(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		mapf ArchiveFieldMapping
	}{
		{"review mapping", "---\nreview: [pass]\n---\n", ArchiveFieldMapping{QualityReview: "review"}},
		{"review evidence mapping", "---\nevidence: {author: x}\n---\n", ArchiveFieldMapping{QualityReviewEvidence: "evidence"}},
		{"reference mapping", "---\nrefs: [TASK-1, nope]\n---\n", ArchiveFieldMapping{PromotedTo: "refs"}},
		{"empty reference", "---\nrefs: [TASK-1, '']\n---\n", ArchiveFieldMapping{PromotedTo: "refs"}},
		{"nested reference", "---\nrefs:\n  - [TASK-1]\n---\n", ArchiveFieldMapping{PromotedTo: "refs"}},
		{"children scalar type", "---\nkids: 12\n---\n", ArchiveFieldMapping{Children: "kids"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			doc, err := Parse([]byte(tc.raw))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := doc.ProjectArchiveMetadata(tc.mapf); err == nil {
				t.Fatal("malformed mapped metadata accepted")
			} else if strings.Contains(err.Error(), "completion") {
				t.Fatalf("projection performed completion judgement: %v", err)
			}
		})
	}
}

func TestProjectArchiveMetadataAllowsPresentEmptyList(t *testing.T) {
	doc, err := Parse([]byte("---\nchildren: []\n---\n"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := doc.ProjectArchiveMetadata(ArchiveFieldMapping{Children: "children"})
	if err != nil || got.Children == nil || len(got.Children) != 0 {
		t.Fatalf("empty list projection=%+v err=%v", got, err)
	}
}

func TestArchiveProjectionRejectsAliasesAndMergeKeys(t *testing.T) {
	for _, raw := range []string{
		"---\noriginal: &verdict pass\nreview: *verdict\n---\n",
		"---\ndefaults: &defaults {review: pass}\n<<: *defaults\n---\n",
		"---\nrefs: [null]\n---\n",
	} {
		doc, err := Parse([]byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := doc.ProjectArchiveMetadata(ArchiveFieldMapping{QualityReview: "review", Children: "refs"}); err == nil {
			t.Fatalf("accepted ambiguous or invalid metadata %q", raw)
		}
		if string(doc.Bytes()) != raw {
			t.Fatal("refusal changed source")
		}
	}
}

func TestArchiveProjectionDoesNotSelectUnmappedStandardFields(t *testing.T) {
	doc, err := Parse([]byte("---\nquality-review: pass\nquality-review-evidence: checked\n---\n"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := doc.ProjectArchiveMetadata(ArchiveFieldMapping{})
	if err != nil || !reflect.DeepEqual(got, ArchiveMetadata{}) {
		t.Fatalf("implicit projection: %+v %v", got, err)
	}
}
