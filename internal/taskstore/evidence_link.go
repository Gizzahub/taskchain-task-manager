package taskstore

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"sort"
	"strings"

	"github.com/Gizzahub/taskchain-task-manager/internal/cardid"
)

const evidenceLinkDirectory = ".task-manager-context/evidence-links"
const maxEvidenceReceiptBytes = 256 << 10

// EvidenceLinkResult is an observational task-to-receipt binding. It does not
// attest to completion, accept a review, or change a card's workflow state.
type EvidenceLinkResult struct {
	TaskID         string `json:"taskId"`
	EventID        string `json:"eventId"`
	ReceiptSHA256  string `json:"receiptSha256"`
	ReceiptRef     string `json:"receiptRef,omitempty"`
	Reason         string `json:"reason"`
	Certainty      string `json:"certainty"`
	Provider       string `json:"provider,omitempty"`
	Model          string `json:"model,omitempty"`
	IdentitySource string `json:"identitySource"`
	Registered     bool   `json:"registered"`
}

type evidenceLinkRecord struct {
	TaskID         string `json:"taskId"`
	EventID        string `json:"eventId"`
	ReceiptSHA256  string `json:"receiptSha256"`
	ReceiptRef     string `json:"receiptRef,omitempty"`
	Reason         string `json:"reason"`
	Certainty      string `json:"certainty"`
	Provider       string `json:"provider,omitempty"`
	Model          string `json:"model,omitempty"`
	IdentitySource string `json:"identitySource"`
}

// LinkEvidence stores a content-addressed, immutable reference to a strict
// interruption receipt. ReceiptRef is inert metadata and is never read.
func LinkEvidence(dir, taskID string, rawReceipt []byte, expectedSHA256 string, receiptRef string) (result EvidenceLinkResult, err error) {
	if len(receiptRef) > 4096 || strings.ContainsRune(receiptRef, 0) {
		return result, errors.New("receipt reference exceeds 4096 bytes or contains NUL")
	}
	if len(rawReceipt) == 0 || len(rawReceipt) > maxEvidenceReceiptBytes {
		return result, errors.New("receipt must be non-empty and no larger than 256 KiB")
	}
	if !validSHA256(expectedSHA256) {
		return result, errors.New("expected SHA-256 must be 64 lowercase hexadecimal characters")
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(rawReceipt))
	if digest != expectedSHA256 {
		return result, errors.New("receipt SHA-256 does not match expected SHA-256")
	}
	rec, err := parseEvidenceReceipt(rawReceipt, taskID, digest, receiptRef)
	if err != nil {
		return result, err
	}
	session, err := openBoardSession(dir)
	if err != nil {
		return result, err
	}
	defer func() { err = errors.Join(err, session.close()) }()
	r := session.root
	if err := rejectPendingTransitions(r); err != nil {
		return result, err
	}
	entries, err := listLocked(r)
	if err != nil {
		return result, err
	}
	canonical, err := evidenceTaskID(entries, taskID)
	if err != nil {
		return result, err
	}
	rec.TaskID = canonical
	if _, err := evidenceDirectories(r, canonical, true); err != nil {
		return result, err
	}
	name := evidencePath(canonical, rec.EventID)
	previous, found, err := readEvidenceLink(r, canonical, rec.EventID)
	if err != nil {
		return result, err
	}
	if found {
		if previous != rec {
			return result, errors.New("evidence event already contains different immutable link")
		}
		return evidenceResult(rec, false), nil
	}
	raw, err := json.Marshal(rec)
	if err != nil {
		return result, err
	}
	stageName, err := stage(r, raw)
	if err != nil {
		return result, err
	}
	if err := r.Link(stageName, name); err != nil {
		return result, errors.Join(err, r.Remove(stageName))
	}
	if err := r.Remove(stageName); err != nil {
		return result, fmt.Errorf("evidence link may already be registered at %s: %w", name, err)
	}
	if err := errors.Join(syncRelocationDirectory(r, path.Dir(name)), syncRoot(r)); err != nil {
		return result, fmt.Errorf("evidence link may already be registered at %s: %w", name, err)
	}
	return evidenceResult(rec, true), nil
}

// EvidenceLinks reads immutable receipt-link metadata after validating that the
// requested live card still exists. It never reads a receipt reference.
func EvidenceLinks(dir, taskID string) (result []EvidenceLinkResult, err error) {
	session, err := openBoardSession(dir)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, session.close()) }()
	r := session.root
	if err := rejectPendingTransitions(r); err != nil {
		return nil, err
	}
	entries, err := listLocked(r)
	if err != nil {
		return nil, err
	}
	canonical, err := evidenceTaskID(entries, taskID)
	if err != nil {
		return nil, err
	}
	ready, err := evidenceDirectories(r, canonical, false)
	if err != nil || !ready {
		return []EvidenceLinkResult{}, err
	}
	dirName := path.Join(evidenceLinkDirectory, canonical)
	d, err := r.Open(dirName)
	if err != nil {
		return nil, err
	}
	defer d.Close()
	names, err := d.ReadDir(-1)
	if err != nil {
		return nil, err
	}
	for _, entry := range names {
		if entry.IsDir() || strings.HasPrefix(entry.Name(), ".") || !strings.HasSuffix(entry.Name(), ".json") {
			return nil, fmt.Errorf("invalid evidence link entry: %s", entry.Name())
		}
		raw, err := boundedSnapshotFile(r, path.Join(dirName, entry.Name()), maxEvidenceReceiptBytes)
		if err != nil {
			return nil, fmt.Errorf("read evidence link: %w", err)
		}
		var rec evidenceLinkRecord
		if err := strictDecode(raw, &rec); err != nil || rec.TaskID != canonical || !validEventID(rec.EventID) || evidencePath(canonical, rec.EventID) != path.Join(dirName, entry.Name()) || !validSHA256(rec.ReceiptSHA256) || !validReason(rec.Reason) || !validCertainty(rec.Certainty) || !validIdentitySource(rec.IdentitySource) {
			return nil, errors.New("corrupt evidence link")
		}
		canonicalRaw, _ := json.Marshal(rec)
		if !bytes.Equal(raw, canonicalRaw) {
			return nil, errors.New("evidence link is not canonical; preserve the original file")
		}
		result = append(result, evidenceResult(rec, false))
	}
	sort.Slice(result, func(i, j int) bool { return result[i].EventID < result[j].EventID })
	return result, nil
}

func evidenceResult(r evidenceLinkRecord, registered bool) EvidenceLinkResult {
	return EvidenceLinkResult{r.TaskID, r.EventID, r.ReceiptSHA256, r.ReceiptRef, r.Reason, r.Certainty, r.Provider, r.Model, r.IdentitySource, registered}
}
func evidencePath(taskID, eventID string) string {
	return path.Join(evidenceLinkDirectory, taskID, fmt.Sprintf("%x", sha256.Sum256([]byte(eventID)))+".json")
}

func evidenceTaskID(entries []Entry, raw string) (string, error) {
	id, err := cardid.Parse(raw)
	if err != nil || id.Prefix != "TASK" {
		return "", errors.New("task ID must be a TASK card identity")
	}
	key := id.Key()
	for _, entry := range entries {
		if identityKey(entry.Card.ID) == key {
			return entry.Card.ID, nil
		}
	}
	return "", fmt.Errorf("TASK is not present: %s", raw)
}

func evidenceDirectories(r *os.Root, taskID string, create bool) (bool, error) {
	name := ""
	for _, part := range []string{".task-manager-context", "evidence-links", taskID} {
		name = path.Join(name, part)
		info, err := r.Lstat(name)
		if errors.Is(err, fs.ErrNotExist) {
			if !create {
				return false, nil
			}
			if err = r.Mkdir(name, 0o700); err != nil {
				return false, err
			}
			if err = syncRelocationDirectory(r, path.Dir(name)); err != nil {
				return false, err
			}
			info, err = r.Lstat(name)
		}
		if err != nil {
			return false, err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return false, fmt.Errorf("evidence link path is not a real directory: %s", name)
		}
	}
	return true, nil
}

func readEvidenceLink(r *os.Root, taskID, eventID string) (evidenceLinkRecord, bool, error) {
	var out evidenceLinkRecord
	if !validEventID(eventID) {
		return out, false, errors.New("invalid evidence event ID")
	}
	ready, err := evidenceDirectories(r, taskID, false)
	if err != nil || !ready {
		return out, false, err
	}
	raw, err := boundedSnapshotFile(r, evidencePath(taskID, eventID), maxEvidenceReceiptBytes)
	if errors.Is(err, fs.ErrNotExist) {
		return out, false, nil
	}
	if err != nil {
		return out, false, fmt.Errorf("read evidence link: %w", err)
	}
	if err := strictDecode(raw, &out); err != nil {
		return out, false, fmt.Errorf("corrupt evidence link: %w", err)
	}
	if out.TaskID != taskID || out.EventID != eventID || !validSHA256(out.ReceiptSHA256) || !validReason(out.Reason) || !validCertainty(out.Certainty) || !validIdentitySource(out.IdentitySource) {
		return out, false, errors.New("evidence link identity or metadata does not match its path")
	}
	canonical, _ := json.Marshal(out)
	if !bytes.Equal(raw, canonical) {
		return out, false, errors.New("evidence link is not canonical; preserve the original file")
	}
	return out, true, nil
}
