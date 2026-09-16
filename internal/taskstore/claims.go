package taskstore

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	claimsFile     = ".task-manager-claims.json"
	maxClaimsBytes = 1 << 20
)

var claimToken = regexp.MustCompile(`^[0-9a-f]{32}$`)

type ClaimRequest struct {
	ID    string
	Owner string
	Token string
}

type ClaimRecord struct {
	ID     string `json:"id"`
	Owner  string `json:"owner"`
	Token  string `json:"token"`
	Status string `json:"status"`
}

type claimsLedger struct {
	SchemaVersion int           `json:"schemaVersion"`
	Records       []ClaimRecord `json:"records"`
}

func Claim(dir string, req ClaimRequest) (record ClaimRecord, err error) {
	if err := validateClaimRequest(req); err != nil {
		return ClaimRecord{}, err
	}
	r, err := openBoard(dir)
	if err != nil {
		return ClaimRecord{}, err
	}
	defer r.Close()
	unlock, err := lock(r)
	if err != nil {
		return ClaimRecord{}, err
	}
	defer func() { err = errors.Join(err, unlock()) }()
	if err := rejectPendingTransitions(r); err != nil {
		return ClaimRecord{}, err
	}
	entries, err := listLocked(r)
	if err != nil {
		return ClaimRecord{}, err
	}
	ledger, err := loadClaims(r, entries)
	if err != nil {
		return ClaimRecord{}, err
	}
	for _, record := range ledger.Records {
		if record.Token != req.Token {
			continue
		}
		if record.ID == req.ID && record.Owner == req.Owner && record.Status == "held" {
			return record, nil
		}
		return ClaimRecord{}, fmt.Errorf("claim token already used")
	}
	for _, record := range ledger.Records {
		if record.Status == "held" && sameIdentity(record.ID, req.ID) {
			return ClaimRecord{}, fmt.Errorf("task %s is already claimed", req.ID)
		}
	}
	ready, err := readyLocked(entries, ledger)
	if err != nil {
		return ClaimRecord{}, err
	}
	found := false
	for _, entry := range ready {
		if sameIdentity(entry.Card.ID, req.ID) && isWorkTask(entry.Card.ID) {
			found = true
			break
		}
	}
	if !found {
		return ClaimRecord{}, fmt.Errorf("task %s is not ready", req.ID)
	}
	record = ClaimRecord{ID: req.ID, Owner: req.Owner, Token: req.Token, Status: "held"}
	ledger.Records = append(ledger.Records, record)
	if err := ensureReleaseCapacity(ledger); err != nil {
		return ClaimRecord{}, err
	}
	if err := saveClaims(r, ledger); err != nil {
		return ClaimRecord{}, err
	}
	return record, nil
}

func Release(dir string, req ClaimRequest) (record ClaimRecord, err error) {
	if err := validateClaimRequest(req); err != nil {
		return ClaimRecord{}, err
	}
	r, err := openBoard(dir)
	if err != nil {
		return ClaimRecord{}, err
	}
	defer r.Close()
	unlock, err := lock(r)
	if err != nil {
		return ClaimRecord{}, err
	}
	defer func() { err = errors.Join(err, unlock()) }()
	if err := rejectPendingTransitions(r); err != nil {
		return ClaimRecord{}, err
	}
	entries, err := listLocked(r)
	if err != nil {
		return ClaimRecord{}, err
	}
	ledger, err := loadClaims(r, entries)
	if err != nil {
		return ClaimRecord{}, err
	}
	for i := range ledger.Records {
		record := &ledger.Records[i]
		if record.Token != req.Token || record.ID != req.ID || record.Owner != req.Owner {
			continue
		}
		if record.Status == "released" {
			return *record, nil
		}
		record.Status = "released"
		if err := saveClaims(r, ledger); err != nil {
			return ClaimRecord{}, err
		}
		return *record, nil
	}
	return ClaimRecord{}, fmt.Errorf("matching held claim not found")
}

func validateClaimRequest(req ClaimRequest) error {
	if !validClaimID(req.ID) {
		return fmt.Errorf("invalid claim task ID %q", req.ID)
	}
	if !validClaimOwner(req.Owner) {
		return errors.New("claim owner must be 1-128 characters without control characters")
	}
	if !claimToken.MatchString(req.Token) {
		return errors.New("claim token must be 32 lowercase hexadecimal characters")
	}
	return nil
}

func validClaimOwner(owner string) bool {
	if owner == "" || strings.TrimSpace(owner) == "" || !utf8.ValidString(owner) || utf8.RuneCountInString(owner) > 128 {
		return false
	}
	for _, r := range owner {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func validClaimID(id string) bool {
	return isWorkTask(id) && identityKey(id) != ""
}

func loadClaims(r *os.Root, entries []Entry) (claimsLedger, error) {
	info, err := r.Lstat(claimsFile)
	if errors.Is(err, fs.ErrNotExist) {
		return claimsLedger{SchemaVersion: 1, Records: []ClaimRecord{}}, nil
	}
	if err != nil {
		return claimsLedger{}, fmt.Errorf("inspect claims ledger: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return claimsLedger{}, errors.New("claims ledger is not a regular file")
	}
	if info.Size() > maxClaimsBytes {
		return claimsLedger{}, errors.New("claims ledger exceeds 1 MiB")
	}
	f, err := r.Open(claimsFile)
	if err != nil {
		return claimsLedger{}, fmt.Errorf("open claims ledger: %w", err)
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, maxClaimsBytes+1))
	if err != nil {
		return claimsLedger{}, fmt.Errorf("read claims ledger: %w", err)
	}
	if len(raw) > maxClaimsBytes {
		return claimsLedger{}, errors.New("claims ledger exceeds 1 MiB")
	}
	if !utf8.Valid(raw) {
		return claimsLedger{}, errors.New("claims ledger contains invalid UTF-8")
	}
	if err := rejectDuplicateJSON(raw); err != nil {
		return claimsLedger{}, err
	}
	if err := validateClaimShape(raw); err != nil {
		return claimsLedger{}, err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var ledger claimsLedger
	if err := dec.Decode(&ledger); err != nil {
		return claimsLedger{}, fmt.Errorf("decode claims ledger: %w", err)
	}
	if ledger.SchemaVersion != 1 {
		return claimsLedger{}, fmt.Errorf("unsupported claims schema version %d", ledger.SchemaVersion)
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return claimsLedger{}, errors.New("claims ledger has trailing content")
	}
	return validateClaims(ledger, entries)
}

func validateClaimShape(raw []byte) error {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(raw, &root); err != nil {
		return fmt.Errorf("decode claims ledger: %w", err)
	}
	if len(root) != 2 {
		return errors.New("claims ledger requires schemaVersion and records")
	}
	for key := range root {
		if key != "schemaVersion" && key != "records" {
			return fmt.Errorf("unknown claims ledger field %q", key)
		}
	}
	var records []json.RawMessage
	if rawRecords, ok := root["records"]; !ok || json.Unmarshal(rawRecords, &records) != nil || records == nil {
		return errors.New("claims ledger records must be an array")
	}
	for _, rawRecord := range records {
		var obj map[string]json.RawMessage
		if err := json.Unmarshal(rawRecord, &obj); err != nil {
			return errors.New("claims ledger record must be an object")
		}
		if len(obj) != 4 {
			return errors.New("claims ledger record requires id, owner, token, and status")
		}
		for key := range obj {
			if key != "id" && key != "owner" && key != "token" && key != "status" {
				return fmt.Errorf("unknown claims record field %q", key)
			}
		}
	}
	return nil
}

func rejectDuplicateJSON(raw []byte) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	if err := consumeJSONValue(dec); err != nil {
		return fmt.Errorf("decode claims ledger: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("claims ledger has trailing content")
	}
	return nil
}

func consumeJSONValue(dec *json.Decoder) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	if delim, ok := tok.(json.Delim); ok {
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
					return errors.New("claims ledger object key is not a string")
				}
				if seen[name] {
					return fmt.Errorf("duplicate claims ledger field %q", name)
				}
				seen[name] = true
				if err := consumeJSONValue(dec); err != nil {
					return err
				}
			}
			_, err = dec.Token()
		case '[':
			for dec.More() {
				if err := consumeJSONValue(dec); err != nil {
					return err
				}
			}
			_, err = dec.Token()
		}
	}
	return err
}

func validateClaims(ledger claimsLedger, entries []Entry) (claimsLedger, error) {
	ids := make(map[string]bool, len(entries))
	for _, entry := range entries {
		if isWorkTask(entry.Card.ID) {
			ids[identityKey(entry.Card.ID)] = true
		}
	}
	tokens := map[string]bool{}
	held := map[string]bool{}
	for _, record := range ledger.Records {
		if !validClaimID(record.ID) || !validClaimOwner(record.Owner) || !claimToken.MatchString(record.Token) {
			return claimsLedger{}, errors.New("invalid claims ledger record")
		}
		if record.Status != "held" && record.Status != "released" {
			return claimsLedger{}, fmt.Errorf("invalid claim status %q", record.Status)
		}
		if tokens[record.Token] {
			return claimsLedger{}, errors.New("duplicate claim token")
		}
		tokens[record.Token] = true
		if record.Status == "held" {
			if !ids[identityKey(record.ID)] {
				return claimsLedger{}, fmt.Errorf("held claim references missing task %s", record.ID)
			}
			if held[identityKey(record.ID)] {
				return claimsLedger{}, fmt.Errorf("multiple held claims for task %s", record.ID)
			}
			held[identityKey(record.ID)] = true
		}
	}
	return ledger, nil
}

func saveClaims(r *os.Root, ledger claimsLedger) error {
	raw, err := json.MarshalIndent(ledger, "", "  ")
	if err != nil {
		return fmt.Errorf("encode claims ledger: %w", err)
	}
	raw = append(raw, '\n')
	if len(raw) > maxClaimsBytes {
		return errors.New("claims ledger exceeds 1 MiB")
	}
	name, err := stage(r, raw)
	if err != nil {
		return err
	}
	if err := r.Rename(name, claimsFile); err != nil {
		return errors.Join(fmt.Errorf("publish claims ledger: %w", err), r.Remove(name))
	}
	return nil
}

func ensureReleaseCapacity(ledger claimsLedger) error {
	reserved := ledger
	reserved.Records = append([]ClaimRecord(nil), ledger.Records...)
	for i := range reserved.Records {
		if reserved.Records[i].Status == "held" {
			reserved.Records[i].Status = "released"
		}
	}
	raw, err := json.MarshalIndent(reserved, "", "  ")
	if err != nil {
		return fmt.Errorf("encode claims release reservation: %w", err)
	}
	if len(raw)+1 > maxClaimsBytes {
		return errors.New("claims ledger has insufficient release capacity")
	}
	return nil
}

func readyLocked(entries []Entry, ledger claimsLedger) ([]Entry, error) {
	if err := validateGraph(entries); err != nil {
		return nil, err
	}
	held := map[string]bool{}
	for _, record := range ledger.Records {
		if record.Status == "held" {
			held[identityKey(record.ID)] = true
		}
	}
	byID := make(map[string]Entry, len(entries))
	for _, entry := range entries {
		if isWorkTask(entry.Card.ID) {
			byID[identityKey(entry.Card.ID)] = entry
		}
	}
	ready := make([]Entry, 0)
	policy := currentPolicy()
	for _, entry := range entries {
		if !entryInWorkflowZone(entry, policy.ReadyZone(), policy) || held[identityKey(entry.Card.ID)] {
			continue
		}
		ok := true
		for _, dep := range entry.Card.DependsOn {
			depEntry, exists := byID[identityKey(dep)]
			if !isWorkTask(dep) || !exists || !entryInWorkflowZone(depEntry, policy.DoneZone(), policy) {
				ok = false
				break
			}
		}
		if ok {
			ready = append(ready, entry)
		}
	}
	return ready, nil
}
