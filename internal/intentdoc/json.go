package intentdoc

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

func strictValue(dec *json.Decoder, depth int) (any, error) {
	if depth > 16 {
		return nil, errors.New("JSON depth exceeds 16")
	}
	token, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if token == nil {
		return nil, errors.New("null is not allowed")
	}
	delim, isDelim := token.(json.Delim)
	if !isDelim {
		return token, nil
	}
	switch delim {
	case '{':
		out := map[string]any{}
		for dec.More() {
			keyToken, err := dec.Token()
			if err != nil {
				return nil, err
			}
			key, ok := keyToken.(string)
			if !ok {
				return nil, errors.New("object key must be a string")
			}
			if _, exists := out[key]; exists {
				return nil, fmt.Errorf("duplicate JSON field %q", key)
			}
			value, err := strictValue(dec, depth+1)
			if err != nil {
				return nil, err
			}
			out[key] = value
		}
		if end, err := dec.Token(); err != nil || end != json.Delim('}') {
			return nil, errors.New("unterminated JSON object")
		}
		return out, nil
	case '[':
		out := []any{}
		for dec.More() {
			if len(out) == 128 {
				return nil, errors.New("array exceeds 128 items")
			}
			value, err := strictValue(dec, depth+1)
			if err != nil {
				return nil, err
			}
			out = append(out, value)
		}
		if end, err := dec.Token(); err != nil || end != json.Delim(']') {
			return nil, errors.New("unterminated JSON array")
		}
		return out, nil
	}
	return nil, errors.New("unexpected JSON delimiter")
}

func validateShape(value any, typ reflect.Type, path string) error {
	if typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	switch typ.Kind() {
	case reflect.Struct:
		object, ok := value.(map[string]any)
		if !ok {
			return fmt.Errorf("%s must be an object", path)
		}
		fields := map[string]reflect.StructField{}
		for i := 0; i < typ.NumField(); i++ {
			field := typ.Field(i)
			fields[strings.Split(field.Tag.Get("json"), ",")[0]] = field
		}
		keys := make([]string, 0, len(object))
		for key := range object {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			if _, ok := fields[key]; !ok {
				return fmt.Errorf("unknown field %s.%s", path, key)
			}
		}
		for i := 0; i < typ.NumField(); i++ {
			field := typ.Field(i)
			key := strings.Split(field.Tag.Get("json"), ",")[0]
			node, exists := object[key]
			if !exists {
				if field.Type.Kind() == reflect.Pointer {
					continue
				}
				return fmt.Errorf("missing field %s.%s", path, key)
			}
			if err := validateShape(node, field.Type, path+"."+key); err != nil {
				return err
			}
		}
	case reflect.Slice:
		values, ok := value.([]any)
		if !ok {
			return fmt.Errorf("%s must be an array", path)
		}
		for i, node := range values {
			if err := validateShape(node, typ.Elem(), fmt.Sprintf("%s[%d]", path, i)); err != nil {
				return err
			}
		}
	case reflect.String:
		if _, ok := value.(string); !ok {
			return fmt.Errorf("%s must be a string", path)
		}
	case reflect.Bool:
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("%s must be a boolean", path)
		}
	case reflect.Uint32:
		number, ok := value.(json.Number)
		if !ok {
			return fmt.Errorf("%s must be an unsigned integer", path)
		}
		if _, err := strconv.ParseUint(string(number), 10, 32); err != nil {
			return fmt.Errorf("%s must be an unsigned 32-bit integer", path)
		}
	default:
		return errors.New("unsupported internal document field type")
	}
	return nil
}

// encoding/json replaces unpaired surrogate escapes with U+FFFD. Reject
// those inputs rather than silently changing the caller's document content.
func validateEscapes(raw []byte) error {
	for i := 0; i < len(raw); i++ {
		if raw[i] != '\\' {
			continue
		}
		i++
		if i >= len(raw) || raw[i] != 'u' {
			continue
		}
		if i+4 >= len(raw) {
			return errors.New("short Unicode escape")
		}
		n, err := strconv.ParseUint(string(raw[i+1:i+5]), 16, 16)
		if err != nil {
			return errors.New("invalid Unicode escape")
		}
		i += 4
		if n >= 0xdc00 && n <= 0xdfff {
			return errors.New("unpaired low surrogate")
		}
		if n < 0xd800 || n > 0xdbff {
			continue
		}
		if i+6 >= len(raw) || raw[i+1] != '\\' || raw[i+2] != 'u' {
			return errors.New("unpaired high surrogate")
		}
		low, err := strconv.ParseUint(string(raw[i+3:i+7]), 16, 16)
		if err != nil || low < 0xdc00 || low > 0xdfff {
			return errors.New("invalid surrogate pair")
		}
		i += 6
	}
	return nil
}
