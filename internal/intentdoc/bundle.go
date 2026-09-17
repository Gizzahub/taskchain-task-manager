package intentdoc

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"unicode/utf8"
)

type BundleRequest struct {
	SchemaVersion uint32         `json:"schemaVersion"`
	Kind          string         `json:"kind"`
	RequestID     string         `json:"requestId"`
	Batch         BundleMetadata `json:"batch"`
	Tasks         []TaskDraft    `json:"tasks"`
}

type BundleMetadata struct {
	ID                string    `json:"id"`
	Revision          uint32    `json:"revision"`
	Intent            IntentRef `json:"intent"`
	Gap               string    `json:"gap"`
	Constraints       []string  `json:"constraints"`
	AuthorizationRefs []string  `json:"authorizationRefs"`
}

type TaskDraft struct {
	Key       string          `json:"key"`
	ID        string          `json:"id"`
	Title     string          `json:"title"`
	DependsOn []TaskReference `json:"dependsOn"`
	Template  *DraftTemplate  `json:"template,omitempty"`
	Module    *string         `json:"module,omitempty"`
	Category  *string         `json:"category,omitempty"`
}

type TaskReference struct {
	TaskID string `json:"taskId"`
	Key    string `json:"key"`
}

type DraftTemplate struct {
	ValidationConfig string   `json:"validationConfig"`
	Type             string   `json:"type"`
	Priority         string   `json:"priority"`
	Summary          string   `json:"summary"`
	Criteria         []string `json:"criteria"`
}

// BundleDocument owns a validated canonical copy and never exposes mutable
// parser storage.
type BundleDocument struct{ canonical []byte }

func (d BundleDocument) Canonical() ([]byte, error) {
	if len(d.canonical) == 0 {
		return nil, errors.New("uninitialized task bundle document")
	}
	return append([]byte(nil), d.canonical...), nil
}

func (d BundleDocument) Digest() (string, error) {
	if len(d.canonical) == 0 {
		return "", errors.New("uninitialized task bundle document")
	}
	sum := sha256.Sum256(d.canonical)
	return hex.EncodeToString(sum[:]), nil
}

func (d BundleDocument) Snapshot() (BundleRequest, error) {
	canonical, err := d.Canonical()
	if err != nil {
		return BundleRequest{}, err
	}
	var request BundleRequest
	if err := json.Unmarshal(canonical, &request); err != nil {
		return BundleRequest{}, err
	}
	return request, nil
}

func ParseBundle(raw []byte) (BundleDocument, error) {
	if len(raw) == 0 || len(raw) > MaxDocumentBytes || !utf8.Valid(raw) {
		return BundleDocument{}, errors.New("bundle must be nonempty UTF-8 and at most 256 KiB")
	}
	if err := validateEscapes(raw); err != nil {
		return BundleDocument{}, err
	}
	value, err := parseStrictObject(raw)
	if err != nil {
		return BundleDocument{}, err
	}
	if err := validateShape(value, reflect.TypeOf(BundleRequest{}), "bundle"); err != nil {
		return BundleDocument{}, err
	}
	var request BundleRequest
	if err := json.Unmarshal(raw, &request); err != nil {
		return BundleDocument{}, err
	}
	if err := validateBundle(request); err != nil {
		return BundleDocument{}, err
	}
	canonical, err := json.Marshal(request)
	if err != nil {
		return BundleDocument{}, err
	}
	if len(canonical) > MaxDocumentBytes {
		return BundleDocument{}, errors.New("canonical bundle exceeds 256 KiB")
	}
	return BundleDocument{canonical: append([]byte(nil), canonical...)}, nil
}

func parseStrictObject(raw []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	value, err := strictValue(dec, 0)
	if err != nil {
		return nil, err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("bundle must contain exactly one JSON object")
	}
	if _, ok := value.(map[string]any); !ok {
		return nil, errors.New("bundle must be an object")
	}
	return value, nil
}
