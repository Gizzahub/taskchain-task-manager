package outputformat_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Gizzahub/taskchain-task-manager/internal/outputformat"
)

// TestEncodeAttachesTheVersionKeyWithItsValue grades the emitted bytes, which
// is what a consumer contracts with. Before the chokepoint existed this test
// could only marshal zero-valued structs and so graded the json tag alone: a
// construction site that hardcoded 1, or borrowed a journal schema constant,
// kept the key and still published the wrong axis' value. With one attachment
// point the value is gradable here, once, for every document.
func TestEncodeAttachesTheVersionKeyWithItsValue(t *testing.T) {
	var out bytes.Buffer
	if err := outputformat.Encode(&out, struct {
		Scope string `json:"scope"`
	}{"board"}); err != nil {
		t.Fatalf("encode: %v", err)
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(out.Bytes(), &top); err != nil {
		t.Fatalf("decode %s: %v", out.Bytes(), err)
	}
	raw, ok := top["outputVersion"]
	if !ok {
		t.Fatalf("emitted document has no top-level \"outputVersion\" key: %s", out.Bytes())
	}
	var version int
	if err := json.Unmarshal(raw, &version); err != nil {
		t.Fatalf("outputVersion is not a number: %s", raw)
	}
	if version != outputformat.Version {
		t.Errorf("outputVersion = %d, want outputformat.Version = %d", version, outputformat.Version)
	}
	// Regaining "schemaVersion" at the top level would re-merge the output axis
	// with the on-disk journal axis, which is independently at 1-4.
	if _, ok := top["schemaVersion"]; ok {
		t.Errorf("emitted document carries a top-level \"schemaVersion\" key, which names the on-disk journal axis: %s", out.Bytes())
	}
}

// TestEncodeFramesLikeTheEncoderItReplaced pins the byte framing, because the
// golden fixtures land on top of it: compact, one trailing newline, version key
// first and every other key in the order the struct declares it. A round trip
// through map[string]json.RawMessage would sort the body alphabetically and
// churn every fixture on a change that touched nothing.
func TestEncodeFramesLikeTheEncoderItReplaced(t *testing.T) {
	var out bytes.Buffer
	if err := outputformat.Encode(&out, struct {
		Zebra string `json:"zebra"`
		Apple string `json:"apple"`
		Mango string `json:"mango"`
	}{"z", "a", "m"}); err != nil {
		t.Fatalf("encode: %v", err)
	}
	want := "{\"outputVersion\":1,\"zebra\":\"z\",\"apple\":\"a\",\"mango\":\"m\"}\n"
	if got := out.String(); got != want {
		t.Errorf("encoded\n got %q\nwant %q", got, want)
	}
}

// TestEncodeEmitsTheVersionForAnEmptyDocument covers the splice's one special
// case: there is no body key to put a comma before.
func TestEncodeEmitsTheVersionForAnEmptyDocument(t *testing.T) {
	var out bytes.Buffer
	if err := outputformat.Encode(&out, struct{}{}); err != nil {
		t.Fatalf("encode: %v", err)
	}
	if want := "{\"outputVersion\":1}\n"; out.String() != want {
		t.Errorf("encoded %q, want %q", out.String(), want)
	}
}

// TestEncodeRefusesADocumentThatIsNotAnObject is the whole reason the helper
// returns an error instead of wrapping quietly: an array has no top level to
// carry the key, and a silent pass would let the next sequence-shaped command
// ship without a version and without anyone noticing.
func TestEncodeRefusesADocumentThatIsNotAnObject(t *testing.T) {
	for name, document := range map[string]any{
		"array":  []int{1, 2},
		"string": "board",
		"number": 7,
		"null":   nil,
	} {
		var out bytes.Buffer
		err := outputformat.Encode(&out, document)
		if !errors.Is(err, outputformat.ErrNotAnObject) {
			t.Errorf("%s document: err = %v, want ErrNotAnObject", name, err)
		}
		if err != nil && !strings.Contains(err.Error(), name) {
			t.Errorf("%s document: error %q does not name the offending shape", name, err)
		}
		if out.Len() != 0 {
			t.Errorf("%s document: wrote %q despite refusing it", name, out.String())
		}
	}
}

// TestEncodeLeavesANestedSchemaVersionAlone guards the embedded input
// documents: validate-context carries a caller's intent document under
// "canonical", and that document's own schemaVersion is its contract, not ours.
//
// The nested document names outputVersion FIRST on purpose. hasTopLevelVersionKey
// walks the outer key stream and skips each value wholesale; a skip that failed
// to swallow a nested object would see the nested key soonest in this order, and
// refuse the document as already versioned.
func TestEncodeLeavesANestedSchemaVersionAlone(t *testing.T) {
	var out bytes.Buffer
	if err := outputformat.Encode(&out, struct {
		Scope     string          `json:"scope"`
		Canonical json.RawMessage `json:"canonical"`
	}{"intent-document", json.RawMessage(`{"outputVersion":9,"schemaVersion":4}`)}); err != nil {
		t.Fatalf("encode: %v", err)
	}
	want := "{\"outputVersion\":1,\"scope\":\"intent-document\",\"canonical\":{\"outputVersion\":9,\"schemaVersion\":4}}\n"
	if got := out.String(); got != want {
		t.Errorf("encoded\n got %q\nwant %q", got, want)
	}
}

// TestEncodeRefusesASecondAttachmentPoint states the decision recorded on
// ErrVersionAlreadyPresent: a document that sets the key itself has re-created
// the split this package removed, and refusing reports it instead of hiding it
// under an overwrite.
func TestEncodeRefusesASecondAttachmentPoint(t *testing.T) {
	var out bytes.Buffer
	err := outputformat.Encode(&out, struct {
		OutputVersion int    `json:"outputVersion"`
		Scope         string `json:"scope"`
	}{outputformat.Version, "board"})
	if !errors.Is(err, outputformat.ErrVersionAlreadyPresent) {
		t.Errorf("err = %v, want ErrVersionAlreadyPresent", err)
	}
	if out.Len() != 0 {
		t.Errorf("wrote %q despite refusing it", out.String())
	}
}
