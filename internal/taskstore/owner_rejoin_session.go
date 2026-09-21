package taskstore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"

	"github.com/Gizzahub/taskchain-task-manager/internal/githistory"
)

type ownerRejoinEvidence struct {
	inventory githistory.WorktreeReport
	history   githistory.Report
	source    githistory.Worktree
	target    githistory.Worktree
}

// inspectOwnerRejoinEvidence brackets the ref scan with identical worktree
// inventories. Both digests use the already ordered public report slices.
func inspectOwnerRejoinEvidence(s *sharedSession, p OwnerRejoinPlan) (ownerRejoinEvidence, error) {
	var out ownerRejoinEvidence
	if s == nil || s.location == nil {
		return out, errors.New("owner rejoin requires a shared Git worktree")
	}
	ctx := context.Background()
	before, err := githistory.InspectWorktrees(ctx, s.location.Repository, s.location.Board)
	if err != nil {
		return out, err
	}
	history, err := githistory.Scan(ctx, s.location.Repository, s.location.Board)
	if err != nil {
		return out, err
	}
	after, err := githistory.InspectWorktrees(ctx, s.location.Repository, s.location.Board)
	if err != nil || !reflect.DeepEqual(before, after) {
		return out, errors.New("owner rejoin Git evidence changed during inspection")
	}
	refsRaw, err := json.Marshal(history.Refs)
	if err != nil || bytesDigest(refsRaw) != p.SourceRefsSHA256 {
		return out, errors.New("owner rejoin ref evidence mismatch")
	}
	worktreesRaw, err := json.Marshal(before.Worktrees)
	if err != nil || bytesDigest(worktreesRaw) != p.SourceInventorySHA256 {
		return out, errors.New("owner rejoin worktree evidence mismatch")
	}
	for _, wt := range before.Worktrees {
		board := filepath.Join(wt.Path, filepath.FromSlash(p.BoardPath))
		switch board {
		case p.SourceOwner:
			out.source = wt
		case p.TargetOwner:
			out.target = wt
		}
	}
	if out.source.Path == "" || out.target.Path == "" || out.source.Bare || out.target.Bare || out.source.Prunable || out.target.Prunable || out.source.Locked || out.target.Locked || out.source.HEAD != p.SourceHEAD || out.target.HEAD != p.SourceHEAD {
		return out, errors.New("owner rejoin source or target worktree evidence mismatch")
	}
	out.inventory, out.history = before, history
	return out, nil
}

func preflightOwnerRejoin(s *sharedSession, r *os.Root, p OwnerRejoinPlan, files []OwnerRejoinFileBytes, evidence ownerRejoinEvidence, digest string) error {
	board, err := canonicalStorageBoard(r)
	if err != nil {
		return err
	}
	state := s.state
	switch {
	case state == nil:
		return errors.New("owner rejoin missing common state")
	case board != p.TargetOwner:
		return errors.New("owner rejoin target board identity mismatch")
	case p.BoardPath != state.BoardPath || p.BoardPath != s.location.Board:
		return errors.New("owner rejoin board path mismatch")
	case state.NamespaceID != p.SourceNamespace || state.NamespaceID != p.TargetNamespace:
		return errors.New("owner rejoin namespace mismatch")
	case state.StorageProtocol == 6 && state.SchemaVersion == 4:
		// Resume of an in-progress or completed same-common rejoin.
	case state.StorageProtocol == p.SourceStorageProtocol && state.SchemaVersion >= 1 && state.SchemaVersion <= 3:
		// Compatible c5c common authority, with or without policy schema.
	default:
		return errors.New("owner rejoin common protocol mismatch")
	}
	if state.PendingBundle != nil || state.PendingRepair != nil || state.PendingRelocation != nil || state.PendingArchive != nil || state.PendingArchiveDelta != nil || state.PendingArchiveCapacity != nil || (state.Policy != nil && state.Policy.Phase != "active") {
		return errors.New("owner rejoin conflicts with pending common operation")
	}
	if err := ownerRejoinPolicyMatches(state, p); err != nil {
		return err
	}
	sourceFound := false
	for _, participant := range state.Participants {
		if filepath.Join(participant.Root, filepath.FromSlash(state.BoardPath)) == p.SourceOwner {
			sourceFound = participant.Root == evidence.source.Path && participant.HEAD == p.SourceHEAD
		}
	}
	if !sourceFound {
		return errors.New("owner rejoin source participant binding mismatch")
	}
	if err := validateOwnerRejoinArchiveCards(r, p); err != nil {
		return err
	}
	if len(state.CompletedOwnerRejoins) >= 256 || (state.PendingOwnerRejoin == nil && len(state.Participants) >= 256) {
		return errors.New("owner rejoin common capacity exceeded")
	}
	for _, done := range state.CompletedOwnerRejoins {
		if done.RejoinID == p.RejoinID {
			if ownerRejoinCommonCompleted(state, p, digest) {
				return nil
			}
			return errors.New("owner rejoin ID was already used")
		}
	}
	if q := state.PendingOwnerRejoin; q != nil && (q.RejoinID != p.RejoinID || q.Owner != p.TargetOwner || q.PlanSHA256 != digest || q.PayloadSHA256 != p.PayloadSHA256) {
		return errors.New("different owner rejoin is pending")
	}
	return verifyOwnerRejoinTargetParticipant(state, p, files, evidence, state.PendingOwnerRejoin != nil)
}

func ownerRejoinPolicyMatches(state *sharedState, p OwnerRejoinPlan) error {
	if p.SourcePolicyAuthority == "" {
		if state.Policy != nil {
			return errors.New("owner rejoin policy presence mismatch")
		}
		return nil
	}
	if state.Policy == nil || state.Policy.AuthorityID != p.SourcePolicyAuthority || state.Policy.Digest != p.SourcePolicySHA256 {
		return errors.New("owner rejoin policy authority mismatch")
	}
	return nil
}

func ownerRejoinTargetParticipant(p OwnerRejoinPlan, files []OwnerRejoinFileBytes, evidence ownerRejoinEvidence) (sharedParticipant, error) {
	participant := sharedParticipant{Root: evidence.target.Path, HEAD: evidence.target.HEAD, Snapshot: ownerRejoinAggregate(p.Files, true)}
	found := false
	for i, meta := range p.Files {
		if meta.Path != idsFile {
			continue
		}
		if !meta.TargetPresent {
			return participant, errors.New("owner rejoin target participant lacks ID ledger")
		}
		participant.OriginalLedger = bytesDigest(files[i].Original)
		participant.TargetLedger = bytesDigest(files[i].Target)
		found = true
	}
	if !found {
		return participant, errors.New("owner rejoin plan lacks ID ledger")
	}
	return participant, nil
}

func verifyOwnerRejoinTargetParticipant(state *sharedState, p OwnerRejoinPlan, files []OwnerRejoinFileBytes, evidence ownerRejoinEvidence, require bool) error {
	want, err := ownerRejoinTargetParticipant(p, files, evidence)
	if err != nil {
		return err
	}
	found := false
	for _, participant := range state.Participants {
		if participant.Root == want.Root {
			if !reflect.DeepEqual(participant, want) {
				return errors.New("owner rejoin target participant conflicts")
			}
			found = true
		}
	}
	if found != require {
		return errors.New("owner rejoin target participant presence mismatch")
	}
	return nil
}

func ensureOwnerRejoinCommonPending(s *sharedSession, p OwnerRejoinPlan, digest string, files []OwnerRejoinFileBytes, evidence ownerRejoinEvidence) error {
	if s.state.PendingOwnerRejoin != nil {
		q := s.state.PendingOwnerRejoin
		if q.RejoinID != p.RejoinID || q.Owner != p.TargetOwner || q.PlanSHA256 != digest || q.PayloadSHA256 != p.PayloadSHA256 {
			return errors.New("different owner rejoin is pending")
		}
		return verifyOwnerRejoinTargetParticipant(s.state, p, files, evidence, true)
	}
	if len(s.state.Participants) >= 256 || len(s.state.CompletedOwnerRejoins) >= 256 {
		return errors.New("owner rejoin common capacity exceeded")
	}
	for _, done := range s.state.CompletedOwnerRejoins {
		if done.RejoinID == p.RejoinID {
			return errors.New("owner rejoin ID was already used")
		}
	}
	participant, err := ownerRejoinTargetParticipant(p, files, evidence)
	if err != nil {
		return err
	}
	next := *s.state
	next.Participants = append(append([]sharedParticipant(nil), next.Participants...), participant)
	sort.Slice(next.Participants, func(i, j int) bool { return next.Participants[i].Root < next.Participants[j].Root })
	next.Reserved = unionIDs(next.Reserved, p.AdditionalReservedIDs)
	next.ReservationFloors, err = unionReservationFloors(next.ReservationFloors, p.ReservationFloors)
	if err != nil {
		return err
	}
	if next.ReservationFloors == nil {
		next.ReservationFloors = []ReservationFloor{}
	}
	// The shared-state journal is an on-disk contract distinct from stdout; it
	// deliberately does not read the stdout vocabulary.
	next.SchemaVersion, next.Phase, next.StorageProtocol = 4, "initializing", 6
	next.PendingOwnerRejoin = &sharedOwnerRejoinPending{RejoinID: p.RejoinID, Owner: p.TargetOwner, PlanSHA256: digest, PayloadSHA256: p.PayloadSHA256}
	return s.saveStorageState(next)
}

func completeOwnerRejoinCommon(s *sharedSession, p OwnerRejoinPlan, digest string) error {
	q := s.state.PendingOwnerRejoin
	if q == nil || q.RejoinID != p.RejoinID || q.Owner != p.TargetOwner || q.PlanSHA256 != digest || q.PayloadSHA256 != p.PayloadSHA256 || len(s.state.CompletedOwnerRejoins) >= 256 {
		return errors.New("owner rejoin common pending marker changed or history is full")
	}
	for _, done := range s.state.CompletedOwnerRejoins {
		if done.RejoinID == p.RejoinID {
			return errors.New("owner rejoin ID was already completed")
		}
	}
	next := *s.state
	next.Phase, next.PendingOwnerRejoin = "active", nil
	next.CompletedOwnerRejoins = append(append([]sharedOwnerRejoinDone(nil), next.CompletedOwnerRejoins...), sharedOwnerRejoinDone{RejoinID: p.RejoinID, Owner: p.TargetOwner, PlanSHA256: digest, PayloadSHA256: p.PayloadSHA256})
	return s.saveStorageState(next)
}

func fullRevalidateOwnerRejoin(s *sharedSession, r *os.Root, p OwnerRejoinPlan, files []OwnerRejoinFileBytes, evidence ownerRejoinEvidence, receipt OwnerRejoinReceipt) error {
	after, err := inspectOwnerRejoinEvidence(s, p)
	if err != nil || !reflect.DeepEqual(evidence, after) {
		return errors.New("owner rejoin Git evidence changed before completion")
	}
	if err := verifyOwnerRejoinTargetParticipant(s.state, p, files, evidence, true); err != nil {
		return err
	}
	if err := validateOwnerRejoinArchiveCards(r, p); err != nil {
		return err
	}
	return validateOwnerRejoinLocalRoot(r, p, files, receipt, true)
}

func validateOwnerRejoinArchiveCards(r *os.Root, p OwnerRejoinPlan) error {
	for _, card := range p.ArchiveCards {
		info, err := r.Lstat(card.Path)
		if err != nil || !info.Mode().IsRegular() || uint32(info.Mode().Perm()) != card.Mode {
			return errors.New("owner rejoin archive card type or mode mismatch")
		}
		raw, err := boundedSnapshotFile(r, card.Path, maxCardBytes)
		if err != nil || bytesDigest(raw) != card.SHA256 {
			return errors.New("owner rejoin archive card bytes mismatch")
		}
	}
	return nil
}

func validateOwnerRejoinLocalRoot(r *os.Root, p OwnerRejoinPlan, files []OwnerRejoinFileBytes, receipt OwnerRejoinReceipt, targetOnly bool) error {
	if err := ensureOwnerRejoinReceipt(r, p, receipt); err != nil {
		return err
	}
	for i, meta := range p.Files {
		if err := verifyOwnerRejoinFileStage(r, meta, files[i], targetOnly); err != nil {
			return err
		}
	}
	return nil
}

func validateOwnerRejoinLocal(r *os.Root, p OwnerRejoinPlan, receipt OwnerRejoinReceipt, targetOnly bool) error {
	planRaw, err := OwnerRejoinPlanBytes(p)
	if err != nil {
		return err
	}
	planName, _ := ownerRejoinPlanArtifactName(receipt.PlanSHA256)
	storedPlan, err := loadOwnerRejoinArtifact(r, planName, 1<<20)
	if err != nil || !bytes.Equal(storedPlan, planRaw) || bytesDigest(storedPlan) != receipt.PlanSHA256 {
		return errors.New("owner rejoin retained plan artifact mismatch")
	}
	payloadName, _ := ownerRejoinPayloadArtifactName(p.PayloadSHA256)
	payload, err := loadOwnerRejoinArtifact(r, payloadName, maxOwnerRejoinPayloadBytes)
	if err != nil {
		return err
	}
	files, err := DecodeOwnerRejoinPayload(payload, p)
	if err != nil {
		return err
	}
	return validateOwnerRejoinLocalRoot(r, p, files, receipt, targetOnly)
}
