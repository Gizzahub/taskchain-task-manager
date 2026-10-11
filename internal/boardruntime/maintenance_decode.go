package boardruntime

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"slices"
	"strings"
)

// DecodePlan accepts one JSON object with no duplicate or unknown fields,
// then freezes it. The digest is the SHA-256 of that canonical JSON.
func DecodePlan(raw []byte) (MaintenancePlan, string, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return MaintenancePlan{}, "", invalidPlan("json", "expected one object")
	}
	probe := json.NewDecoder(bytes.NewReader(raw))
	if err := rejectDuplicateJSON(probe); err != nil {
		return MaintenancePlan{}, "", invalidPlan("json", err.Error())
	}
	if _, err := probe.Token(); err != io.EOF {
		return MaintenancePlan{}, "", invalidPlan("json", "trailing value")
	}
	if err := exactJSONKeys(raw, reflect.TypeOf(MaintenancePlan{})); err != nil {
		return MaintenancePlan{}, "", invalidPlan("json", err.Error())
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var plan MaintenancePlan
	if err := dec.Decode(&plan); err != nil {
		return MaintenancePlan{}, "", invalidPlan("json", err.Error())
	}
	if err := decodeEOF(dec); err != nil {
		return MaintenancePlan{}, "", invalidPlan("json", err.Error())
	}
	return FreezePlan(plan)
}

// FreezePlan validates and canonicalizes a plan. The digest is SHA-256 of the
// canonical JSON.
func FreezePlan(input MaintenancePlan) (MaintenancePlan, string, error) {
	plan := clonePlan(input)
	if err := canonicalize(&plan); err != nil {
		return MaintenancePlan{}, "", err
	}
	if err := validatePlan(&plan); err != nil {
		return MaintenancePlan{}, "", err
	}
	raw, err := json.Marshal(plan)
	if err != nil {
		return MaintenancePlan{}, "", fmt.Errorf("marshal maintenance plan: %w", err)
	}
	sum := sha256.Sum256(raw)
	return plan, hex.EncodeToString(sum[:]), nil
}

func decodeEOF(dec *json.Decoder) error {
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return errors.New("trailing value")
	}
	return nil
}

func rejectDuplicateJSON(dec *json.Decoder) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	return scanJSON(dec, tok)
}

func scanJSON(dec *json.Decoder, tok json.Token) error {
	delim, ok := tok.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		for dec.More() {
			key, err := dec.Token()
			if err != nil {
				return err
			}
			name, ok := key.(string)
			if !ok {
				return errors.New("object key is not a string")
			}
			if seen[name] {
				return fmt.Errorf("duplicate field %q", name)
			}
			seen[name] = true
			value, err := dec.Token()
			if err != nil {
				return err
			}
			if err := scanJSON(dec, value); err != nil {
				return err
			}
		}
		_, err := dec.Token()
		return err
	case '[':
		for dec.More() {
			value, err := dec.Token()
			if err != nil {
				return err
			}
			if err := scanJSON(dec, value); err != nil {
				return err
			}
		}
		_, err := dec.Token()
		return err
	}
	return errors.New("invalid delimiter")
}

func exactJSONKeys(raw []byte, kind reflect.Type) error {
	if kind == reflect.TypeOf(json.RawMessage{}) {
		return nil
	}
	switch kind.Kind() {
	case reflect.Struct:
		var object map[string]json.RawMessage
		if err := json.Unmarshal(raw, &object); err != nil {
			return err
		}
		if object == nil {
			return fmt.Errorf("expected object for %s", kind.Name())
		}
		fields := map[string]reflect.Type{}
		for i := 0; i < kind.NumField(); i++ {
			field := kind.Field(i)
			fields[strings.Split(field.Tag.Get("json"), ",")[0]] = field.Type
		}
		for name, value := range object {
			field, found := fields[name]
			if !found {
				return fmt.Errorf("unknown field %q", name)
			}
			if err := exactJSONKeys(value, field); err != nil {
				return err
			}
		}
	case reflect.Slice:
		var values []json.RawMessage
		if err := json.Unmarshal(raw, &values); err != nil {
			return err
		}
		for _, value := range values {
			if err := exactJSONKeys(value, kind.Elem()); err != nil {
				return err
			}
		}
	case reflect.Pointer:
		return exactJSONKeys(raw, kind.Elem())
	}
	return nil
}

func canonicalize(plan *MaintenancePlan) error {
	sortFiles := func(files []MaintenanceFile) {
		slices.SortFunc(files, func(a, b MaintenanceFile) int { return strings.Compare(a.Path, b.Path) })
		for i := range files {
			slices.Sort(files[i].References)
		}
	}
	sortFiles(plan.Before)
	sortFiles(plan.After)
	slices.SortFunc(plan.Mappings, func(a, b MaintenanceMapping) int { return strings.Compare(a.From, b.From) })
	sortCards := func(cards []MaintenanceCard) error {
		slices.SortFunc(cards, func(a, b MaintenanceCard) int { return strings.Compare(a.Identity, b.Identity) })
		for i := range cards {
			slices.Sort(cards[i].Dependencies)
			if len(cards[i].Unknown) == 0 {
				cards[i].Unknown = nil
			}
			for key, raw := range cards[i].Unknown {
				canonical, err := canonicalRaw(raw)
				if err != nil {
					return invalidPlan("unknown", err.Error())
				}
				cards[i].Unknown[key] = canonical
			}
		}
		return nil
	}
	if err := sortCards(plan.BeforeCards); err != nil {
		return err
	}
	return sortCards(plan.AfterCards)
}

func canonicalRaw(raw []byte) (json.RawMessage, error) {
	probe := json.NewDecoder(bytes.NewReader(raw))
	if err := rejectDuplicateJSON(probe); err != nil {
		return nil, err
	}
	if _, err := probe.Token(); err != io.EOF {
		return nil, fmt.Errorf("unknown field has trailing JSON")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var value any
	if err := dec.Decode(&value); err != nil {
		return nil, err
	}
	result, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return result, nil
}

func clonePlan(in MaintenancePlan) MaintenancePlan {
	out := in
	cloneFiles := func(files []MaintenanceFile) []MaintenanceFile {
		copied := append([]MaintenanceFile(nil), files...)
		for i := range copied {
			copied[i].References = append([]string(nil), copied[i].References...)
		}
		return copied
	}
	cloneCards := func(cards []MaintenanceCard) []MaintenanceCard {
		copied := append([]MaintenanceCard(nil), cards...)
		for i := range copied {
			copied[i].Criteria = append([]string(nil), copied[i].Criteria...)
			copied[i].Dependencies = append([]string(nil), copied[i].Dependencies...)
			copied[i].Evidence = append([]string(nil), copied[i].Evidence...)
			if cards[i].Unknown != nil {
				copied[i].Unknown = map[string]json.RawMessage{}
				for key, raw := range cards[i].Unknown {
					copied[i].Unknown[key] = append(json.RawMessage(nil), raw...)
				}
			}
		}
		return copied
	}
	out.Before = cloneFiles(in.Before)
	out.After = cloneFiles(in.After)
	out.Mappings = append([]MaintenanceMapping(nil), in.Mappings...)
	out.BeforeCards = cloneCards(in.BeforeCards)
	out.AfterCards = cloneCards(in.AfterCards)
	return out
}
