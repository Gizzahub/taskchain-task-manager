package taskstore

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/Gizzahub/taskchain-task-manager/internal/boardpolicy"
	"github.com/Gizzahub/taskchain-task-manager/internal/card"
	"github.com/Gizzahub/taskchain-task-manager/internal/cardpath"
)

type RepairRequest struct{ ID, Owner, Token, RequestID, Path, ExpectedSHA256 string }
type RepairResult struct {
	SchemaVersion int    `json:"schemaVersion"`
	RequestID     string `json:"requestId"`
	ID            string `json:"id"`
	Path          string `json:"path"`
	Status        string `json:"status"`
	Changed       bool   `json:"changed"`
}

func RepairStatus(dir string, req RepairRequest, adopt bool) (RepairResult, error) {
	return repairStatusWithStep(dir, req, adopt, false, nil)
}
func RecoverStatusRepair(dir string, req RepairRequest) (RepairResult, error) {
	return repairStatusWithStep(dir, req, false, true, nil)
}
func repairStatusWithStep(dir string, req RepairRequest, adopt, recoverOnly bool, step func(string) error) (result RepairResult, err error) {
	if err := validateRepairRequest(req); err != nil {
		return result, err
	}
	s, err := openRepairSession(dir, req)
	if err != nil {
		return result, err
	}
	defer func() { err = errors.Join(err, s.close()) }()
	if err := s.resumeCommon(req); err != nil {
		return result, err
	}
	for _, rec := range s.journal.Records {
		if rec.Kind == "pending" && rec.RequestID != req.RequestID {
			return result, errors.New("pending repair requires its original request")
		}
	}
	for _, rec := range s.journal.Records {
		if rec.RequestID != req.RequestID {
			continue
		}
		if !sameRepair(rec, req) {
			return result, errors.New("request ID already used by different repair")
		}
		if rec.Kind == "completed" {
			if err := s.clearCommon(req); err != nil {
				return result, err
			}
			return repairResult(rec), nil
		}
		return s.finishRepair(rec, req, step)
	}
	if recoverOnly {
		return result, errors.New("matching repair not found; recovery never starts an operation")
	}
	rec, err := prepareRepair(s, req)
	if err != nil {
		return result, err
	}
	next := s.journal
	next.Records = append(append([]repairRecord(nil), next.Records...), rec)
	if _, err := repairJournalBytes(next); err != nil {
		return result, err
	}
	if err := s.adopt(adopt, step); err != nil {
		return result, err
	}
	if err := s.reserveCommon(next, req); err != nil {
		return result, err
	}
	if err := storageStep(step, "after-common-pending"); err != nil {
		return result, err
	}
	if err := saveRepairJournal(s.root, next, false); err != nil {
		return result, err
	}
	s.journal = next
	if err := storageStep(step, "after-journal"); err != nil {
		return result, err
	}
	return s.finishRepair(rec, req, step)
}

func prepareRepair(s *repairSession, req RepairRequest) (repairRecord, error) {
	var zero repairRecord
	entries, err := listLockedWithPolicy(s.root, "", s.policy)
	if err != nil {
		return zero, err
	}
	if err := validateGraph(entries); err != nil {
		return zero, err
	}
	var entry Entry
	for _, e := range entries {
		if e.Path == req.Path {
			entry = e
			break
		}
	}
	status, ok := repairPathStatus(req.Path, s.policy)
	if !sameIdentity(entry.Card.ID, req.ID) || !isWorkTask(entry.Card.ID) || !ok {
		return zero, errors.New("repair source is not the requested workflow TASK")
	}
	ledger, err := loadIDs(s.root)
	if err != nil {
		return zero, err
	}
	found := false
	for _, id := range ledger.Reserved {
		if sameIdentity(id, req.ID) {
			found = true
		}
	}
	if !found {
		return zero, errors.New("repair TASK must already have a permanent ID reservation")
	}
	claims, err := loadClaims(s.root, entries)
	if err != nil {
		return zero, err
	}
	if err := validateRepairClaim(claims, req); err != nil {
		return zero, err
	}
	if err := relocationParents(s.root, req.Path, false); err != nil {
		return zero, err
	}
	info, err := s.root.Lstat(req.Path)
	if err != nil {
		return zero, err
	}
	if !info.Mode().IsRegular() {
		return zero, errors.New("repair source is not regular")
	}
	raw, err := readTransitionCard(s.root, req.Path)
	if err != nil {
		return zero, err
	}
	if bytesDigest(raw) != req.ExpectedSHA256 {
		return zero, errors.New("repair source digest does not match expected SHA-256")
	}
	doc, err := card.Parse(raw)
	if err != nil {
		return zero, err
	}
	patched, changed, err := doc.SetStatusCell(status)
	if err != nil {
		return zero, err
	}
	digest, err := s.policy.Digest()
	if err != nil {
		return zero, err
	}
	rec := repairRecord{Kind: "pending", RequestID: req.RequestID, ID: req.ID, Owner: req.Owner, Token: req.Token, Path: req.Path, ExpectedSHA256: req.ExpectedSHA256, Mode: uint32(info.Mode().Perm()), Original: raw, Patched: patched, PolicyDigest: digest, BoardPath: s.journal.BoardPath, Changed: changed, CanonicalStatus: status}
	if !isTopLevelWorkflowPath(req.Path, s.policy) {
		rec.PolicyCanonical, err = s.policy.Canonical()
		if err != nil {
			return zero, err
		}
	}
	if s.shared != nil && s.shared.state != nil {
		rec.Namespace = s.shared.state.NamespaceID
	}
	return rec, validateRepairRecord(rec)
}
func repairResult(rec repairRecord) RepairResult {
	return RepairResult{SchemaVersion: 1, RequestID: rec.RequestID, ID: rec.ID, Path: rec.Path, Status: "completed", Changed: rec.Changed}
}
func sameRepair(rec repairRecord, req RepairRequest) bool {
	return rec.ID == req.ID && rec.Owner == req.Owner && rec.Token == req.Token && rec.Path == req.Path && rec.ExpectedSHA256 == req.ExpectedSHA256
}
func validateRepairRequest(req RepairRequest) error {
	if !validClaimID(req.ID) || !isWorkTask(req.ID) || !validClaimOwner(req.Owner) || !claimToken.MatchString(req.RequestID) || !sharedHex64.MatchString(req.ExpectedSHA256) {
		return errors.New("invalid status repair request")
	}
	if req.Token != "" && !claimToken.MatchString(req.Token) {
		return errors.New("invalid repair token")
	}
	if req.Path == "" || filepath.IsAbs(req.Path) || filepath.Clean(req.Path) != req.Path || strings.ContainsAny(req.Path, "\\\x00") || strings.HasPrefix(req.Path, "../") {
		return errors.New("invalid repair path")
	}
	return nil
}
func validateRepairClaim(j claimsLedger, req RepairRequest) error {
	held := false
	for _, c := range j.Records {
		if sameIdentity(c.ID, req.ID) && c.Status == "held" {
			held = true
			if c.Owner != req.Owner || c.Token != req.Token {
				return errors.New("repair requires the exact held claim")
			}
		}
	}
	if !held && req.Token != "" {
		return errors.New("repair token supplied without a held claim")
	}
	return nil
}
func isTopLevelWorkflowPath(name string, policy boardpolicy.Policy) bool {
	return name != "" && filepath.Clean(name) == name && !filepath.IsAbs(name) && !strings.ContainsAny(name, "\\\x00") && policy.Workflow(filepath.Dir(name)) && filepath.Ext(name) == ".md" && !strings.HasPrefix(filepath.Base(name), ".") && !cardpath.IsDocumentation(filepath.Base(name))
}
func inspectRepairFile(r *os.Root, rec repairRecord) (bool, error) {
	if err := relocationParents(r, rec.Path, false); err != nil {
		return false, err
	}
	parent, err := r.Lstat(filepath.Dir(rec.Path))
	if err != nil {
		return false, err
	}
	if !parent.IsDir() || parent.Mode()&os.ModeSymlink != 0 {
		return false, errors.New("repair parent is not a real directory")
	}
	info, err := r.Lstat(rec.Path)
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() || uint32(info.Mode().Perm()) != rec.Mode {
		return false, errors.New("repair source mode or type changed")
	}
	raw, err := readTransitionCard(r, rec.Path)
	if err != nil {
		return false, err
	}
	if bytes.Equal(raw, rec.Patched) {
		return true, nil
	}
	if bytes.Equal(raw, rec.Original) {
		return false, nil
	}
	return false, errors.New("repair source differs from original and patched bytes")
}
