package taskstore

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/Gizzahub/taskchain-task-manager/internal/boardpolicy"
)

const policyActivationFile = ".task-manager-policy-activation.json"
const maxPolicyActivationBytes = 2 << 20

type policyActivationPlan struct {
	Root               string            `json:"root"`
	HEAD               string            `json:"head"`
	Snapshot           string            `json:"snapshot"`
	OriginalJournal    string            `json:"originalJournal"`
	TargetJournal      string            `json:"targetJournal"`
	OriginalPolicy     string            `json:"originalPolicy"`
	OriginalActivation string            `json:"originalActivation"`
	OriginalIDs        string            `json:"originalIds"`
	TargetIDs          string            `json:"targetIds"`
	IDTarget           []byte            `json:"idTarget"`
	ModuleAdoption     *moduleIDAdoption `json:"moduleAdoption,omitempty"`
}

type policyActivationState struct {
	SchemaVersion int                  `json:"schemaVersion"`
	Phase         string               `json:"phase"`
	AuthorityID   string               `json:"authorityId"`
	Scope         string               `json:"scope"`
	Namespace     string               `json:"namespace"`
	Canonical     []byte               `json:"canonical"`
	Digest        string               `json:"digest"`
	Plan          policyActivationPlan `json:"plan"`
	Revision      *policyRevision      `json:"revision,omitempty"`
}

type sharedPolicyAuthority struct {
	AuthorityID string                 `json:"authorityId"`
	Phase       string                 `json:"phase"`
	Canonical   []byte                 `json:"canonical"`
	Digest      string                 `json:"digest"`
	Pending     []policyActivationPlan `json:"pending"`
	Revision    *policyRevision        `json:"revision,omitempty"`
}

func loadPolicyActivation(r *os.Root) (policyActivationState, error) {
	info, err := r.Lstat(policyActivationFile)
	if err != nil {
		return policyActivationState{}, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return policyActivationState{}, errors.New("policy activation is not a regular file")
	}
	if info.Size() > maxModuleActivationBytes {
		return policyActivationState{}, errors.New("policy activation exceeds 4 MiB")
	}
	f, err := r.Open(policyActivationFile)
	if err != nil {
		return policyActivationState{}, err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, maxModuleActivationBytes+1))
	if err != nil {
		return policyActivationState{}, err
	}
	if len(raw) > maxModuleActivationBytes || !utf8.Valid(raw) {
		return policyActivationState{}, errors.New("invalid policy activation size or UTF-8")
	}
	if err := validatePolicyActivationShape(raw); err != nil {
		return policyActivationState{}, err
	}
	var state policyActivationState
	if err := decodeExact(raw, &state); err != nil {
		return policyActivationState{}, err
	}
	if state.SchemaVersion < 3 && len(raw) > maxPolicyActivationBytes {
		return policyActivationState{}, errors.New("legacy policy activation exceeds 2 MiB")
	}
	if err := validatePolicyActivationState(state); err != nil {
		return policyActivationState{}, err
	}
	return state, nil
}

func savePolicyActivation(r *os.Root, state policyActivationState, initial bool) error {
	raw, err := policyActivationBytes(state)
	if err != nil {
		return err
	}
	name, err := stage(r, raw)
	if err != nil {
		return err
	}
	if initial {
		if err := r.Link(name, policyActivationFile); err != nil {
			return errors.Join(err, r.Remove(name))
		}
		if err := r.Remove(name); err != nil {
			return err
		}
		d, err := r.Open(".")
		if err != nil {
			return err
		}
		return errors.Join(d.Sync(), d.Close())
	}
	if err := r.Rename(name, policyActivationFile); err != nil {
		return errors.Join(err, r.Remove(name))
	}
	d, err := r.Open(".")
	if err != nil {
		return err
	}
	return errors.Join(d.Sync(), d.Close())
}

func policyActivationBytes(state policyActivationState) ([]byte, error) {
	if err := validatePolicyActivationState(state); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(state)
	if err != nil {
		return nil, err
	}
	raw = append(raw, '\n')
	limit := maxPolicyActivationBytes
	if state.SchemaVersion == 3 {
		limit = maxModuleActivationBytes
	}
	if len(raw) > limit {
		return nil, errors.New("policy activation exceeds its schema byte limit")
	}
	return raw, nil
}

func validatePolicyActivationState(state policyActivationState) error {
	if (state.SchemaVersion != 1 && state.SchemaVersion != 2 && state.SchemaVersion != 3) || (state.Phase != "pending" && state.Phase != "completed" && !(state.SchemaVersion == 3 && state.Phase == "ids-published")) {
		return errors.New("invalid policy activation schema or phase")
	}
	if err := validateActivationRevision(state); err != nil {
		return err
	}
	if (state.SchemaVersion == 3) != (state.Plan.ModuleAdoption != nil) {
		return errors.New("module adoption plan requires activation schema 3")
	}
	if !sharedHex32.MatchString(state.AuthorityID) {
		return errors.New("invalid policy activation authority ID")
	}
	if state.Scope != "local" && state.Scope != "shared" {
		return errors.New("invalid policy activation scope")
	}
	if state.Scope == "local" && state.Namespace != "" {
		return errors.New("local policy activation namespace must be empty")
	}
	if state.Scope == "shared" && !sharedHex32.MatchString(state.Namespace) {
		return errors.New("invalid shared policy activation namespace")
	}
	if len(state.Canonical) == 0 || len(state.Canonical) > 64<<10 || !utf8.Valid(state.Canonical) {
		return errors.New("invalid policy activation canonical policy")
	}
	p, err := boardpolicy.Parse(state.Canonical)
	if err != nil {
		return fmt.Errorf("parse activation policy: %w", err)
	}
	if state.Plan.ModuleAdoption != nil && len(p.Modules()) == 0 {
		return errors.New("module adoption requires declared modules")
	}
	if state.Revision != nil {
		previous, err := boardpolicy.Parse(state.Revision.PreviousCanonical)
		if err != nil {
			return err
		}
		if err := validateModuleScopeChange(previous, p, state.Plan.ModuleAdoption != nil); err != nil {
			return err
		}
	}
	canonical, err := p.Canonical()
	if err != nil || !bytes.Equal(canonical, state.Canonical) {
		return errors.New("policy activation canonical bytes are not canonical")
	}
	if bytesDigest(state.Canonical) != state.Digest || !sharedHex64.MatchString(state.Digest) {
		return errors.New("policy activation digest mismatch")
	}
	if err := validatePolicyActivationPlan(state.Plan, state.Scope == "shared"); err != nil {
		return err
	}
	if len(state.Plan.IDTarget) > 0 {
		ledger, err := decodeIDs(state.Plan.IDTarget)
		if err != nil || ledger.Namespace != state.Namespace {
			return errors.New("policy activation ID target namespace mismatch")
		}
	}
	return nil
}

func validatePolicyActivationPlan(plan policyActivationPlan, shared bool) error {
	if plan.Root == "" || !filepath.IsAbs(plan.Root) || filepath.Clean(plan.Root) != plan.Root || strings.ContainsRune(plan.Root, '\x00') || !utf8.ValidString(plan.Root) {
		return errors.New("invalid policy activation root")
	}
	if shared && !sharedHex40Or64.MatchString(plan.HEAD) {
		return errors.New("shared policy activation requires a valid HEAD")
	}
	if !shared && plan.HEAD != "" && !sharedHex40Or64.MatchString(plan.HEAD) {
		return errors.New("invalid local policy activation HEAD")
	}
	for name, value := range map[string]string{"snapshot": plan.Snapshot, "targetJournal": plan.TargetJournal, "targetIds": plan.TargetIDs} {
		if !sharedHex64.MatchString(value) {
			return fmt.Errorf("invalid policy activation %s", name)
		}
	}
	if !sharedHex64.MatchString(plan.OriginalIDs) && !(plan.ModuleAdoption != nil && plan.OriginalIDs == "") {
		return errors.New("invalid original ID ledger hash")
	}
	if plan.IDTarget == nil || len(plan.IDTarget) > maxIDsBytes {
		return errors.New("policy activation ID target must be a bounded non-null byte string")
	}
	if plan.ModuleAdoption != nil {
		if err := validateModuleIDPlan(plan, shared); err != nil {
			return err
		}
	} else if len(plan.IDTarget) == 0 {
		if plan.OriginalIDs != plan.TargetIDs {
			return errors.New("changed ID target requires durable bytes")
		}
	} else {
		if !shared {
			return errors.New("local policy activation cannot change IDs")
		}
		ledger, err := decodeIDs(plan.IDTarget)
		if err != nil || ledger.SchemaVersion != 3 || bytesDigest(plan.IDTarget) != plan.TargetIDs {
			return errors.New("invalid policy activation ID target")
		}
	}
	for name, value := range map[string]string{"originalJournal": plan.OriginalJournal, "originalPolicy": plan.OriginalPolicy, "originalActivation": plan.OriginalActivation} {
		if value != "" && !sharedHex64.MatchString(value) {
			return fmt.Errorf("invalid policy activation %s", name)
		}
	}
	return nil
}

func validateSharedPolicyAuthority(authority sharedPolicyAuthority) error {
	if !sharedHex32.MatchString(authority.AuthorityID) || (authority.Phase != "initializing" && authority.Phase != "joining" && authority.Phase != "active" && authority.Phase != "revising") {
		return errors.New("invalid shared policy authority")
	}
	if authority.Phase == "revising" && authority.Revision == nil {
		return errors.New("shared revision requires previous binding")
	}
	if authority.Revision != nil {
		if authority.Phase == "initializing" {
			return errors.New("policy revision cannot be an initial activation")
		}
		if err := validatePolicyRevision(*authority.Revision, authority.AuthorityID, authority.Digest); err != nil {
			return err
		}
	}
	if len(authority.Canonical) == 0 || !utf8.Valid(authority.Canonical) {
		return errors.New("invalid shared authority canonical policy")
	}
	p, err := boardpolicy.Parse(authority.Canonical)
	if err != nil {
		return err
	}
	canonical, err := p.Canonical()
	if err != nil || !bytes.Equal(canonical, authority.Canonical) || bytesDigest(authority.Canonical) != authority.Digest || !sharedHex64.MatchString(authority.Digest) {
		return errors.New("shared authority policy digest mismatch")
	}
	if authority.Pending == nil {
		return errors.New("shared authority pending must be a non-null array")
	}
	if authority.Phase == "active" && len(authority.Pending) != 0 {
		return errors.New("active authority requires no pending plans")
	}
	if (authority.Phase == "initializing" || authority.Phase == "revising") && (len(authority.Pending) == 0 || len(authority.Pending) > 256) {
		return errors.New("initializing authority requires 1..256 pending plans")
	}
	seen := map[string]bool{}
	for _, plan := range authority.Pending {
		if err := validatePolicyActivationPlan(plan, true); err != nil {
			return err
		}
		if seen[plan.Root] {
			return errors.New("duplicate shared authority root")
		}
		seen[plan.Root] = true
	}
	for i := 1; i < len(authority.Pending); i++ {
		if authority.Pending[i-1].Root >= authority.Pending[i].Root {
			return errors.New("shared authority pending roots must be sorted")
		}
	}
	if authority.Phase == "joining" && len(authority.Pending) != 1 {
		return errors.New("joining authority requires one pending plan")
	}
	return nil
}

func validatePolicyActivationShape(raw []byte) error {
	return validateRevisionEnvelope(raw, false)
}
func validateSharedPolicyShape(raw json.RawMessage) error {
	return validateRevisionEnvelope(raw, true)
}

var policyPlanKeys = []string{"root", "head", "snapshot", "originalJournal", "targetJournal", "originalPolicy", "originalActivation", "originalIds", "targetIds", "idTarget"}

func validateObjectShape(raw []byte, keys []string, nested []string) error {
	if !utf8.Valid(raw) {
		return errors.New("invalid UTF-8")
	}
	if err := rejectJSONSurrogates(raw); err != nil {
		return err
	}
	if err := rejectDuplicateJSON(raw); err != nil {
		return err
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return err
	}
	if len(obj) != len(keys) {
		return errors.New("object requires exact fields")
	}
	allowed := map[string]bool{}
	for _, key := range keys {
		allowed[key] = true
		if value, ok := obj[key]; !ok || string(value) == "null" {
			return fmt.Errorf("missing or null field %q", key)
		}
	}
	for key := range obj {
		if !allowed[key] {
			return fmt.Errorf("unknown field %q", key)
		}
	}
	for _, key := range keys {
		if key == "schemaVersion" {
			var version int
			if err := json.Unmarshal(obj[key], &version); err != nil {
				return fmt.Errorf("field %q must be an integer", key)
			}
			continue
		}
		if key == "plan" || key == "pending" {
			continue
		}
		if err := requireJSONString(obj[key], key); err != nil {
			return err
		}
	}
	if plan, ok := obj["plan"]; ok {
		return validatePlanShape(plan, nested)
	}
	if pending, ok := obj["pending"]; ok {
		var rows []json.RawMessage
		if err := json.Unmarshal(pending, &rows); err != nil || rows == nil {
			return errors.New("pending must be a non-null array")
		}
		for _, row := range rows {
			if err := validatePlanShape(row, nested); err != nil {
				return err
			}
		}
	}
	return nil
}

func rejectJSONSurrogates(raw []byte) error {
	inString := false
	for i := 0; i < len(raw); i++ {
		if !inString {
			if raw[i] == '"' {
				inString = true
			}
			continue
		}
		if raw[i] == '"' {
			inString = false
			continue
		}
		if raw[i] != '\\' {
			continue
		}
		if i+1 >= len(raw) {
			continue
		}
		if raw[i+1] != 'u' {
			i++
			continue
		}
		if i+5 >= len(raw) {
			continue
		}
		value, ok := jsonHex4(raw[i+2 : i+6])
		if !ok || value < 0xd800 || value > 0xdfff {
			continue
		}
		if value >= 0xdc00 {
			return errors.New("unpaired JSON low surrogate")
		}
		if i+11 >= len(raw) || raw[i+6] != '\\' || raw[i+7] != 'u' {
			return errors.New("unpaired JSON high surrogate")
		}
		low, ok := jsonHex4(raw[i+8 : i+12])
		if !ok || low < 0xdc00 || low > 0xdfff {
			return errors.New("unpaired JSON high surrogate")
		}
		i += 11
	}
	return nil
}

func jsonHex4(raw []byte) (int, bool) {
	if len(raw) != 4 {
		return 0, false
	}
	value := 0
	for _, c := range raw {
		value <<= 4
		switch {
		case c >= '0' && c <= '9':
			value += int(c - '0')
		case c >= 'a' && c <= 'f':
			value += int(c-'a') + 10
		case c >= 'A' && c <= 'F':
			value += int(c-'A') + 10
		default:
			return 0, false
		}
	}
	return value, true
}

func requireJSONString(raw json.RawMessage, field string) error {
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return fmt.Errorf("field %q must be a string", field)
	}
	return nil
}

func validatePlanShape(raw []byte, keys []string) error {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return err
	}
	if adoption, exists := obj["moduleAdoption"]; exists {
		if err := validateModuleAdoptionShape(adoption); err != nil {
			return err
		}
		delete(obj, "moduleAdoption")
	}
	if len(obj) != len(keys) {
		return errors.New("invalid policy activation plan shape")
	}
	allowed := map[string]bool{}
	for _, key := range keys {
		allowed[key] = true
		value, ok := obj[key]
		if !ok || string(value) == "null" {
			return fmt.Errorf("missing or null plan field %q", key)
		}
		var text string
		if err := json.Unmarshal(value, &text); err != nil {
			return fmt.Errorf("plan field %q must be a string", key)
		}
	}
	for key := range obj {
		if !allowed[key] {
			return fmt.Errorf("unknown plan field %q", key)
		}
	}
	return nil
}

func decodeExact(raw []byte, target any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("JSON has trailing content")
	}
	return nil
}
