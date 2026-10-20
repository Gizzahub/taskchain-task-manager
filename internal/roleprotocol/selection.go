package roleprotocol

import (
	"errors"
	"fmt"
)

const (
	selectionContract = "task-manager:role-selection:v1"
	maxSelectorBytes  = 64 << 10
	maxBindingBytes   = 512
)

// Selector is the §14.2 role selection. A syntactically valid selector says
// nothing about whether an artifact implements the profile it names.
type Selector struct {
	BaseProfile string // section-9 | section-10
	Amendment   string // none | section-11
}

// ParseSelector strictly decodes raw selector bytes in any key order.
func ParseSelector(raw []byte) (Selector, error) {
	n, err := parseJSON(raw, maxSelectorBytes, "selector")
	if err != nil {
		return Selector{}, err
	}
	return decodeSelector(n)
}

func decodeSelector(n *node) (Selector, error) {
	f, err := fields(n, "selector", "contract", "baseProfile", "amendment")
	if err != nil {
		return Selector{}, err
	}
	if _, err := enumValue(f["contract"], "selector.contract", selectionContract); err != nil {
		return Selector{}, err
	}
	base, err := enumValue(f["baseProfile"], "selector.baseProfile", "section-9", "section-10")
	if err != nil {
		return Selector{}, err
	}
	amendment, err := enumValue(f["amendment"], "selector.amendment", "none", "section-11")
	if err != nil {
		return Selector{}, err
	}
	return Selector{BaseProfile: base, Amendment: amendment}, nil
}

func (s Selector) validate() error {
	_, err := decodeSelector(objectNode(
		member{"contract", stringNode(selectionContract)},
		member{"baseProfile", stringNode(s.BaseProfile)},
		member{"amendment", stringNode(s.Amendment)}))
	return err
}

// Canonical is the fixed-order minified ASCII JSON without trailing LF. All
// values are validated enum spellings, so no escaping is ever needed.
func (s Selector) Canonical() ([]byte, error) {
	if err := s.validate(); err != nil {
		return nil, err
	}
	return []byte(`{"contract":"` + selectionContract + `","baseProfile":"` + s.BaseProfile +
		`","amendment":"` + s.Amendment + `"}`), nil
}

// Digest is D(task-manager:role-selection:v1, canonical selector).
func (s Selector) Digest() (string, error) {
	raw, err := s.Canonical()
	if err != nil {
		return "", err
	}
	return domainDigest(domainSelection, raw), nil
}

// Profile is the support profile label, e.g. "section-9/none".
func (s Selector) Profile() string { return s.BaseProfile + "/" + s.Amendment }

// canonicalSelector accepts only bytes that already are a canonical selector.
func canonicalSelector(raw []byte) (Selector, error) {
	s, err := ParseSelector(raw)
	if err != nil {
		return Selector{}, err
	}
	canonical, _ := s.Canonical()
	if string(canonical) != string(raw) {
		return Selector{}, errors.New("selector bytes are not canonical")
	}
	return s, nil
}

const (
	BindingSelected   = "selected"
	BindingUnselected = "unselected"
)

// Binding couples a policy digest with a selection digest (selected) or with
// an explicit null (unselected: observed legacy history, no inferred profile).
type Binding struct {
	Kind            string
	PolicyDigest    string
	SelectionDigest *string
}

func SelectedBinding(policyDigest, selectionDigest string) Binding {
	return Binding{Kind: BindingSelected, PolicyDigest: policyDigest, SelectionDigest: &selectionDigest}
}

func UnselectedBinding(policyDigest string) Binding {
	return Binding{Kind: BindingUnselected, PolicyDigest: policyDigest}
}

func (b Binding) validate() error {
	if !isDigest(b.PolicyDigest) {
		return errors.New("binding.policyDigest must be lowercase hex64")
	}
	switch b.Kind {
	case BindingSelected:
		if b.SelectionDigest == nil || !isDigest(*b.SelectionDigest) {
			return errors.New("selected binding requires a selectionDigest")
		}
	case BindingUnselected:
		if b.SelectionDigest != nil {
			return errors.New("unselected binding requires a null selectionDigest")
		}
	default:
		return fmt.Errorf("binding.kind has unsupported value %q", b.Kind)
	}
	return nil
}

// Canonical is the fixed-order minified ASCII binding JSON.
func (b Binding) Canonical() ([]byte, error) {
	if err := b.validate(); err != nil {
		return nil, err
	}
	selection := "null"
	if b.SelectionDigest != nil {
		selection = `"` + *b.SelectionDigest + `"`
	}
	return []byte(`{"kind":"` + b.Kind + `","policyDigest":"` + b.PolicyDigest +
		`","selectionDigest":` + selection + `}`), nil
}

// Digest is D(task-manager:role-binding:v1, canonical binding).
func (b Binding) Digest() (string, error) {
	raw, err := b.Canonical()
	if err != nil {
		return "", err
	}
	return domainDigest(domainBinding, raw), nil
}

// decodeBinding reads a stored binding object. Its keys must appear in the
// canonical order kind, policyDigest, selectionDigest.
func decodeBinding(n *node, what string) (Binding, error) {
	f, err := fields(n, what, "kind", "policyDigest", "selectionDigest")
	if err != nil {
		return Binding{}, err
	}
	if n.keys[0] != "kind" || n.keys[1] != "policyDigest" || n.keys[2] != "selectionDigest" {
		return Binding{}, fmt.Errorf("%s keys must be in canonical order", what)
	}
	kind, err := enumValue(f["kind"], what+".kind", BindingSelected, BindingUnselected)
	if err != nil {
		return Binding{}, err
	}
	policy, err := digestValue(f["policyDigest"], what+".policyDigest")
	if err != nil {
		return Binding{}, err
	}
	selection, err := nullableDigest(f["selectionDigest"], what+".selectionDigest")
	if err != nil {
		return Binding{}, err
	}
	b := Binding{Kind: kind, PolicyDigest: policy, SelectionDigest: selection}
	if err := b.validate(); err != nil {
		return Binding{}, fmt.Errorf("%s: %w", what, err)
	}
	return b, nil
}
