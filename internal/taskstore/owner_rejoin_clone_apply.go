package taskstore

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"

	"github.com/Gizzahub/taskchain-task-manager/internal/githistory"
)

// inspectCloneOwnerRejoinEvidence brackets the clone's own Git evidence.  The
// source repository is not reachable from an independent clone, so the source
// HEAD, ref digest and worktree inventory in the plan are provenance the
// operator asserted at preparation time and are deliberately NOT re-verified
// here - claiming otherwise would be a lie about what the transaction proved.
// What is proved is that the returning board is a live, unlocked, non-prunable
// worktree of this clone and that the clone's inventory did not move while the
// transaction inspected it.
func inspectCloneOwnerRejoinEvidence(s *sharedSession, p OwnerRejoinPlan) (ownerRejoinEvidence, error) {
	var out ownerRejoinEvidence
	if s == nil || s.location == nil {
		return out, errors.New("owner rejoin requires a Git worktree")
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
	for _, wt := range before.Worktrees {
		board := filepath.Join(wt.Path, filepath.FromSlash(p.BoardPath))
		if board == p.SourceOwner {
			return out, errors.New("clone rejoin source board is a worktree of this clone; use the same-common rejoin instead")
		}
		if board == p.TargetOwner {
			out.target = wt
		}
	}
	if out.target.Path == "" || out.target.Bare || out.target.Prunable || out.target.Locked {
		return out, errors.New("clone rejoin target worktree evidence mismatch")
	}
	out.inventory, out.history = before, history
	return out, nil
}

// preflightCloneOwnerRejoin admits exactly two clone-local common states: none
// at all, which this transaction bootstraps, and one this very rejoin already
// created.  A clone that already carries some other namespace is refused: there
// is no distributed lock between clones and no automatic reconciliation, so
// merging two namespaces is an operator decision, never a side effect.
func preflightCloneOwnerRejoin(s *sharedSession, r *os.Root, p OwnerRejoinPlan, files []OwnerRejoinFileBytes, evidence ownerRejoinEvidence, digest string) error {
	board, err := canonicalStorageBoard(r)
	if err != nil {
		return err
	}
	if s == nil || s.location == nil {
		return errors.New("clone rejoin requires a Git worktree")
	}
	if board != p.TargetOwner {
		return errors.New("owner rejoin target board identity mismatch")
	}
	if p.BoardPath != s.location.Board {
		return errors.New("owner rejoin board path mismatch")
	}
	state := s.state
	if state == nil {
		return validateOwnerRejoinArchiveCards(r, p)
	}
	switch {
	case state.BoardPath != p.BoardPath:
		return errors.New("clone common state board path mismatch")
	case state.NamespaceID == p.SourceNamespace:
		return errors.New("clone already shares the source namespace; use the same-common rejoin instead")
	// These three are separated deliberately.  They are reached when a clone
	// already carries common authority that is not the one this rejoin would
	// have written, and the operator's next move differs in each case, so
	// collapsing them into one message leaves them with a refusal and no
	// action.  Each names the value found, the value required, and what to
	// restore.
	case state.NamespaceID != p.TargetNamespace:
		return fmt.Errorf("clone common state is namespace %s, not the planned target namespace %s; restore the common state this rejoin created, or prepare a rejoin for the namespace the clone already holds", state.NamespaceID, p.TargetNamespace)
	case state.SchemaVersion != 4:
		return fmt.Errorf("clone common state is schema %d, not the schema 4 an owner rejoin writes; restore the common state this rejoin created rather than an earlier copy of it", state.SchemaVersion)
	case state.StorageProtocol != 6:
		return fmt.Errorf("clone common state records storage protocol %d, not the protocol 6 an owner rejoin writes; restore the common state this rejoin created rather than an earlier copy of it", state.StorageProtocol)
	}
	if state.PendingBundle != nil || state.PendingRepair != nil || state.PendingRelocation != nil || state.PendingArchive != nil || state.PendingArchiveDelta != nil || state.PendingArchiveCapacity != nil {
		return errors.New("owner rejoin conflicts with pending common operation")
	}
	if err := cloneOwnerRejoinPolicyMatches(state, p); err != nil {
		return err
	}
	if err := validateOwnerRejoinArchiveCards(r, p); err != nil {
		return err
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

// cloneOwnerRejoinPolicyMatches checks the clone-local authority, which is the
// NEW one.  The source authority never appears in the clone's common state.
func cloneOwnerRejoinPolicyMatches(state *sharedState, p OwnerRejoinPlan) error {
	if p.TargetPolicyAuthority == "" {
		if state.Policy != nil {
			return errors.New("clone rejoin policy presence mismatch")
		}
		return nil
	}
	if state.Policy == nil || state.Policy.AuthorityID != p.TargetPolicyAuthority || state.Policy.Digest != p.TargetPolicySHA256 || state.Policy.Phase != "active" {
		return errors.New("clone rejoin policy authority mismatch")
	}
	return nil
}

func cloneOwnerRejoinPolicyAuthority(p OwnerRejoinPlan, files []OwnerRejoinFileBytes) (*sharedPolicyAuthority, error) {
	if p.TargetPolicyAuthority == "" {
		return nil, nil
	}
	for i, meta := range p.Files {
		if meta.Role != "policy" {
			continue
		}
		canonical := append([]byte(nil), files[i].Target...)
		if bytesDigest(canonical) != p.TargetPolicySHA256 {
			return nil, errors.New("clone rejoin policy bytes contradict the plan digest")
		}
		return &sharedPolicyAuthority{AuthorityID: p.TargetPolicyAuthority, Phase: "active", Canonical: canonical, Digest: p.TargetPolicySHA256, Pending: []policyActivationPlan{}}, nil
	}
	return nil, errors.New("clone rejoin plan lacks the policy it claims to carry")
}

// ensureCloneOwnerRejoinCommonPending creates the clone's own common state on
// first use and is otherwise identical to the same-common marker step: the
// common marker is exclusion only, and the local receipt plus the immutable
// plan and payload remain the recovery authority.
func ensureCloneOwnerRejoinCommonPending(s *sharedSession, p OwnerRejoinPlan, digest string, files []OwnerRejoinFileBytes, evidence ownerRejoinEvidence) error {
	participant, err := ownerRejoinTargetParticipant(p, files, evidence)
	if err != nil {
		return err
	}
	if s.state == nil {
		authority, err := cloneOwnerRejoinPolicyAuthority(p, files)
		if err != nil {
			return err
		}
		floors := append([]ReservationFloor{}, p.ReservationFloors...)
		if floors == nil {
			floors = []ReservationFloor{}
		}
		// The shared-state journal is an on-disk contract distinct from stdout; it
		// deliberately does not read the stdout vocabulary.
		next := sharedState{
			SchemaVersion: 4, NamespaceID: p.TargetNamespace, BoardPath: p.BoardPath, Phase: "initializing",
			Reserved: unionIDs(cloneOwnerRejoinLedgerIDs(p, files), p.AdditionalReservedIDs), ReservationFloors: floors,
			Participants: []sharedParticipant{participant}, Policy: authority, StorageProtocol: 6,
			PendingOwnerRejoin: &sharedOwnerRejoinPending{RejoinID: p.RejoinID, Owner: p.TargetOwner, PlanSHA256: digest, PayloadSHA256: p.PayloadSHA256},
		}
		return s.saveInitialStorageState(next)
	}
	if s.state.PendingOwnerRejoin != nil {
		q := s.state.PendingOwnerRejoin
		if q.RejoinID != p.RejoinID || q.Owner != p.TargetOwner || q.PlanSHA256 != digest || q.PayloadSHA256 != p.PayloadSHA256 {
			return errors.New("different owner rejoin is pending")
		}
		return verifyOwnerRejoinTargetParticipant(s.state, p, files, evidence, true)
	}
	return errors.New("clone common state exists without this rejoin's pending marker; recover explicitly")
}

// cloneOwnerRejoinLedgerIDs reads the reserved set out of the transformed
// ledger the plan publishes, so the clone's common reservations can never be
// narrower than what the board itself will hand out.
func cloneOwnerRejoinLedgerIDs(p OwnerRejoinPlan, files []OwnerRejoinFileBytes) []string {
	for i, meta := range p.Files {
		if meta.Role != "ids" {
			continue
		}
		ledger, err := decodeIDs(files[i].Target)
		if err != nil {
			return nil
		}
		return ledger.Reserved
	}
	return nil
}

func fullRevalidateCloneOwnerRejoin(s *sharedSession, r *os.Root, p OwnerRejoinPlan, files []OwnerRejoinFileBytes, evidence ownerRejoinEvidence, receipt OwnerRejoinReceipt) error {
	after, err := inspectCloneOwnerRejoinEvidence(s, p)
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

// applyOwnerRejoinIndependentClone runs the durable rejoin transaction for a
// board whose clone does not share the source common directory.  It is the same
// two-phase transaction as the same-common path, with the same cutpoints, and
// differs only in where its common authority comes from: it creates one rather
// than joining one.
func applyOwnerRejoinIndependentClone(dir string, plan OwnerRejoinPlan, payload []byte, step func(string) error) (err error) {
	if err := validateOwnerRejoinPlan(plan, true); err != nil || plan.SourceCommonAvailable {
		return errors.New("independent clone owner rejoin requires a strict clone plan")
	}
	files, err := DecodeOwnerRejoinPayload(payload, plan)
	if err != nil {
		return err
	}
	if err := validateIndependentCloneOwnerRejoinPlan(plan, files); err != nil {
		return err
	}
	planRaw, err := OwnerRejoinPlanBytes(plan)
	if err != nil {
		return err
	}
	planDigest := bytesDigest(planRaw)

	shared, release, err := acquireSharedOwnerRejoin(dir)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, release()) }()
	if shared == nil {
		return errors.New("independent clone owner rejoin requires a Git worktree")
	}
	r, err := openBoard(dir)
	if err != nil {
		return err
	}
	unlock, err := lock(r)
	if err != nil {
		return errors.Join(err, r.Close())
	}
	defer func() { err = errors.Join(err, unlock(), r.Close()) }()

	evidence, err := inspectCloneOwnerRejoinEvidence(shared, plan)
	if err != nil {
		return err
	}
	if err := preflightCloneOwnerRejoin(shared, r, plan, files, evidence, planDigest); err != nil {
		return err
	}
	completed := ownerRejoinReceipt(plan, planDigest, "completed")
	if ownerRejoinCommonCompleted(shared.state, plan, planDigest) {
		return validateOwnerRejoinLocal(r, plan, completed, true)
	}
	if raw, err := loadOwnerRejoinArtifact(r, ownerRejoinReceiptFile, 4096); err == nil {
		if got, decErr := DecodeOwnerRejoinReceipt(raw, plan); decErr == nil && got == completed {
			if err := validateOwnerRejoinLocal(r, plan, completed, true); err != nil {
				return err
			}
			if err := completeOwnerRejoinCommon(shared, plan, planDigest); err != nil {
				return err
			}
			return storageStep(step, "after-owner-rejoin-common-clear")
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}

	planName, _ := ownerRejoinPlanArtifactName(planDigest)
	if err := publishOwnerRejoinArtifact(r, planName, planRaw, 1<<20); err != nil {
		return err
	}
	if err := storageStep(step, "after-owner-rejoin-plan"); err != nil {
		return err
	}
	payloadName, _ := ownerRejoinPayloadArtifactName(plan.PayloadSHA256)
	if err := publishOwnerRejoinArtifact(r, payloadName, payload, maxOwnerRejoinPayloadBytes); err != nil {
		return err
	}
	if err := storageStep(step, "after-owner-rejoin-payload"); err != nil {
		return err
	}
	pending := ownerRejoinReceipt(plan, planDigest, "pending")
	if err := ensureOwnerRejoinReceipt(r, plan, pending); err != nil {
		return err
	}
	if err := storageStep(step, "after-owner-rejoin-local-pending"); err != nil {
		return err
	}
	if err := ensureCloneOwnerRejoinCommonPending(shared, plan, planDigest, files, evidence); err != nil {
		return err
	}
	if err := storageStep(step, "after-owner-rejoin-common-pending"); err != nil {
		return err
	}
	if err := publishOwnerRejoinTargets(r, plan, files, step); err != nil {
		return err
	}
	if err := fullRevalidateCloneOwnerRejoin(shared, r, plan, files, evidence, pending); err != nil {
		return err
	}
	if err := storageStep(step, "after-owner-rejoin-full-revalidate"); err != nil {
		return err
	}
	if err := replaceOwnerRejoinReceipt(r, plan, pending, completed); err != nil {
		return err
	}
	if err := storageStep(step, "after-owner-rejoin-local-completed"); err != nil {
		return err
	}
	if err := validateOwnerRejoinLocal(r, plan, completed, true); err != nil {
		return err
	}
	if err := completeOwnerRejoinCommon(shared, plan, planDigest); err != nil {
		return err
	}
	return storageStep(step, "after-owner-rejoin-common-clear")
}
