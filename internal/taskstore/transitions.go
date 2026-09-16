package taskstore

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/Gizzahub/taskchain-task-manager/internal/card"
)

const (
	transitionsFile    = ".task-manager-transitions.json"
	maxCardBytes       = 1 << 20
	maxTransitionBytes = 8 << 20
)

type TransitionRequest struct{ ID, Owner, Token, RequestID, From, To string }
type TransitionResult struct {
	RequestID string `json:"requestId"`
	ID        string `json:"id"`
	From      string `json:"from"`
	To        string `json:"to"`
	Path      string `json:"path"`
	Status    string `json:"status"`
}

type transitionRecord struct {
	Kind      string `json:"kind"`
	RequestID string `json:"requestId"`
	ID        string `json:"id"`
	Owner     string `json:"owner"`
	Token     string `json:"token"`
	From      string `json:"from"`
	To        string `json:"to"`
	Source    string `json:"source"`
	Target    string `json:"target"`
	Mode      uint32 `json:"mode"`
	Original  []byte `json:"original,omitempty"`
	Patched   []byte `json:"patched,omitempty"`
	Status    string `json:"status"`
}
type transitionJournal struct {
	SchemaVersion int                `json:"schemaVersion"`
	Records       []transitionRecord `json:"records"`
}

func Transition(dir string, req TransitionRequest) (TransitionResult, error) {
	return transitionWithStep(dir, req, nil)
}

// transitionWithStep is package-private so crash-boundary tests can inject a
// deterministic interruption without a process-global hook.
func transitionWithStep(dir string, req TransitionRequest, step func(string) error) (TransitionResult, error) {
	return executeTransition(dir, req, false, step)
}

func Recover(dir string, req TransitionRequest) (TransitionResult, error) {
	return executeTransition(dir, req, true, nil)
}

func executeTransition(dir string, req TransitionRequest, recoverOnly bool, step func(string) error) (result TransitionResult, err error) {
	if err := validateTransitionRequest(req); err != nil {
		return result, err
	}
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
	j, err := loadTransitions(r)
	if err != nil {
		return result, err
	}
	for _, rec := range j.Records {
		if rec.Kind == "pending" && rec.RequestID != req.RequestID {
			return result, errors.New("pending transition requires its original request")
		}
	}
	for _, rec := range j.Records {
		if rec.RequestID != req.RequestID {
			continue
		}
		if !sameTransition(rec, req) {
			return result, errors.New("request ID already used by different transition")
		}
		if rec.Kind == "completed" {
			return transitionResult(rec), nil
		}
		return finishTransition(r, j, rec, req, step)
	}
	if recoverOnly {
		return result, errors.New("matching transition not found; recover never starts a new operation")
	}
	entries, err := listLocked(r)
	if err != nil {
		return result, err
	}
	rec, err := prepareTransition(r, entries, req)
	if err != nil {
		return result, err
	}
	j.Records = append(j.Records, rec)
	if err := validateTransitionCapacity(j); err != nil {
		return result, err
	}
	if err := publishTransitionJournal(r, j); err != nil {
		return result, err
	}
	return finishTransition(r, j, rec, req, step)
}

func ClaimResume(dir string, req ClaimRequest) (record ClaimRecord, err error) {
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
	j, err := loadClaims(r, entries)
	if err != nil {
		return ClaimRecord{}, err
	}
	for _, rec := range j.Records {
		if rec.Token == req.Token {
			if rec.ID == req.ID && rec.Owner == req.Owner && rec.Status == "held" {
				return rec, nil
			}
			return ClaimRecord{}, errors.New("claim token already used")
		}
	}
	for _, rec := range j.Records {
		if rec.Status == "held" && sameIdentity(rec.ID, req.ID) {
			return ClaimRecord{}, fmt.Errorf("task %s is already claimed", req.ID)
		}
	}
	var found Entry
	for _, entry := range entries {
		zone := filepath.Dir(entry.Path)
		if sameIdentity(entry.Card.ID, req.ID) && isWorkTask(entry.Card.ID) && validZone(zone) && zone != "todo" {
			found = entry
			break
		}
	}
	if found.Card.ID == "" {
		return ClaimRecord{}, fmt.Errorf("task %s is not resumable", req.ID)
	}
	rec := ClaimRecord{ID: req.ID, Owner: req.Owner, Token: req.Token, Status: "held"}
	j.Records = append(j.Records, rec)
	if err := ensureReleaseCapacity(j); err != nil {
		return ClaimRecord{}, err
	}
	if err := saveClaims(r, j); err != nil {
		return ClaimRecord{}, err
	}
	return rec, nil
}

func validateTransitionRequest(req TransitionRequest) error {
	if !validClaimID(req.ID) || !validClaimOwner(req.Owner) || !claimToken.MatchString(req.Token) || !claimToken.MatchString(req.RequestID) {
		return errors.New("invalid transition identity")
	}
	if !validZone(req.From) || !validZone(req.To) || req.From == req.To || !allowedEdge(req.From, req.To) {
		return errors.New("unsupported transition")
	}
	return nil
}
func validZone(s string) bool {
	switch s {
	case "todo", "doing", "review", "blocked", "done":
		return true
	}
	return false
}
func allowedEdge(from, to string) bool {
	switch from {
	case "todo":
		return to == "doing"
	case "doing":
		return to == "todo" || to == "review" || to == "blocked"
	case "blocked":
		return to == "todo" || to == "doing"
	case "review":
		return to == "doing" || to == "done"
	case "done":
		return to == "todo"
	}
	return false
}
func sameTransition(rec transitionRecord, req TransitionRequest) bool {
	return rec.ID == req.ID && rec.Owner == req.Owner && rec.Token == req.Token && rec.From == req.From && rec.To == req.To
}
func transitionResult(rec transitionRecord) TransitionResult {
	return TransitionResult{RequestID: rec.RequestID, ID: rec.ID, From: rec.From, To: rec.To, Path: rec.Target, Status: "completed"}
}

func prepareTransition(r *os.Root, entries []Entry, req TransitionRequest) (transitionRecord, error) {
	var entry Entry
	for _, e := range entries {
		if sameIdentity(e.Card.ID, req.ID) && isWorkTask(e.Card.ID) {
			entry = e
			break
		}
	}
	if entry.Card.ID == "" || filepath.Dir(entry.Path) != req.From {
		return transitionRecord{}, errors.New("source card does not match transition")
	}
	if err := validateGraph(entries); err != nil {
		return transitionRecord{}, err
	}
	for _, e := range entries {
		if sameIdentity(e.Card.ID, req.ID) && e.Path != entry.Path {
			return transitionRecord{}, errors.New("duplicate task ID")
		}
	}
	if req.To == "doing" || req.To == "done" {
		for _, dep := range entry.Card.DependsOn {
			for _, e := range entries {
				if sameIdentity(e.Card.ID, dep) && !(isWorkTask(e.Card.ID) && filepath.Dir(e.Path) == "done" && e.Card.Status == "done") {
					return transitionRecord{}, errors.New("dependency is not done")
				}
			}
		}
	}
	var held *ClaimRecord
	claims, err := loadClaims(r, entries)
	if err != nil {
		return transitionRecord{}, err
	}
	for i := range claims.Records {
		if sameIdentity(claims.Records[i].ID, req.ID) && claims.Records[i].Status == "held" {
			held = &claims.Records[i]
		}
	}
	if held == nil || held.Owner != req.Owner || held.Token != req.Token {
		return transitionRecord{}, errors.New("matching held claim not found")
	}
	info, err := r.Lstat(entry.Path)
	if err != nil {
		return transitionRecord{}, err
	}
	if !info.Mode().IsRegular() {
		return transitionRecord{}, errors.New("source card is not regular")
	}
	raw, err := readTransitionCard(r, entry.Path)
	if err != nil {
		return transitionRecord{}, err
	}
	if len(raw) > maxCardBytes {
		return transitionRecord{}, errors.New("card exceeds 1 MiB")
	}
	doc, err := card.Parse(raw)
	if err != nil {
		return transitionRecord{}, err
	}
	status := req.To
	if req.To == "todo" {
		status = "pending"
	}
	if req.To == "doing" {
		status = "in-progress"
	}
	patched, _, err := doc.SetStatusCell(status)
	if err != nil {
		return transitionRecord{}, err
	}
	target := filepath.ToSlash(filepath.Join(req.To, filepath.Base(entry.Path)))
	if _, err := r.Lstat(target); err == nil {
		return transitionRecord{}, errors.New("transition target already exists")
	} else if !errors.Is(err, fs.ErrNotExist) {
		return transitionRecord{}, err
	}
	rec := transitionRecord{Kind: "pending", RequestID: req.RequestID, ID: req.ID, Owner: req.Owner, Token: req.Token, From: req.From, To: req.To, Source: entry.Path, Target: target, Mode: uint32(info.Mode().Perm()), Original: raw, Patched: patched, Status: "pending"}
	if err := validateTransitionRecord(rec); err != nil {
		return transitionRecord{}, err
	}
	return rec, nil
}
