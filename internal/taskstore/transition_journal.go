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

	"github.com/Gizzahub/taskchain-task-manager/internal/card"
)

func loadTransitions(r *os.Root) (transitionJournal, error) {
	info, err := r.Lstat(transitionsFile)
	if errors.Is(err, fs.ErrNotExist) {
		return transitionJournal{SchemaVersion: 1, Records: []transitionRecord{}}, nil
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
	if j.SchemaVersion != 1 || j.Records == nil {
		return transitionJournal{}, errors.New("invalid transition journal schema")
	}
	pending := 0
	seen := map[string]bool{}
	for _, rec := range j.Records {
		if rec.RequestID == "" || seen[rec.RequestID] {
			return transitionJournal{}, errors.New("duplicate transition request ID")
		}
		seen[rec.RequestID] = true
		if rec.Kind == "pending" {
			pending++
		} else if rec.Kind != "completed" {
			return transitionJournal{}, errors.New("invalid transition record")
		}
		if err := validateTransitionRecord(rec); err != nil {
			return transitionJournal{}, err
		}
	}
	if pending > 1 {
		return transitionJournal{}, errors.New("multiple pending transitions")
	}
	return j, nil
}

func validateTransitionShape(raw []byte) error {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(raw, &root); err != nil {
		return err
	}
	if len(root) != 2 || root["schemaVersion"] == nil || root["records"] == nil || string(root["records"]) == "null" {
		return errors.New("transition journal requires schemaVersion and records")
	}
	for key := range root {
		if key != "schemaVersion" && key != "records" {
			return fmt.Errorf("unknown transition journal field %q", key)
		}
	}
	var records []map[string]json.RawMessage
	if err := json.Unmarshal(root["records"], &records); err != nil {
		return errors.New("transition records must be an array of objects")
	}
	allowed := map[string]bool{"kind": true, "requestId": true, "id": true, "owner": true, "token": true, "from": true, "to": true, "source": true, "target": true, "mode": true, "original": true, "patched": true, "status": true}
	for _, record := range records {
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
	if !claimToken.MatchString(rec.RequestID) || !validClaimID(rec.ID) || !validClaimOwner(rec.Owner) || !claimToken.MatchString(rec.Token) || !validZone(rec.From) || !validZone(rec.To) || !allowedEdge(rec.From, rec.To) || rec.From == rec.To {
		return errors.New("invalid transition record authority")
	}
	if filepath.Dir(rec.Source) != rec.From || filepath.Dir(rec.Target) != rec.To || filepath.Base(rec.Source) != filepath.Base(rec.Target) || strings.HasPrefix(filepath.Base(rec.Source), ".") || filepath.Ext(rec.Source) != ".md" || strings.EqualFold(filepath.Base(rec.Source), "README.md") {
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
		status, ok := currentPolicy().Status(rec.To)
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
	if err := validateTransitionCapacity(j); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(j, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	if len(raw) > maxTransitionBytes {
		return errors.New("transition journal exceeds 8 MiB")
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
