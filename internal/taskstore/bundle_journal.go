package taskstore

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"unicode/utf8"
)

const bundlesFile = ".task-manager-bundles.json"
const maxBundleJournalBytes = 8 << 20

var bundleRequestIDPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)
var bundleBoardIDPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

type bundleJournal struct {
	SchemaVersion int            `json:"schemaVersion"`
	BoardID       string         `json:"boardId"`
	Records       []bundleRecord `json:"records"`
}

type bundleRecord struct {
	Status        string       `json:"status"`
	RequestID     string       `json:"requestId"`
	RequestDigest string       `json:"requestDigest"`
	Request       []byte       `json:"request"`
	Intent        []byte       `json:"intent"`
	OriginalIDs   []byte       `json:"originalIds"`
	BaseIDs       []byte       `json:"baseIds"`
	TargetIDs     []byte       `json:"targetIds"`
	Cards         []bundleCard `json:"cards"`
	Batch         []byte       `json:"batch"`
	PolicyDigest  string       `json:"policyDigest"`
	Namespace     string       `json:"namespace"`
	Owner         string       `json:"owner"`
}

type bundleCard struct {
	Key  string `json:"key"`
	ID   string `json:"id"`
	Path string `json:"path"`
	Raw  []byte `json:"raw"`
}

func loadBundles(r *os.Root) (bundleJournal, error) {
	info, err := r.Lstat(bundlesFile)
	if err != nil {
		return bundleJournal{}, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return bundleJournal{}, errors.New("bundle journal is not a regular file")
	}
	if info.Size() > maxBundleJournalBytes {
		return bundleJournal{}, errors.New("bundle journal exceeds 8 MiB")
	}
	f, err := r.Open(bundlesFile)
	if err != nil {
		return bundleJournal{}, err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, maxBundleJournalBytes+1))
	if err != nil {
		return bundleJournal{}, err
	}
	if len(raw) > maxBundleJournalBytes || !utf8.Valid(raw) {
		return bundleJournal{}, errors.New("bundle journal exceeds 8 MiB or is invalid UTF-8")
	}
	if err := rejectDuplicateJSON(raw); err != nil {
		return bundleJournal{}, err
	}
	if err := validateBundleJournalShape(raw); err != nil {
		return bundleJournal{}, err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var journal bundleJournal
	if err := dec.Decode(&journal); err != nil {
		return bundleJournal{}, err
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return bundleJournal{}, errors.New("bundle journal has trailing content")
	}
	if journal.SchemaVersion != 1 || journal.Records == nil {
		return bundleJournal{}, errors.New("invalid bundle journal schema")
	}
	if !bundleBoardIDPattern.MatchString(journal.BoardID) {
		return bundleJournal{}, errors.New("invalid bundle journal board ID")
	}
	seen := map[string]bool{}
	pending := 0
	for i := range journal.Records {
		record := journal.Records[i]
		if !bundleRequestIDPattern.MatchString(record.RequestID) || seen[record.RequestID] {
			return bundleJournal{}, errors.New("invalid or duplicate bundle request ID")
		}
		seen[record.RequestID] = true
		switch record.Status {
		case "pending":
			pending++
		case "completed":
		default:
			return bundleJournal{}, errors.New("invalid bundle journal record status")
		}
		if pending > 1 {
			return bundleJournal{}, errors.New("multiple pending bundle records")
		}
		if err := validateBundleRecord(record); err != nil {
			return bundleJournal{}, err
		}
	}
	return journal, nil
}

func bundleJournalBytes(journal bundleJournal) ([]byte, error) {
	if journal.SchemaVersion != 1 || journal.Records == nil {
		return nil, errors.New("invalid bundle journal schema")
	}
	if !bundleBoardIDPattern.MatchString(journal.BoardID) {
		return nil, errors.New("invalid bundle journal board ID")
	}
	seen := map[string]bool{}
	pending := 0
	for _, record := range journal.Records {
		if !bundleRequestIDPattern.MatchString(record.RequestID) || seen[record.RequestID] {
			return nil, errors.New("invalid or duplicate bundle request ID")
		}
		seen[record.RequestID] = true
		if record.Status == "pending" {
			pending++
		} else if record.Status != "completed" {
			return nil, errors.New("invalid bundle journal record status")
		}
		if err := validateBundleRecord(record); err != nil {
			return nil, err
		}
	}
	if pending > 1 {
		return nil, errors.New("multiple pending bundle records")
	}
	raw, err := json.Marshal(journal)
	if err != nil {
		return nil, err
	}
	raw = append(raw, '\n')
	if len(raw) > maxBundleJournalBytes {
		return nil, errors.New("bundle journal exceeds 8 MiB")
	}
	return raw, nil
}

func saveBundles(r *os.Root, journal bundleJournal, initial bool) error {
	raw, err := bundleJournalBytes(journal)
	if err != nil {
		return err
	}
	name, err := stage(r, raw)
	if err != nil {
		return err
	}
	if initial {
		if err := r.Link(name, bundlesFile); err != nil {
			return errors.Join(err, r.Remove(name))
		}
		if err := r.Remove(name); err != nil {
			return err
		}
	} else if err := r.Rename(name, bundlesFile); err != nil {
		return errors.Join(err, r.Remove(name))
	}
	dir, err := r.Open(".")
	if err != nil {
		return err
	}
	return errors.Join(dir.Sync(), dir.Close())
}

func validateBundleJournalShape(raw []byte) error {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(raw, &root); err != nil {
		return err
	}
	if err := requireExactFields(root, map[string]bool{"schemaVersion": true, "boardId": true, "records": true}); err != nil {
		return err
	}
	if err := requireStringFields(root, "boardId"); err != nil {
		return err
	}
	var records []json.RawMessage
	if err := json.Unmarshal(root["records"], &records); err != nil || records == nil {
		return errors.New("bundle journal records must be a non-null array")
	}
	for _, rawRecord := range records {
		var record map[string]json.RawMessage
		if err := json.Unmarshal(rawRecord, &record); err != nil {
			return errors.New("bundle journal record must be an object")
		}
		if err := requireExactFields(record, map[string]bool{"status": true, "requestId": true, "requestDigest": true, "request": true, "intent": true, "originalIds": true, "baseIds": true, "targetIds": true, "cards": true, "batch": true, "policyDigest": true, "namespace": true, "owner": true}); err != nil {
			return err
		}
		if err := requireStringFields(record, "status", "requestId", "requestDigest", "request", "intent", "originalIds", "baseIds", "targetIds", "batch", "policyDigest", "namespace", "owner"); err != nil {
			return err
		}
		var cards []json.RawMessage
		if err := json.Unmarshal(record["cards"], &cards); err != nil || cards == nil {
			return errors.New("bundle record cards must be a non-null array")
		}
		for _, rawCard := range cards {
			var card map[string]json.RawMessage
			if err := json.Unmarshal(rawCard, &card); err != nil {
				return errors.New("bundle card must be an object")
			}
			if err := requireExactFields(card, map[string]bool{"key": true, "id": true, "path": true, "raw": true}); err != nil {
				return err
			}
			if err := requireStringFields(card, "key", "id", "path", "raw"); err != nil {
				return err
			}
		}
	}
	return nil
}

func requireStringFields(object map[string]json.RawMessage, fields ...string) error {
	for _, field := range fields {
		var value string
		if err := json.Unmarshal(object[field], &value); err != nil {
			return fmt.Errorf("bundle journal field %q must be a JSON string", field)
		}
	}
	return nil
}

func requireExactFields(object map[string]json.RawMessage, allowed map[string]bool) error {
	if len(object) != len(allowed) {
		return errors.New("bundle journal object requires all fields exactly once")
	}
	for key, raw := range object {
		if !allowed[key] {
			return fmt.Errorf("unknown bundle journal field %q", key)
		}
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return fmt.Errorf("bundle journal field %q cannot be null", key)
		}
	}
	return nil
}
