// Package intentdoc validates standalone intent and batch documents. It does
// not resolve references, write records, authenticate actors or evaluate goals.
package intentdoc

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"unicode/utf8"
)

const MaxDocumentBytes = 256 << 10

type Criterion struct {
	Key  string `json:"key"`
	Text string `json:"text"`
}

type Intent struct {
	SchemaVersion   uint32       `json:"schemaVersion"`
	Kind            string       `json:"kind"`
	ID              string       `json:"id"`
	Revision        uint32       `json:"revision"`
	Title           string       `json:"title"`
	Outcome         string       `json:"outcome"`
	Mode            string       `json:"mode"`
	Constraints     []string     `json:"constraints"`
	NonGoals        []string     `json:"nonGoals"`
	SuccessCriteria []Criterion  `json:"successCriteria"`
	Maintenance     *Maintenance `json:"maintenance,omitempty"`
}

type IntentRef struct {
	ID       string `json:"id"`
	Revision uint32 `json:"revision"`
	Digest   string `json:"digest"`
}

type Evaluation struct {
	Actor         string    `json:"actor"`
	Intent        IntentRef `json:"intent"`
	Decision      string    `json:"decision"`
	Reason        string    `json:"reason"`
	RemainingGaps []string  `json:"remainingGaps"`
	EvidenceRefs  []string  `json:"evidenceRefs"`
}

type Batch struct {
	SchemaVersion     uint32      `json:"schemaVersion"`
	Kind              string      `json:"kind"`
	ID                string      `json:"id"`
	Revision          uint32      `json:"revision"`
	Intent            IntentRef   `json:"intent"`
	Gap               string      `json:"gap"`
	TaskIDs           []string    `json:"taskIds"`
	Constraints       []string    `json:"constraints"`
	AuthorizationRefs []string    `json:"authorizationRefs"`
	Evaluation        *Evaluation `json:"evaluation,omitempty"`
}

// Document retains only validated canonical bytes, not caller-owned slices.
type Document struct {
	kind      string
	id        string
	revision  uint32
	canonical []byte
}

func (d Document) Kind() string     { return d.kind }
func (d Document) ID() string       { return d.id }
func (d Document) Revision() uint32 { return d.revision }

func (d Document) Canonical() ([]byte, error) {
	if len(d.canonical) == 0 {
		return nil, errors.New("uninitialized intent/batch document")
	}
	return append([]byte(nil), d.canonical...), nil
}

func (d Document) Digest() (string, error) {
	if len(d.canonical) == 0 {
		return "", errors.New("uninitialized intent/batch document")
	}
	return fmt.Sprintf("%x", sha256.Sum256(d.canonical)), nil
}

func Parse(raw []byte) (Document, error) {
	if len(raw) == 0 || len(raw) > MaxDocumentBytes || !utf8.Valid(raw) {
		return Document{}, errors.New("document must be nonempty UTF-8 and at most 256 KiB")
	}
	if err := validateEscapes(raw); err != nil {
		return Document{}, err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	value, err := strictValue(dec, 0)
	if err != nil {
		return Document{}, err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return Document{}, errors.New("document must contain exactly one JSON object")
	}
	root, ok := value.(map[string]any)
	if !ok {
		return Document{}, errors.New("document must be an object")
	}
	var data any
	var id string
	var revision uint32
	switch root["kind"] {
	case "intent":
		data = &Intent{}
	case "batch":
		data = &Batch{}
	case "iteration":
		data = &Iteration{}
	default:
		return Document{}, errors.New("kind must be intent, batch or iteration")
	}
	if err := validateShape(value, reflect.TypeOf(data).Elem(), "document"); err != nil {
		return Document{}, err
	}
	// Shape validation enforces exact case-sensitive keys before Go's decoder.
	if err := json.Unmarshal(raw, data); err != nil {
		return Document{}, err
	}
	switch doc := data.(type) {
	case *Intent:
		if err := validateIntent(*doc); err != nil {
			return Document{}, err
		}
		id, revision = doc.ID, doc.Revision
	case *Batch:
		if err := validateBatch(*doc); err != nil {
			return Document{}, err
		}
		id, revision = doc.ID, doc.Revision
	case *Iteration:
		if err := validateIteration(*doc); err != nil {
			return Document{}, err
		}
		id, revision = doc.ID, doc.Revision
	}
	canonical, err := json.Marshal(data)
	if err != nil {
		return Document{}, err
	}
	if len(canonical) > MaxDocumentBytes {
		return Document{}, errors.New("canonical document exceeds 256 KiB")
	}
	return Document{kind: root["kind"].(string), id: id, revision: revision, canonical: canonical}, nil
}
