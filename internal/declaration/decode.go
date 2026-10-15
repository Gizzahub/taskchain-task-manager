package declaration

import (
	"bytes"
	"encoding/json"
	"io"
	"reflect"
	"strings"
	"unicode/utf8"
)

// Decode accepts one strict local wire record. Errors never echo input values,
// paths, unknown keys, topics or raw JSON; callers must not log rejected bytes.
func Decode(data []byte) (Record, error) {
	var out Record
	if err := decodeWire(data, SchemaV1, &out); err != nil {
		return Record{}, err
	}
	if err := out.Validate(); err != nil {
		return Record{}, err
	}
	return out, nil
}

func decodeWire(data []byte, schema string, out any) error {
	if len(data) == 0 || len(data) > MaxBytes || !utf8.Valid(data) {
		return ErrInvalid
	}
	d := json.NewDecoder(bytes.NewReader(data))
	if err := checkValue(d, 0); err != nil {
		return ErrInvalid
	}
	if _, err := d.Token(); err != io.EOF {
		return ErrInvalid
	}
	// Detect an unsupported schema without interpreting it as a current record.
	var header struct {
		Schema string `json:"schema"`
	}
	if err := json.Unmarshal(data, &header); err != nil {
		return ErrInvalid
	}
	if header.Schema == "" {
		return ErrInvalid
	}
	if header.Schema != schema {
		return ErrUnsupported
	}
	if err := shape(data, reflect.TypeOf(out)); err != nil {
		return err
	}
	d = json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	if err := d.Decode(out); err != nil {
		return ErrInvalid
	}
	return nil
}

// shape checks exact case-sensitive field names and required presence. Struct
// zero values cannot distinguish a missing required purpose from explicit "".
func shape(data []byte, t reflect.Type) error {
	if t.Kind() == reflect.Pointer {
		return shape(data, t.Elem())
	}
	switch t.Kind() {
	case reflect.Struct:
		var fields map[string]json.RawMessage
		if json.Unmarshal(data, &fields) != nil || fields == nil {
			return ErrInvalid
		}
		allowed := map[string]bool{}
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			tag := strings.Split(f.Tag.Get("json"), ",")
			key := tag[0]
			allowed[key] = true
			v, ok := fields[key]
			if !ok {
				if !strings.Contains(f.Tag.Get("json"), ",omitempty") {
					return ErrInvalid
				}
				continue
			}
			if err := shape(v, f.Type); err != nil {
				return err
			}
		}
		for k := range fields {
			if !allowed[k] {
				return ErrInvalid
			}
		}
	case reflect.Slice:
		var values []json.RawMessage
		if json.Unmarshal(data, &values) != nil || values == nil {
			return ErrInvalid
		}
		for _, v := range values {
			if err := shape(v, t.Elem()); err != nil {
				return err
			}
		}
	}
	return nil
}

// checkValue preserves strict key spelling, rejects duplicates at every level,
// null, unpaired UTF-16 escapes (which encoding/json otherwise replaces), and
// bounded nesting/arrays before typed decoding.
func checkValue(d *json.Decoder, depth int) error {
	if depth > 16 {
		return ErrInvalid
	}
	t, err := d.Token()
	if err != nil || t == nil {
		return ErrInvalid
	}
	switch v := t.(type) {
	case string:
		if strings.ContainsRune(v, utf8.RuneError) {
			return ErrInvalid
		}
	case json.Delim:
		switch v {
		case '{':
			seen := map[string]bool{}
			for d.More() {
				k, e := d.Token()
				key, ok := k.(string)
				if e != nil || !ok || seen[key] || strings.ContainsRune(key, utf8.RuneError) {
					return ErrInvalid
				}
				seen[key] = true
				// Exact lowercase schema keys; encoding/json accepts case-insensitive
				// aliases, so those must be rejected before struct decoding.
				if key != "startedAt" && key != strings.ToLower(key) {
					return ErrInvalid
				}
				if err := checkValue(d, depth+1); err != nil {
					return err
				}
			}
			end, e := d.Token()
			if e != nil || end != json.Delim('}') {
				return ErrInvalid
			}
		case '[':
			n := 0
			for d.More() {
				n++
				if n > 128 {
					return ErrInvalid
				}
				if err := checkValue(d, depth+1); err != nil {
					return err
				}
			}
			end, e := d.Token()
			if e != nil || end != json.Delim(']') {
				return ErrInvalid
			}
		default:
			return ErrInvalid
		}
	}
	return nil
}
