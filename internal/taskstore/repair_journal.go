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

const (
	repairsFile        = ".task-manager-repairs.json"
	maxRepairsBytes    = 8 << 20
	maxRepairCardBytes = 1 << 20
)

type repairRecord struct {
	Kind            string `json:"kind"`
	RequestID       string `json:"requestId"`
	ID              string `json:"id"`
	Owner           string `json:"owner"`
	Token           string `json:"token"`
	Path            string `json:"path"`
	ExpectedSHA256  string `json:"expectedSha256"`
	Mode            uint32 `json:"mode"`
	Original        []byte `json:"original,omitempty"`
	Patched         []byte `json:"patched,omitempty"`
	PolicyDigest    string `json:"policyDigest"`
	BoardPath       string `json:"boardPath"`
	Namespace       string `json:"namespace,omitempty"`
	CanonicalStatus string `json:"canonicalStatus"`
	Changed         bool   `json:"changed"`
}

type repairJournal struct {
	SchemaVersion int            `json:"schemaVersion"`
	BoardPath     string         `json:"boardPath"`
	Records       []repairRecord `json:"records"`
}

func loadRepairJournal(r *os.Root) (repairJournal, error) {
	info, err := r.Lstat(repairsFile)
	if errors.Is(err, fs.ErrNotExist) {
		return repairJournal{}, fs.ErrNotExist
	}
	if err != nil {
		return repairJournal{}, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return repairJournal{}, errors.New("repair journal is not regular")
	}
	if info.Size() > maxRepairsBytes {
		return repairJournal{}, errors.New("repair journal exceeds 8 MiB")
	}
	f, err := r.Open(repairsFile)
	if err != nil {
		return repairJournal{}, err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, maxRepairsBytes+1))
	if err != nil {
		return repairJournal{}, err
	}
	return decodeRepairJournal(raw)
}

func decodeRepairJournal(raw []byte) (repairJournal, error) {
	if len(raw) > maxRepairsBytes || !utf8.Valid(raw) {
		return repairJournal{}, errors.New("repair journal invalid size or UTF-8")
	}
	if err := rejectDuplicateJSON(raw); err != nil {
		return repairJournal{}, err
	}
	var shape map[string]json.RawMessage
	if err := json.Unmarshal(raw, &shape); err != nil {
		return repairJournal{}, err
	}
	if len(shape) != 3 || shape["schemaVersion"] == nil || shape["boardPath"] == nil || shape["records"] == nil {
		return repairJournal{}, errors.New("repair journal requires schemaVersion and records")
	}
	var j repairJournal
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&j); err != nil {
		return repairJournal{}, err
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return repairJournal{}, errors.New("repair journal trailing content")
	}
	if j.SchemaVersion != 1 || j.Records == nil || !canonicalBoardPath(j.BoardPath) {
		return repairJournal{}, errors.New("invalid repair journal schema")
	}
	var recordShapes []json.RawMessage
	if err := json.Unmarshal(shape["records"], &recordShapes); err != nil {
		return repairJournal{}, errors.New("repair records must be an array")
	}
	if len(recordShapes) != len(j.Records) {
		return repairJournal{}, errors.New("repair records shape mismatch")
	}
	seen := map[string]bool{}
	pending := 0
	for i, rec := range j.Records {
		if err := validateRepairRecordShape(recordShapes[i], rec.Kind == "pending"); err != nil {
			return repairJournal{}, err
		}
		if seen[rec.RequestID] {
			return repairJournal{}, errors.New("duplicate repair request ID")
		}
		seen[rec.RequestID] = true
		if err := validateRepairRecord(rec); err != nil {
			return repairJournal{}, err
		}
		if rec.BoardPath != j.BoardPath {
			return repairJournal{}, errors.New("repair record board binding mismatch")
		}
		if rec.Kind == "pending" {
			pending++
			if pending > 1 {
				return repairJournal{}, errors.New("repair journal has multiple pending records")
			}
		}
	}
	return j, nil
}

func validateRepairRecordShape(raw json.RawMessage, pending bool) error {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return errors.New("repair record must be an object")
	}
	allowed := map[string]bool{"kind": true, "requestId": true, "id": true, "owner": true, "token": true, "path": true, "expectedSha256": true, "mode": true, "policyDigest": true, "boardPath": true, "namespace": true, "canonicalStatus": true, "changed": true, "original": true, "patched": true}
	for k := range m {
		if !allowed[k] {
			return fmt.Errorf("unknown repair record field %q", k)
		}
		if bytes.Equal(bytes.TrimSpace(m[k]), []byte("null")) {
			return fmt.Errorf("repair record field %q is null", k)
		}
	}
	required := []string{"kind", "requestId", "id", "owner", "token", "path", "expectedSha256", "mode", "policyDigest", "boardPath", "canonicalStatus", "changed"}
	for _, k := range required {
		if m[k] == nil {
			return fmt.Errorf("repair record missing %s", k)
		}
	}
	for _, k := range []string{"kind", "requestId", "id", "owner", "token", "path", "expectedSha256", "policyDigest", "boardPath", "canonicalStatus"} {
		if len(bytes.TrimSpace(m[k])) == 0 || bytes.TrimSpace(m[k])[0] != '"' {
			return fmt.Errorf("repair record %s must be a string", k)
		}
	}
	if !pending {
		if m["original"] != nil || m["patched"] != nil {
			return errors.New("completed repair retains payload fields")
		}
	} else if m["original"] == nil || m["patched"] == nil {
		return errors.New("pending repair requires payload fields")
	}
	return nil
}

func validateRepairRecord(rec repairRecord) error {
	if rec.Kind != "pending" && rec.Kind != "completed" {
		return errors.New("invalid repair record status")
	}
	if !claimToken.MatchString(rec.RequestID) || !validClaimID(rec.ID) || !isWorkTask(rec.ID) || !validClaimOwner(rec.Owner) {
		return errors.New("invalid repair record identity")
	}
	if rec.Token != "" && !claimToken.MatchString(rec.Token) {
		return errors.New("invalid repair record token")
	}
	if !sharedHex64.MatchString(rec.ExpectedSHA256) || !sharedHex64.MatchString(rec.PolicyDigest) || rec.CanonicalStatus == "" {
		return errors.New("invalid repair digest")
	}
	if !isTopLevelWorkflowPath(rec.Path, boardpolicy.Default()) {
		return errors.New("invalid repair path")
	}
	status, ok := boardpolicy.Default().Status(filepath.Dir(rec.Path))
	if !ok || rec.CanonicalStatus != status {
		return errors.New("repair status does not match workflow path")
	}
	if !canonicalBoardPath(rec.BoardPath) {
		return errors.New("invalid repair board binding")
	}
	if rec.Namespace != "" && !sharedHex32.MatchString(rec.Namespace) {
		return errors.New("invalid repair namespace")
	}
	if rec.Mode == 0 || rec.Mode&^0777 != 0 || len(rec.Original) > maxRepairCardBytes || len(rec.Patched) > maxRepairCardBytes {
		return errors.New("invalid repair payload or mode")
	}
	if rec.Kind == "pending" && len(rec.Original) == 0 {
		return errors.New("pending repair has no original bytes")
	}
	if rec.Kind == "pending" {
		if bytesDigest(rec.Original) != rec.ExpectedSHA256 {
			return errors.New("repair original digest mismatch")
		}
		doc, err := card.Parse(rec.Original)
		if err != nil {
			return fmt.Errorf("parse repair original: %w", err)
		}
		if !sameIdentity(doc.View().ID, rec.ID) {
			return errors.New("repair original ID mismatch")
		}
		patched, changed, err := doc.SetStatusCell(rec.CanonicalStatus)
		if err != nil {
			return err
		}
		if !bytes.Equal(patched, rec.Patched) || changed != rec.Changed {
			return errors.New("repair patched payload mismatch")
		}
	}
	if rec.Kind == "completed" && (len(rec.Original) != 0 || len(rec.Patched) != 0) {
		return errors.New("completed repair retains payload")
	}
	return nil
}

func saveRepairJournal(r *os.Root, j repairJournal, initial bool) error {
	if j.SchemaVersion != 1 || j.Records == nil || !canonicalBoardPath(j.BoardPath) {
		return errors.New("invalid repair journal")
	}
	for _, rec := range j.Records {
		if err := validateRepairRecord(rec); err != nil {
			return err
		}
		if rec.BoardPath != j.BoardPath {
			return errors.New("repair record board binding mismatch")
		}
	}
	raw, err := json.Marshal(j)
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	if len(raw) > maxRepairsBytes {
		return errors.New("repair journal exceeds 8 MiB")
	}
	if _, err := decodeRepairJournal(raw); err != nil {
		return err
	}
	completed := j
	completed.Records = append([]repairRecord(nil), j.Records...)
	for i := range completed.Records {
		if completed.Records[i].Kind == "pending" {
			completed.Records[i].Kind = "completed"
			completed.Records[i].Original = nil
			completed.Records[i].Patched = nil
		}
	}
	reserve, err := json.Marshal(completed)
	if err != nil {
		return err
	}
	reserve = append(reserve, '\n')
	if len(reserve) > maxRepairsBytes {
		return errors.New("repair journal completion capacity exceeded")
	}
	name, err := stage(r, raw)
	if err != nil {
		return err
	}
	if initial {
		if err := r.Link(name, repairsFile); err != nil {
			return errors.Join(err, r.Remove(name))
		}
		return errors.Join(r.Remove(name), syncRoot(r))
	}
	if err := r.Rename(name, repairsFile); err != nil {
		return errors.Join(err, r.Remove(name))
	}
	return syncRoot(r)
}

func syncRoot(r *os.Root) error {
	f, err := r.Open(".")
	if err != nil {
		return err
	}
	return errors.Join(f.Sync(), f.Close())
}

func canonicalBoardPath(p string) bool {
	return p != "" && filepath.IsAbs(p) && filepath.Clean(p) == p && !strings.ContainsAny(p, "\\\x00")
}
