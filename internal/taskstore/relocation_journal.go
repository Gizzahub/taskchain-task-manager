package taskstore

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"unicode/utf8"

	"github.com/Gizzahub/taskchain-task-manager/internal/boardpolicy"
)

const relocationsFile = ".task-manager-relocations.json"

type relocationRecord struct {
	Kind            string `json:"kind"`
	RequestID       string `json:"requestId"`
	ID              string `json:"id"`
	Owner           string `json:"owner"`
	Token           string `json:"token"`
	Source          string `json:"source"`
	Target          string `json:"target"`
	ExpectedSHA256  string `json:"expectedSha256"`
	Mode            uint32 `json:"mode"`
	Original        []byte `json:"original,omitempty"`
	Patched         []byte `json:"patched,omitempty"`
	PolicyDigest    string `json:"policyDigest"`
	PolicyCanonical []byte `json:"policyCanonical"`
	BoardPath       string `json:"boardPath"`
	Namespace       string `json:"namespace,omitempty"`
	Changed         bool   `json:"changed"`
}

type relocationJournal struct {
	SchemaVersion int                `json:"schemaVersion"`
	BoardPath     string             `json:"boardPath"`
	Records       []relocationRecord `json:"records"`
}

func loadRelocationJournal(r *os.Root) (relocationJournal, error) {
	raw, err := boundedSnapshotFile(r, relocationsFile, maxRepairsBytes)
	if err != nil {
		return relocationJournal{}, err
	}
	return decodeRelocationJournal(raw)
}

func decodeRelocationJournal(raw []byte) (relocationJournal, error) {
	var j relocationJournal
	if len(raw) > maxRepairsBytes || !utf8.Valid(raw) {
		return j, errors.New("relocation journal invalid size or UTF-8")
	}
	if err := rejectJSONSurrogates(raw); err != nil {
		return j, err
	}
	if err := rejectDuplicateJSON(raw); err != nil {
		return j, err
	}
	if err := relocationShape(raw, []string{"schemaVersion", "boardPath", "records"}, nil); err != nil {
		return j, err
	}
	if err := json.Unmarshal(raw, &j); err != nil {
		return j, err
	}
	if j.SchemaVersion != 1 || !canonicalBoardPath(j.BoardPath) || j.Records == nil {
		return j, errors.New("invalid relocation journal binding or schema")
	}
	var shape struct{ Records []json.RawMessage }
	if err := json.Unmarshal(raw, &shape); err != nil {
		return j, err
	}
	seen := map[string]bool{}
	pending := 0
	for i, rec := range j.Records {
		required := []string{"kind", "requestId", "id", "owner", "token", "source", "target", "expectedSha256", "mode", "policyDigest", "policyCanonical", "boardPath", "changed"}
		if rec.Kind == "pending" {
			required = append(required, "original", "patched")
			pending++
		}
		if err := relocationShape(shape.Records[i], required, []string{"namespace"}); err != nil {
			return j, err
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(shape.Records[i], &fields); err != nil {
			return j, err
		}
		for _, key := range []string{"policyCanonical", "original", "patched"} {
			if value, ok := fields[key]; ok && bytes.TrimSpace(value)[0] != '"' {
				return j, fmt.Errorf("relocation %s must be a base64 string", key)
			}
		}
		if seen[rec.RequestID] || pending > 1 || rec.BoardPath != j.BoardPath {
			return j, errors.New("duplicate relocation request, multiple pending operations or board mismatch")
		}
		seen[rec.RequestID] = true
		if err := validateRelocationRecord(rec); err != nil {
			return j, err
		}
	}
	return j, nil
}

func relocationShape(raw []byte, required, optional []string) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		return errors.New("relocation journal requires an object")
	}
	allowed := map[string]bool{}
	for _, key := range required {
		allowed[key] = true
		if fields[key] == nil {
			return fmt.Errorf("relocation journal missing %s", key)
		}
	}
	for _, key := range optional {
		allowed[key] = true
	}
	for key, value := range fields {
		if !allowed[key] || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return fmt.Errorf("unknown or null relocation field %s", key)
		}
	}
	return nil
}

func validateRelocationRecord(rec relocationRecord) error {
	if rec.Kind != "pending" && rec.Kind != "completed" {
		return errors.New("invalid relocation record status")
	}
	if !claimToken.MatchString(rec.RequestID) || identityKey(rec.ID) == "" || !validClaimOwner(rec.Owner) || (rec.Token != "" && !claimToken.MatchString(rec.Token)) {
		return errors.New("invalid relocation request identity")
	}
	if !sharedHex64.MatchString(rec.ExpectedSHA256) || !sharedHex64.MatchString(rec.PolicyDigest) || !canonicalBoardPath(rec.BoardPath) || (rec.Namespace != "" && !sharedHex32.MatchString(rec.Namespace)) {
		return errors.New("invalid relocation digest or board binding")
	}
	if rec.Mode == 0 || rec.Mode&^0777 != 0 || len(rec.Original) > maxCardBytes || len(rec.Patched) > maxCardBytes {
		return errors.New("invalid relocation mode or payload size")
	}
	policy, err := boardpolicy.Parse(rec.PolicyCanonical)
	if err != nil {
		return err
	}
	canonical, err := policy.Canonical()
	if err != nil {
		return err
	}
	if !bytes.Equal(canonical, rec.PolicyCanonical) || bytesDigest(canonical) != rec.PolicyDigest {
		return errors.New("relocation policy is not the exact canonical binding")
	}
	if _, _, err := relocationZones(rec.Source, rec.Target, policy); err != nil {
		return err
	}
	if rec.Kind == "pending" {
		if bytesDigest(rec.Original) != rec.ExpectedSHA256 {
			return errors.New("relocation original digest mismatch")
		}
		patched, changed, err := prepareRelocationPatch(rec.Original, rec.ID, rec.Source, rec.Target, policy)
		if err != nil {
			return err
		}
		if !bytes.Equal(patched, rec.Patched) || changed != rec.Changed {
			return errors.New("relocation patch differs from bound policy")
		}
	} else if len(rec.Original) != 0 || len(rec.Patched) != 0 {
		return errors.New("completed relocation retains payload")
	}
	return nil
}

func completedRelocationJournal(j relocationJournal) relocationJournal {
	j.Records = append([]relocationRecord(nil), j.Records...)
	for i := range j.Records {
		if j.Records[i].Kind == "pending" {
			j.Records[i].Kind = "completed"
			j.Records[i].Original, j.Records[i].Patched = nil, nil
		}
	}
	return j
}

func relocationJournalBytes(j relocationJournal) ([]byte, error) {
	raw, err := json.Marshal(j)
	if err != nil {
		return nil, err
	}
	raw = append(raw, '\n')
	if _, err := decodeRelocationJournal(raw); err != nil {
		return nil, err
	}
	completed, err := json.Marshal(completedRelocationJournal(j))
	if err != nil {
		return nil, err
	}
	if len(completed)+1 > maxRepairsBytes {
		return nil, errors.New("relocation journal completion capacity exceeded")
	}
	return raw, nil
}

func saveRelocationJournal(r *os.Root, j relocationJournal, initial bool) error {
	raw, err := relocationJournalBytes(j)
	if err != nil {
		return err
	}
	if !initial {
		info, err := r.Lstat(relocationsFile)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return errors.New("relocation journal is not regular")
		}
	}
	name, err := stage(r, raw)
	if err != nil {
		return err
	}
	if initial {
		if err := r.Link(name, relocationsFile); err != nil {
			return errors.Join(err, r.Remove(name))
		}
		return errors.Join(r.Remove(name), syncRoot(r))
	}
	if err := r.Rename(name, relocationsFile); err != nil {
		return errors.Join(err, r.Remove(name))
	}
	return syncRoot(r)
}
