package taskstore

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"sort"
	"unicode/utf8"

	"github.com/Gizzahub/taskchain-task-manager/internal/cardid"
)

const idsFile = ".task-manager-ids.json"
const maxIDsBytes = 1 << 20

type idLedger struct {
	SchemaVersion int      `json:"schemaVersion"`
	Reserved      []string `json:"reserved"`
	Namespace     string   `json:"namespace,omitempty"`
}

type ReservationResult struct {
	ReservedCount int               `json:"reservedCount"`
	MaxID         string            `json:"maxId"`
	MaxIDs        map[string]string `json:"maxIds"`
}

func loadIDs(r *os.Root) (idLedger, error) {
	info, err := r.Lstat(idsFile)
	if err != nil {
		return idLedger{}, err
	}
	if !info.Mode().IsRegular() {
		return idLedger{}, errors.New("ID ledger is not a regular file")
	}
	if info.Size() > maxIDsBytes {
		return idLedger{}, errors.New("ID ledger exceeds 1 MiB")
	}
	f, err := r.Open(idsFile)
	if err != nil {
		return idLedger{}, err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, maxIDsBytes+1))
	if err != nil {
		return idLedger{}, err
	}
	return decodeIDs(raw)
}

func decodeIDs(raw []byte) (idLedger, error) {
	if len(raw) > maxIDsBytes || !utf8.Valid(raw) {
		return idLedger{}, errors.New("invalid ID ledger size or UTF-8")
	}
	if err := rejectDuplicateJSON(raw); err != nil {
		return idLedger{}, err
	}
	var shape map[string]json.RawMessage
	if err := json.Unmarshal(raw, &shape); err != nil {
		return idLedger{}, err
	}
	if (len(shape) != 2 && len(shape) != 3) || shape["schemaVersion"] == nil || shape["reserved"] == nil || (len(shape) == 3 && shape["namespace"] == nil) {
		return idLedger{}, errors.New("ID ledger requires exactly schemaVersion and reserved")
	}
	var ledger idLedger
	if err := json.Unmarshal(raw, &ledger); err != nil {
		return idLedger{}, err
	}
	if (ledger.SchemaVersion != 1 && ledger.SchemaVersion != 2 && ledger.SchemaVersion != 3) || ledger.Reserved == nil {
		return idLedger{}, errors.New("invalid ID ledger schema")
	}
	if ledger.SchemaVersion == 3 {
		if len(shape) != 3 || !sharedHex32.MatchString(ledger.Namespace) {
			return idLedger{}, errors.New("invalid shared ID namespace binding")
		}
	} else if len(shape) != 2 || ledger.Namespace != "" {
		return idLedger{}, errors.New("unexpected local ID namespace binding")
	}
	for i, id := range ledger.Reserved {
		if identityKey(id) != id || id == "" || (ledger.SchemaVersion == 1 && !canonicalID.MatchString(id)) || (i > 0 && ledger.Reserved[i-1] >= id) {
			return idLedger{}, errors.New("ID ledger requires sorted unique canonical IDs")
		}
	}
	return ledger, nil
}

func publishIDs(r *os.Root, ledger idLedger, initial bool) error {
	if _, err := policyForBoard(r); err != nil {
		return err
	}
	return publishValidatedIDs(r, ledger, initial)
}

// The bundle writer validates its exact pending operation and bound policy
// before using this primitive. Ordinary writers use publishIDs.
func publishValidatedIDs(r *os.Root, ledger idLedger, initial bool) error {
	raw, err := json.Marshal(ledger)
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	if len(raw) > maxIDsBytes {
		return errors.New("ID ledger exceeds 1 MiB")
	}
	name, err := stage(r, raw)
	if err != nil {
		return err
	}
	if initial {
		if err := errors.Join(r.Link(name, idsFile), r.Remove(name)); err != nil {
			return err
		}
	} else if err := r.Rename(name, idsFile); err != nil {
		return errors.Join(err, r.Remove(name))
	}
	if ledger.SchemaVersion == 3 {
		dir, err := r.Open(".")
		if err != nil {
			return err
		}
		return errors.Join(dir.Sync(), dir.Close())
	}
	return nil
}

// observedIDs does not inspect Git history or infer IDs of deleted manual cards.
func observedIDs(r *os.Root, entries []Entry, ledger idLedger, extra []string) (idLedger, error) {
	claims, err := loadClaims(r, entries)
	if err != nil {
		return idLedger{}, err
	}
	transitions, err := loadTransitions(r)
	if err != nil {
		return idLedger{}, err
	}
	return observedIDsWithRecords(entries, ledger, extra, claims, transitions)
}

func observedIDsWithRecords(entries []Entry, ledger idLedger, extra []string, claims claimsLedger, transitions transitionJournal) (idLedger, error) {
	set := map[string]bool{}
	for _, id := range ledger.Reserved {
		set[id] = true
	}
	for _, entry := range entries {
		set[identityKey(entry.Card.ID)] = true
	}
	for _, record := range claims.Records {
		set[identityKey(record.ID)] = true
	}
	for _, record := range transitions.Records {
		set[identityKey(record.ID)] = true
	}
	for _, id := range extra {
		set[identityKey(id)] = true
	}
	ledger.Reserved = make([]string, 0, len(set))
	for id := range set {
		if identityKey(id) == "" {
			return idLedger{}, fmt.Errorf("invalid reservation ID %q", id)
		}
		ledger.Reserved = append(ledger.Reserved, id)
	}
	sort.Strings(ledger.Reserved)
	if ledger.SchemaVersion != 3 {
		ledger.SchemaVersion = 2
	}
	return ledger, nil
}

func allocateID(ledger idLedger, requested, prefix string) (string, error) {
	var max uint64
	for _, id := range ledger.Reserved {
		if sameIdentity(id, requested) {
			return "", fmt.Errorf("task ID already reserved: %s", id)
		}
		parsed, err := cardid.Parse(id)
		if err != nil {
			return "", err
		}
		if parsed.Prefix == prefix && parsed.Number > max {
			max = parsed.Number
		}
	}
	if requested != "" {
		if identityKey(requested) == "" {
			return "", fmt.Errorf("invalid task ID %q", requested)
		}
		return requested, nil
	}
	if max == ^uint64(0) {
		return "", errors.New("task ID allocation overflow")
	}
	return fmt.Sprintf("%s-%d", prefix, max+1), nil
}

// ReserveIDs is a monotonic union, including when explicitly adopting a legacy board.
func ReserveIDs(dir string, ids []string, adopt bool) (result ReservationResult, err error) {
	for _, id := range ids {
		if identityKey(id) == "" {
			return result, fmt.Errorf("invalid reservation ID %q", id)
		}
	}
	shared, release, err := acquireShared(dir, false)
	if err != nil {
		return result, err
	}
	defer func() { err = errors.Join(err, release()) }()
	r, err := openBoard(dir)
	if err != nil {
		return result, err
	}
	defer r.Close()
	unlock, err := lock(r)
	if err != nil {
		return result, err
	}
	defer func() { err = errors.Join(err, unlock()) }()
	if err := shared.verifyBoard(r); err != nil {
		return result, err
	}
	if err := rejectPendingTransitions(r); err != nil {
		return result, err
	}
	entries, err := listLocked(r)
	if err != nil {
		return result, err
	}
	ledger, err := loadIDs(r)
	initial := errors.Is(err, fs.ErrNotExist)
	if initial && adopt {
		ledger, err = idLedger{SchemaVersion: 2, Reserved: []string{}}, nil
	}
	if err != nil {
		return result, fmt.Errorf("load ID ledger (use reserve-ids --adopt for an uninitialized board): %w", err)
	}
	ledger, err = observedIDs(r, entries, ledger, ids)
	if err != nil {
		return result, err
	}
	ledger, err = shared.merge(ledger)
	if err != nil {
		return result, err
	}
	if err := shared.publish(ledger); err != nil {
		return result, err
	}
	if err := shared.verifyBoard(r); err != nil {
		return result, err
	}
	if err := publishIDs(r, ledger, initial); err != nil {
		return result, err
	}
	result.ReservedCount = len(ledger.Reserved)
	result.MaxIDs = map[string]string{}
	for _, id := range ledger.Reserved {
		parsed, _ := cardid.Parse(id)
		previous, exists := result.MaxIDs[parsed.Prefix]
		old, _ := cardid.Parse(previous)
		if !exists || parsed.Number > old.Number {
			result.MaxIDs[parsed.Prefix] = id
		}
	}
	result.MaxID = result.MaxIDs["TASK"]
	return result, nil
}

func validateOptionalIDs(r *os.Root) error {
	_, err := loadIDs(r)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}
