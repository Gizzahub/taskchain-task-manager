package taskstore

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/Gizzahub/taskchain-task-manager/internal/boardpolicy"
	"github.com/Gizzahub/taskchain-task-manager/internal/card"
)

func loadTransitions(r *os.Root) (transitionJournal, error) {
	j, err := loadTransitionsForBundle(r)
	if err != nil {
		return transitionJournal{}, err
	}
	if err := checkBundleGate(r, j); err != nil {
		return transitionJournal{}, err
	}
	return j, nil
}

// Bundle admission/recovery must validate its own journal under the same
// session. Ordinary callers must use loadTransitions, including receipt replay.
func loadTransitionsForBundle(r *os.Root) (transitionJournal, error) {
	j, err := loadTransitionsForStorage(r)
	if err != nil {
		return transitionJournal{}, err
	}
	if err := checkStorageGate(r, j); err != nil {
		return transitionJournal{}, err
	}
	return j, nil
}

// Only the storage recovery session bypasses the storage pending gate. It
// validates its exact request and both journals before publishing anything.
func loadTransitionsForStorage(r *os.Root) (transitionJournal, error) {
	info, err := r.Lstat(transitionsFile)
	if errors.Is(err, fs.ErrNotExist) {
		j := transitionJournal{SchemaVersion: 1, Records: []transitionRecord{}}
		if _, policyErr := policyForJournal(r, j); policyErr != nil {
			return transitionJournal{}, policyErr
		}
		return j, nil
	}
	if err != nil {
		return transitionJournal{}, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return transitionJournal{}, errors.New("transition journal is not regular")
	}
	if info.Size() > maxTransitionBytes {
		return transitionJournal{}, errors.New("transition journal exceeds 8 MiB")
	}
	f, err := r.Open(transitionsFile)
	if err != nil {
		return transitionJournal{}, err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, maxTransitionBytes+1))
	if err != nil {
		return transitionJournal{}, err
	}
	j, err := decodeTransitionJournal(raw)
	if err != nil {
		return transitionJournal{}, err
	}
	if j.StorageProtocol == 6 {
		if err := ownerRejoinAdmitsProtocol6(r); err != nil {
			return transitionJournal{}, err
		}
	}
	policy, err := policyForJournal(r, j)
	if err != nil {
		return transitionJournal{}, err
	}
	if err := validateTransitionRecords(j, policy); err != nil {
		return transitionJournal{}, err
	}
	return j, nil
}

// decodeTransitionJournal validates the wire format without consulting mutable
// policy files. Activation recovery must first prove the original/target hashes,
// then validate records against the explicitly bound policy. Ordinary callers
// must still use loadTransitions or loadTransitionsForBundle.
func decodeTransitionJournal(raw []byte) (transitionJournal, error) {
	if !utf8.Valid(raw) {
		return transitionJournal{}, errors.New("transition journal invalid UTF-8")
	}
	if len(raw) > maxTransitionBytes {
		return transitionJournal{}, errors.New("transition journal exceeds 8 MiB")
	}
	if err := rejectDuplicateJSON(raw); err != nil {
		return transitionJournal{}, err
	}
	if err := validateTransitionShape(raw); err != nil {
		return transitionJournal{}, err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var j transitionJournal
	if err := dec.Decode(&j); err != nil {
		return transitionJournal{}, err
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return transitionJournal{}, errors.New("transition journal trailing content")
	}
	if (j.SchemaVersion < 1 || j.SchemaVersion > 4) || j.Records == nil {
		return transitionJournal{}, errors.New("invalid transition journal schema")
	}
	if j.SchemaVersion >= 2 && !sharedHex64.MatchString(j.PolicyDigest) {
		return transitionJournal{}, errors.New("invalid transition policy digest")
	}
	if _, err := transitionPolicyHistory(j); err != nil {
		return transitionJournal{}, err
	}
	return j, nil
}

func validateTransitionRecords(j transitionJournal, policy boardpolicy.Policy) error {
	history, err := transitionPolicyHistory(j)
	if err != nil {
		return err
	}
	if j.SchemaVersion == 4 {
		digest, err := policy.Digest()
		if err != nil || digest != j.PolicyDigest {
			return errors.New("active policy differs from journal history binding")
		}
	}
	if j.SchemaVersion == 1 {
		// Legacy receipts never acquire the semantics of a newly selected policy.
		policy = boardpolicy.Default()
	}
	pending := 0
	seen := map[string]bool{}
	for _, rec := range j.Records {
		if rec.RequestID == "" || seen[rec.RequestID] {
			return errors.New("duplicate transition request ID")
		}
		seen[rec.RequestID] = true
		if rec.Kind == "pending" {
			pending++
		} else if rec.Kind != "completed" {
			return errors.New("invalid transition record")
		}
		if err := validateBoundRecord(j, rec, policy, history); err != nil {
			return err
		}
	}
	if pending > 1 {
		return errors.New("multiple pending transitions")
	}
	return nil
}

func validateTransitionShape(raw []byte) error {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(raw, &root); err != nil {
		return err
	}
	if root["schemaVersion"] == nil || root["records"] == nil || string(root["records"]) == "null" {
		return errors.New("transition journal requires schemaVersion and records")
	}
	var version int
	if err := json.Unmarshal(root["schemaVersion"], &version); err != nil {
		return errors.New("transition schemaVersion must be integer")
	}
	if version < 1 || version > 4 {
		return errors.New("unsupported transition journal schema")
	}
	for key := range root {
		if key != "schemaVersion" && key != "records" && key != "policyDigest" && key != "bundleProtocol" && key != "policyAuthority" && key != "storageProtocol" && key != "policyHistory" {
			return fmt.Errorf("unknown transition journal field %q", key)
		}
	}
	extra := 0
	if version == 4 {
		if err := validatePolicyHistoryShape(root["policyHistory"]); err != nil {
			return err
		}
		extra++
	} else if _, present := root["policyHistory"]; present {
		return errors.New("legacy journal cannot contain policy history")
	}
	if raw, ok := root["storageProtocol"]; ok {
		var protocol int
		if err := json.Unmarshal(raw, &protocol); err != nil || protocol < 1 || protocol > 6 {
			return errors.New("unsupported storage protocol")
		}
		extra++
	}
	if raw, ok := root["bundleProtocol"]; ok {
		var protocol int
		if err := json.Unmarshal(raw, &protocol); err != nil || protocol != 1 {
			return errors.New("unsupported bundle protocol")
		}
		extra++
	}
	if version == 1 && len(root) != 2+extra {
		return errors.New("legacy transition journal cannot contain policyDigest")
	}
	if version >= 3 {
		if err := validatePolicyAuthorityShape(root["policyAuthority"]); err != nil {
			return err
		}
		extra++
	} else if _, present := root["policyAuthority"]; present {
		return errors.New("legacy journal cannot contain policy authority")
	}
	if version >= 2 {
		if len(root) != 3+extra || root["policyDigest"] == nil || string(root["policyDigest"]) == "null" {
			return errors.New("bound transition journal requires policyDigest")
		}
		var digest string
		if err := json.Unmarshal(root["policyDigest"], &digest); err != nil || !sharedHex64.MatchString(digest) {
			return errors.New("invalid transition policy digest")
		}
	}
	var records []map[string]json.RawMessage
	if err := json.Unmarshal(root["records"], &records); err != nil {
		return errors.New("transition records must be an array of objects")
	}
	allowed := map[string]bool{"kind": true, "requestId": true, "id": true, "owner": true, "token": true, "from": true, "to": true, "source": true, "target": true, "mode": true, "original": true, "patched": true, "status": true, "policyDigest": true}
	for _, record := range records {
		if version == 1 {
			if _, present := record["policyDigest"]; present {
				return errors.New("legacy transition record cannot contain policyDigest")
			}
		}
		if version >= 2 {
			if kindRaw, ok := record["kind"]; ok {
				var kind string
				_ = json.Unmarshal(kindRaw, &kind)
				if kind == "pending" && (record["policyDigest"] == nil || string(record["policyDigest"]) == "null") {
					return errors.New("pending transition policy digest is missing")
				}
				if digestRaw, ok := record["policyDigest"]; ok {
					var digest string
					if err := json.Unmarshal(digestRaw, &digest); err != nil || !sharedHex64.MatchString(digest) {
						return errors.New("invalid transition record policy digest")
					}
				}
			}
		}
		for key := range record {
			if !allowed[key] {
				return fmt.Errorf("unknown transition record field %q", key)
			}
		}
		base := []string{"kind", "requestId", "id", "owner", "token", "from", "to", "target", "source", "mode", "status"}
		for _, key := range base {
			if record[key] == nil {
				return fmt.Errorf("missing transition record field %q", key)
			}
		}
		if string(record["mode"]) == "null" {
			return errors.New("transition record mode is null")
		}
		var kind string
		if err := json.Unmarshal(record["kind"], &kind); err != nil {
			return errors.New("transition record kind must be string")
		}
		if kind == "pending" {
			if record["original"] == nil || record["patched"] == nil {
				return errors.New("pending transition payload is missing")
			}
		}
	}
	return nil
}

func validateTransitionRecord(rec transitionRecord) error {
	return validateTransitionRecordWithPolicy(rec, boardpolicy.Default())
}

func validateBoundRecord(j transitionJournal, rec transitionRecord, policy boardpolicy.Policy, history map[string]boardpolicy.Policy) error {
	if j.SchemaVersion >= 2 {
		if rec.PolicyDigest == "" && rec.Kind == "completed" {
			// Carried-over receipts retain the semantics of the legacy journal.
			return validateTransitionRecord(rec)
		}
		if j.SchemaVersion == 4 && rec.Kind == "completed" {
			p, ok := history[rec.PolicyDigest]
			if !ok {
				return errors.New("completed transition historical policy is missing")
			}
			return validateTransitionRecordWithPolicy(rec, p)
		}
		if rec.PolicyDigest != j.PolicyDigest {
			return errors.New("transition record policy digest mismatch")
		}
	}
	return validateTransitionRecordWithPolicy(rec, policy)
}

func validateTransitionRecordWithPolicy(rec transitionRecord, policy boardpolicy.Policy) error {
	if !claimToken.MatchString(rec.RequestID) || !validClaimID(rec.ID) || !validClaimOwner(rec.Owner) || !claimToken.MatchString(rec.Token) || !policy.Allows(rec.From, rec.To) || !policy.Workflow(rec.To) || (!policy.Workflow(rec.From) && !policy.Parked(rec.From)) || rec.From == rec.To {
		return errors.New("invalid transition record authority")
	}
	expectedTarget, pathErr := transitionTargetPath(rec.Source, rec.From, rec.To, policy)
	if pathErr != nil || rec.Target != expectedTarget || filepath.Base(rec.Source) != filepath.Base(rec.Target) || strings.HasPrefix(filepath.Base(rec.Source), ".") || filepath.Ext(rec.Source) != ".md" || strings.EqualFold(filepath.Base(rec.Source), "README.md") {
		return errors.New("invalid transition record paths")
	}
	if filepath.IsAbs(rec.Source) || filepath.IsAbs(rec.Target) || strings.ContainsAny(rec.Source+rec.Target, "\\\x00") || filepath.Clean(rec.Source) != rec.Source || filepath.Clean(rec.Target) != rec.Target {
		return errors.New("unsafe transition record path")
	}
	if rec.Mode&^uint32(0o777) != 0 {
		return errors.New("unsafe transition mode")
	}
	if rec.Kind == "pending" {
		if len(rec.Original) == 0 || len(rec.Patched) == 0 || len(rec.Original) > maxCardBytes || len(rec.Patched) > maxCardBytes || rec.Status != "pending" {
			return errors.New("invalid pending transition record")
		}
		doc, err := card.Parse(rec.Original)
		if err != nil {
			return errors.New("invalid original card in transition record")
		}
		if !sameIdentity(doc.View().ID, rec.ID) {
			return errors.New("transition record ID does not match card")
		}
		status, ok := policy.Status(rec.To)
		if !ok {
			return errors.New("transition record destination has no status")
		}
		patched, _, err := doc.SetStatusCell(status)
		if err != nil || !bytes.Equal(patched, rec.Patched) {
			return errors.New("patched card does not match original transition")
		}
	} else if rec.Original != nil || rec.Patched != nil || rec.Status != "completed" {
		return errors.New("invalid completed transition record")
	}
	return nil
}

func rejectPendingTransitions(r *os.Root) error {
	j, err := loadTransitions(r)
	if err != nil {
		return err
	}
	for _, rec := range j.Records {
		if rec.Kind == "pending" {
			return errors.New("pending transition requires retry or recovery")
		}
	}
	return nil
}

func publishTransitionJournal(r *os.Root, j transitionJournal) error {
	policy, err := policyForJournal(r, j)
	if err != nil {
		return err
	}
	raw, err := encodeTransitionJournal(j, policy)
	if err != nil {
		return err
	}
	name, err := stage(r, raw)
	if err != nil {
		return err
	}
	if err := r.Rename(name, transitionsFile); err != nil {
		return errors.Join(err, r.Remove(name))
	}
	return nil
}

// encodeTransitionJournal computes the exact target bytes before activation
// publishes any files. It does not authorize a policy or write a journal.
func encodeTransitionJournal(j transitionJournal, policy boardpolicy.Policy) ([]byte, error) {
	shapeRaw, err := json.Marshal(j)
	if err != nil {
		return nil, err
	}
	if err := validateTransitionShape(shapeRaw); err != nil {
		return nil, err
	}
	digest, err := policy.Digest()
	if err != nil {
		return nil, err
	}
	if j.SchemaVersion >= 2 && digest != j.PolicyDigest {
		return nil, errors.New("transition target policy digest mismatch")
	}
	if err := validateTransitionRecords(j, policy); err != nil {
		return nil, err
	}
	if err := validateTransitionCapacity(j); err != nil {
		return nil, err
	}
	raw, err := json.MarshalIndent(j, "", "  ")
	if err != nil {
		return nil, err
	}
	raw = append(raw, '\n')
	if len(raw) > maxTransitionBytes {
		return nil, errors.New("transition journal exceeds 8 MiB")
	}
	return raw, nil
}

func validateTransitionCapacity(j transitionJournal) error {
	encode := func(value transitionJournal) (int, error) {
		raw, err := json.MarshalIndent(value, "", "  ")
		if err != nil {
			return 0, err
		}
		return len(raw) + 1, nil
	}
	n, err := encode(j)
	if err != nil {
		return err
	}
	if n > maxTransitionBytes {
		return errors.New("transition journal exceeds 8 MiB")
	}
	completed := j
	completed.Records = append([]transitionRecord(nil), j.Records...)
	for i := range completed.Records {
		if completed.Records[i].Kind == "pending" {
			completed.Records[i].Kind = "completed"
			completed.Records[i].Status = "completed"
			completed.Records[i].Original = nil
			completed.Records[i].Patched = nil
		}
	}
	n, err = encode(completed)
	if err != nil {
		return err
	}
	if n > maxTransitionBytes {
		return errors.New("transition journal has insufficient completion capacity")
	}
	return nil
}
