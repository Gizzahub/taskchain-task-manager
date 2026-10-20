package roleprotocol

import (
	"bytes"
	"errors"
	"fmt"
	"sort"

	"github.com/Gizzahub/taskchain-task-manager/internal/boardpolicy"
)

// §14.2 initial limits. They are not measured operating limits.
const (
	maxHistoryPolicies    = 128
	maxHistoryPolicyBytes = 64 << 10
	maxHistoryPolicyTotal = 512 << 10
	maxHistorySelections  = 4
	maxHistorySelectorRaw = 4 << 10
	maxHistoryBindings    = 640
	maxHistoryJSONBytes   = 1 << 20
)

// HistoryMode says which kind of history a caller expects.
type HistoryMode int

const (
	// HistoryLegacySource is the history computed from a verified legacy
	// source: no selections and only unselected bindings.
	HistoryLegacySource HistoryMode = iota + 1
	// HistoryBound is a role-bound history; it holds at least one selection.
	HistoryBound
)

// History is the single cumulative §14.2 history. Policies and Selections
// hold decoded canonical bytes, never their JSON or base64 spelling.
type History struct {
	Policies   map[string][]byte
	Selections map[string][]byte
	Bindings   map[string]Binding
}

// ParseHistory strictly decodes a standalone history document.
func ParseHistory(raw []byte, mode HistoryMode) (History, error) {
	n, err := parseJSON(raw, maxHistoryJSONBytes, "history")
	if err != nil {
		return History{}, err
	}
	return decodeHistory(n, mode)
}

func decodeHistory(n *node, mode HistoryMode) (History, error) {
	f, err := fields(n, "history", "version", "policies", "selections", "bindings")
	if err != nil {
		return History{}, err
	}
	if err := exactInt(f["version"], "history.version", 1); err != nil {
		return History{}, err
	}
	h := History{Policies: map[string][]byte{}, Selections: map[string][]byte{}, Bindings: map[string]Binding{}}
	for _, m := range []struct {
		name  string
		limit int
		out   map[string][]byte
	}{{"policies", maxHistoryPolicyBytes, h.Policies}, {"selections", maxHistorySelectorRaw, h.Selections}} {
		object := f[m.name]
		if object.kind != jsonObject {
			return History{}, fmt.Errorf("history.%s must be an object", m.name)
		}
		for _, key := range object.keys {
			what := "history." + m.name + "[" + key + "]"
			s, err := stringValue(object.obj[key], what)
			if err != nil {
				return History{}, err
			}
			if m.out[key], err = decodeBase64(s, m.limit, what); err != nil {
				return History{}, err
			}
		}
	}
	bindings := f["bindings"]
	if bindings.kind != jsonObject {
		return History{}, errors.New("history.bindings must be an object")
	}
	for _, key := range bindings.keys {
		if h.Bindings[key], err = decodeBinding(bindings.obj[key], "history.bindings["+key+"]"); err != nil {
			return History{}, err
		}
	}
	if err := h.Validate(mode); err != nil {
		return History{}, err
	}
	return h, nil
}

// Validate checks every key/hash, reference, limit and policy rule. Policies
// must pass the existing parser, canonical equality and lineage contract.
func (h History) Validate(mode HistoryMode) error {
	if mode != HistoryLegacySource && mode != HistoryBound {
		return errors.New("unknown history mode")
	}
	if len(h.Policies) == 0 || len(h.Policies) > maxHistoryPolicies {
		return fmt.Errorf("history must hold 1..%d policies", maxHistoryPolicies)
	}
	total := 0
	parsed := make([]boardpolicy.Policy, 0, len(h.Policies))
	for _, digest := range sortedKeys(h.Policies) {
		raw := h.Policies[digest]
		total += len(raw)
		if !isDigest(digest) || len(raw) == 0 || len(raw) > maxHistoryPolicyBytes || rawDigest(raw) != digest {
			return fmt.Errorf("history policy %s: invalid digest key or size", digest)
		}
		p, err := boardpolicy.Parse(raw)
		if err != nil {
			return fmt.Errorf("history policy %s: %w", digest, err)
		}
		if canonical, err := p.Canonical(); err != nil || !bytes.Equal(canonical, raw) {
			return fmt.Errorf("history policy %s is not canonical", digest)
		}
		parsed = append(parsed, p)
	}
	if total > maxHistoryPolicyTotal {
		return fmt.Errorf("history policies exceed %d bytes", maxHistoryPolicyTotal)
	}
	if err := boardpolicy.ValidateFileRoleLineage(parsed...); err != nil {
		return fmt.Errorf("history: %w", err)
	}
	switch {
	case mode == HistoryLegacySource && len(h.Selections) != 0:
		return errors.New("legacy source history cannot hold selections")
	case mode == HistoryBound && len(h.Selections) == 0:
		return errors.New("role-bound history must hold a selection")
	case len(h.Selections) > maxHistorySelections:
		return fmt.Errorf("history holds more than %d selections", maxHistorySelections)
	}
	for _, digest := range sortedKeys(h.Selections) {
		raw := h.Selections[digest]
		if !isDigest(digest) || len(raw) > maxHistorySelectorRaw {
			return fmt.Errorf("history selection %s: invalid digest key or size", digest)
		}
		s, err := canonicalSelector(raw)
		if err != nil {
			return fmt.Errorf("history selection %s: %w", digest, err)
		}
		if d, _ := s.Digest(); d != digest {
			return fmt.Errorf("history selection %s does not match its key", digest)
		}
	}
	if len(h.Bindings) == 0 || len(h.Bindings) > maxHistoryBindings {
		return fmt.Errorf("history must hold 1..%d bindings", maxHistoryBindings)
	}
	usedPolicies, usedSelections := map[string]bool{}, map[string]bool{}
	for _, digest := range sortedKeys(h.Bindings) {
		b := h.Bindings[digest]
		canonical, err := b.Canonical()
		if err != nil {
			return fmt.Errorf("history binding %s: %w", digest, err)
		}
		if len(canonical) > maxBindingBytes || domainDigest(domainBinding, canonical) != digest {
			return fmt.Errorf("history binding %s does not match its key", digest)
		}
		if h.Policies[b.PolicyDigest] == nil {
			return fmt.Errorf("history binding %s references a missing policy", digest)
		}
		if mode == HistoryLegacySource && b.Kind != BindingUnselected {
			return errors.New("legacy source history can only hold unselected bindings")
		}
		usedPolicies[b.PolicyDigest] = true
		if b.SelectionDigest != nil {
			if h.Selections[*b.SelectionDigest] == nil {
				return fmt.Errorf("history binding %s references a missing selection", digest)
			}
			usedSelections[*b.SelectionDigest] = true
		}
	}
	// Writers only add a policy or selection together with a binding, so an
	// unreferenced entry has no provenance; refuse it rather than carry it.
	if len(usedPolicies) != len(h.Policies) || len(usedSelections) != len(h.Selections) {
		return errors.New("history holds a policy or selection no binding references")
	}
	return nil
}

// Digest is D(task-manager:role-history:v1, encoded maps): per map, in the
// order policies, selections, bindings, U32(count) then each sorted key and
// L(canonical value bytes).
func (h History) Digest() (string, error) {
	var out []byte
	out = appendHistoryMap(out, h.Policies)
	out = appendHistoryMap(out, h.Selections)
	bindings := make(map[string][]byte, len(h.Bindings))
	for digest, b := range h.Bindings {
		canonical, err := b.Canonical()
		if err != nil {
			return "", err
		}
		bindings[digest] = canonical
	}
	out = appendHistoryMap(out, bindings)
	return domainDigest(domainHistory, out), nil
}

func appendHistoryMap(out []byte, m map[string][]byte) []byte {
	out = appendU32(out, len(m))
	for _, key := range sortedKeys(m) {
		out = appendL(append(out, key...), m[key])
	}
	return out
}

// ValidateCurrent requires the current binding to exist and bind the active
// policy; a target current binding must also be selected.
func (h History) ValidateCurrent(currentBindingDigest, activePolicyDigest string, requireSelected bool) error {
	b, ok := h.Bindings[currentBindingDigest]
	if !ok {
		return errors.New("current binding is not in history")
	}
	if b.PolicyDigest != activePolicyDigest {
		return errors.New("current binding does not bind the active policy")
	}
	if requireSelected && b.Kind != BindingSelected {
		return errors.New("target current binding must be selected")
	}
	return nil
}

// ValidateHistoryExtension refuses a target history that deletes a source
// entry or holds different bytes under a source key.
func ValidateHistoryExtension(source, target History) error {
	for digest, raw := range source.Policies {
		if got, ok := target.Policies[digest]; !ok || !bytes.Equal(got, raw) {
			return fmt.Errorf("target history drops or alters policy %s", digest)
		}
	}
	for digest, raw := range source.Selections {
		if got, ok := target.Selections[digest]; !ok || !bytes.Equal(got, raw) {
			return fmt.Errorf("target history drops or alters selection %s", digest)
		}
	}
	for digest, b := range source.Bindings {
		got, ok := target.Bindings[digest]
		want, _ := b.Canonical()
		have, _ := got.Canonical()
		if !ok || !bytes.Equal(want, have) {
			return fmt.Errorf("target history drops or alters binding %s", digest)
		}
	}
	return nil
}

// LegacySourceHistory computes the §14.4 legacy source history: one
// unselected binding per historical canonical policy. The result is derived
// from observed legacy history; it does not claim that any old receipt stored
// it. It returns the history, the current binding digest and history digest.
func LegacySourceHistory(policies [][]byte, activePolicyDigest string) (History, string, string, error) {
	h := History{Policies: map[string][]byte{}, Selections: map[string][]byte{}, Bindings: map[string]Binding{}}
	for _, raw := range policies {
		digest := rawDigest(raw)
		if previous, dup := h.Policies[digest]; dup && !bytes.Equal(previous, raw) {
			return History{}, "", "", errors.New("legacy policy digest collision")
		}
		h.Policies[digest] = append([]byte(nil), raw...)
		b := UnselectedBinding(digest)
		bindingDigest, err := b.Digest()
		if err != nil {
			return History{}, "", "", err
		}
		h.Bindings[bindingDigest] = b
	}
	if err := h.Validate(HistoryLegacySource); err != nil {
		return History{}, "", "", err
	}
	current, err := UnselectedBinding(activePolicyDigest).Digest()
	if err != nil {
		return History{}, "", "", err
	}
	if err := h.ValidateCurrent(current, activePolicyDigest, false); err != nil {
		return History{}, "", "", err
	}
	historyDigest, err := h.Digest()
	if err != nil {
		return History{}, "", "", err
	}
	return h, current, historyDigest, nil
}

// ValidateRecordedPolicies requires every completed transition record's
// policy digest to be in history; a missing past policy is never inferred.
func (h History) ValidateRecordedPolicies(recordPolicyDigests []string) error {
	for _, digest := range recordPolicyDigests {
		if h.Policies[digest] == nil {
			return fmt.Errorf("completed record policy %s is missing from history", digest)
		}
	}
	return nil
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
