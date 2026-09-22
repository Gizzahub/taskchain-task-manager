package outputformat

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// ErrNotAnObject reports a document that does not marshal to a JSON object and
// therefore has no top level able to carry the version key.
var ErrNotAnObject = errors.New("output document is not a JSON object")

// ErrVersionAlreadyPresent reports a document that already carries a top-level
// outputVersion key.
//
// This is an error rather than an overwrite because two attachment points for
// one axis is exactly the failure this chokepoint exists to remove: a document
// that sets its own value can disagree with Version, and an overwrite would
// hide that disagreement instead of reporting it. Refusing makes the second
// attachment point fail loudly the moment someone adds it back.
var ErrVersionAlreadyPresent = errors.New("output document already carries a top-level \"outputVersion\" key")

// Encode writes document to w as the single JSON document of a command's
// stdout, carrying the stdout format version at its top level.
//
// The version is attached here and nowhere else. No output type declares an
// OutputVersion field of its own: several of them (ClaimRecord above all) are
// also written verbatim into on-disk ledgers, so a field on the type would
// leak a stdout contract into storage.
//
// Key order: the version key is spliced in directly after the opening brace, so
// it comes first and every other key keeps the order encoding/json gave it.
// Round-tripping through map[string]json.RawMessage would have been shorter but
// reorders the whole body alphabetically, churning every golden fixture on a
// change that touched nothing.
//
// Framing matches json.NewEncoder(w).Encode: compact, HTML-escaped, one
// trailing newline.
func Encode(w io.Writer, document any) error {
	body, err := json.Marshal(document)
	if err != nil {
		return err
	}
	body = bytes.TrimSpace(body)
	if len(body) == 0 || body[0] != '{' {
		return fmt.Errorf("%w: marshals to a JSON %s", ErrNotAnObject, jsonShape(body))
	}
	if hasTopLevelVersionKey(body) {
		return ErrVersionAlreadyPresent
	}

	var buf bytes.Buffer
	buf.Grow(len(body) + 32)
	fmt.Fprintf(&buf, `{"outputVersion":%d`, Version)
	if rest := bytes.TrimSpace(body[1:]); len(rest) > 1 { // more than the closing brace
		buf.WriteByte(',')
		buf.Write(rest)
	} else {
		buf.WriteByte('}')
	}
	buf.WriteByte('\n')
	_, err = w.Write(buf.Bytes())
	return err
}

// hasTopLevelVersionKey reports whether body, known to be a JSON object, names
// outputVersion at its own top level. It reads only the outermost keys, so a
// nested outputVersion inside an embedded input document is not mistaken for
// one of ours.
func hasTopLevelVersionKey(body []byte) bool {
	decoder := json.NewDecoder(bytes.NewReader(body))
	if _, err := decoder.Token(); err != nil { // consume '{'
		return false
	}
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return false
		}
		if name, ok := key.(string); ok && name == "outputVersion" {
			return true
		}
		var skipped json.RawMessage
		if err := decoder.Decode(&skipped); err != nil {
			return false
		}
	}
	return false
}

// jsonShape names the offending kind for the not-an-object error, so the
// message says what was encoded rather than only that it was wrong.
func jsonShape(body []byte) string {
	if len(body) == 0 {
		return "nothing"
	}
	switch body[0] {
	case '[':
		return "array"
	case '"':
		return "string"
	case 't', 'f':
		return "boolean"
	case 'n':
		return "null"
	default:
		return "number"
	}
}
