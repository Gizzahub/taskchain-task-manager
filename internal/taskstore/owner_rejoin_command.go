package taskstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/Gizzahub/taskchain-task-manager/internal/githistory"
)

// RejoinBoardOptions is the complete operator instruction for one owner-rejoin
// step.  Every value the transaction binds is named here rather than inferred:
// a rejoin moves a board's authority, and a default guessed on the operator's
// behalf is exactly the kind of silent choice this contract exists to prevent.
type RejoinBoardOptions struct {
	Dir                   string
	SourceOwner           string
	Clone                 bool
	RejoinID              string
	TargetNamespace       string
	TargetPolicyAuthority string
	ReservationFloors     []ReservationFloor
	AdditionalReservedIDs []string
	SourceExport          []byte
	SourceFencedNonempty  bool
}

// RejoinBoardResult is the requested data of a rejoin step.  It reports what
// the board now binds, never advice; diagnostics belong on the caller's error
// channel.
type RejoinBoardResult struct {
	Mode                  string                `json:"mode"`
	Phase                 string                `json:"phase"`
	RejoinID              string                `json:"rejoinId"`
	SourceOwner           string                `json:"sourceOwner"`
	TargetOwner           string                `json:"targetOwner"`
	SourceNamespace       string                `json:"sourceNamespace"`
	TargetNamespace       string                `json:"targetNamespace"`
	SourcePolicyAuthority string                `json:"sourcePolicyAuthority"`
	TargetPolicyAuthority string                `json:"targetPolicyAuthority"`
	SourceStorageProtocol int                   `json:"sourceStorageProtocol"`
	TargetStorageProtocol int                   `json:"targetStorageProtocol"`
	PlanSHA256            string                `json:"planSha256"`
	PayloadSHA256         string                `json:"payloadSha256"`
	ReservationFloors     []ReservationFloor    `json:"reservationFloors"`
	AdditionalReservedIDs []string              `json:"additionalReservedIds"`
	Files                 []OwnerRejoinFile     `json:"files"`
	Artifacts             []OwnerRejoinArtifact `json:"artifacts"`
}

func rejoinMode(clone bool) string {
	if clone {
		return "independent-clone"
	}
	return "same-common"
}

// PrepareRejoinBoard derives the immutable plan, payload and capacity artifact
// for a returning board.  It only reads: nothing is published here, so a
// prepare that is interrupted leaves both boards exactly as they were.
func PrepareRejoinBoard(opts RejoinBoardOptions) (RejoinBoardResult, []byte, []byte, []byte, error) {
	var zero RejoinBoardResult
	target, err := filepath.EvalSymlinks(opts.Dir)
	if err != nil {
		return zero, nil, nil, nil, err
	}
	source, err := filepath.EvalSymlinks(opts.SourceOwner)
	if err != nil {
		return zero, nil, nil, nil, err
	}
	p, sources, err := rejoinBoardPlanHeader(opts, source, target)
	if err != nil {
		return zero, nil, nil, nil, err
	}
	prepare := PrepareSameCommonOwnerRejoin
	if opts.Clone {
		prepare = PrepareIndependentCloneOwnerRejoin
	}
	plan, payload, capacity, err := prepare(p, sources)
	if err != nil {
		return zero, nil, nil, nil, err
	}
	planRaw, err := OwnerRejoinPlanBytes(plan)
	if err != nil {
		return zero, nil, nil, nil, err
	}
	return rejoinBoardResult(plan, "prepared"), planRaw, payload, capacity, nil
}

// rejoinBoardPlanHeader assembles the plan header from what can be observed
// and what the operator asserts, keeping the two apart.  The source Git
// evidence is observed for a same-common rejoin, where the source repository
// is reachable; for an independent clone it is recorded as the operator's
// assertion, because the source is by definition not reachable from here.
func rejoinBoardPlanHeader(opts RejoinBoardOptions, source, target string) (OwnerRejoinPlan, []OwnerRejoinSourceFile, error) {
	var zero OwnerRejoinPlan
	if !opts.SourceFencedNonempty {
		return zero, nil, errors.New("owner rejoin requires the operator to assert the source writer fence")
	}
	location, err := githistory.LocateBoard(context.Background(), source)
	if err != nil || location == nil {
		return zero, nil, fmt.Errorf("locate source board: %w", err)
	}
	state, err := rejoinBoardSourceState(opts, location)
	if err != nil {
		return zero, nil, err
	}
	inventory, err := githistory.InspectWorktrees(context.Background(), location.Repository, location.Board)
	if err != nil {
		return zero, nil, err
	}
	history, err := githistory.Scan(context.Background(), location.Repository, location.Board)
	if err != nil {
		return zero, nil, err
	}
	refsRaw, err := json.Marshal(history.Refs)
	if err != nil {
		return zero, nil, err
	}
	worktreesRaw, err := json.Marshal(inventory.Worktrees)
	if err != nil {
		return zero, nil, err
	}
	head := ""
	for _, wt := range inventory.Worktrees {
		if filepath.Join(wt.Path, location.Board) == source {
			head = wt.HEAD
		}
	}
	p := OwnerRejoinPlan{
		SchemaVersion: 1, RejoinID: opts.RejoinID, SourceOwner: source, TargetOwner: target,
		BoardPath: location.Board, SourceHEAD: head, SourceRefsSHA256: bytesDigest(refsRaw), SourceInventorySHA256: bytesDigest(worktreesRaw),
		SourceNamespace: state.NamespaceID, TargetNamespace: state.NamespaceID,
		SourceCommonAvailable: !opts.Clone, SourceFencedNonempty: true,
		SourceStorageProtocol: state.StorageProtocol, TargetStorageProtocol: 6,
		ReservationFloors: opts.ReservationFloors, AdditionalReservedIDs: opts.AdditionalReservedIDs,
	}
	if p.ReservationFloors == nil {
		p.ReservationFloors = []ReservationFloor{}
	}
	if p.AdditionalReservedIDs == nil {
		p.AdditionalReservedIDs = []string{}
	}
	if p.SourceStorageProtocol == 0 {
		p.SourceStorageProtocol = 5
	}
	if state.Policy != nil {
		p.SourcePolicyAuthority, p.SourcePolicySHA256 = state.Policy.AuthorityID, state.Policy.Digest
		p.TargetPolicyAuthority, p.TargetPolicySHA256 = state.Policy.AuthorityID, state.Policy.Digest
	}
	if opts.Clone {
		p.TargetNamespace = opts.TargetNamespace
		if state.Policy != nil {
			p.TargetPolicyAuthority = opts.TargetPolicyAuthority
		} else if opts.TargetPolicyAuthority != "" {
			return zero, nil, errors.New("target policy authority given for a board that carries no policy")
		}
	} else if opts.TargetNamespace != "" || opts.TargetPolicyAuthority != "" {
		return zero, nil, errors.New("same-common rejoin keeps the source namespace and policy authority")
	}
	sources, err := rejoinBoardSourceFiles(source, state.Policy != nil)
	if err != nil {
		return zero, nil, err
	}
	if opts.Clone && len(opts.SourceExport) > 0 {
		// The export is the only record of what the source's other worktrees
		// reserved through the common directory, and this board's own ledger
		// cannot show those.  Deriving them here is not a convenience: leaving
		// them out would let the clone reissue IDs the source already handed
		// out, which is exactly what the bootstrap refusal exists to prevent.
		// Operator-supplied evidence is merged with it rather than replaced by
		// it, so neither source of evidence can quietly drop a reservation.
		ledger, err := ownerRejoinSourceRoleBytes(sources, "ids")
		if err != nil {
			return zero, nil, err
		}
		floors, additional, err := OwnerRejoinCloneReservationEvidence(opts.SourceExport, ledger, p.SourceNamespace)
		if err != nil {
			return zero, nil, err
		}
		if p.ReservationFloors, err = unionReservationFloors(p.ReservationFloors, floors); err != nil {
			return zero, nil, err
		}
		if p.AdditionalReservedIDs, err = mergeReservedIDs(p.AdditionalReservedIDs, additional); err != nil {
			return zero, nil, err
		}
	}
	return p, sources, nil
}

func ownerRejoinSourceRoleBytes(sources []OwnerRejoinSourceFile, role string) ([]byte, error) {
	for _, f := range sources {
		if f.Role == role {
			return f.Raw, nil
		}
	}
	return nil, fmt.Errorf("source %s is missing", role)
}

// mergeReservedIDs unions the two sets.  The plan requires them sorted and
// unique, and an ID that both sides name is one reservation rather than two.
func mergeReservedIDs(a, b []string) ([]string, error) {
	seen := map[string]bool{}
	out := []string{}
	for _, id := range append(append([]string{}, a...), b...) {
		if identityKey(id) != id || id == "" {
			return nil, fmt.Errorf("reserved ID %q is not canonical", id)
		}
		if !seen[id] {
			seen[id], out = true, append(out, id)
		}
	}
	sort.Strings(out)
	return out, nil
}

// rejoinBoardSourceState resolves the source's common state.  A same-common
// rejoin reads it directly, because the returning board shares that very
// directory.  An independent clone cannot reach it at all, so the operator
// supplies an export and its reservation evidence is derived from that export
// rather than assumed.
func rejoinBoardSourceState(opts RejoinBoardOptions, location *githistory.BoardLocation) (sharedState, error) {
	if !opts.Clone {
		raw, err := os.ReadFile(filepath.Join(location.CommonDirectory, "taskchain-task-manager", "ids", location.NamespaceKey, sharedStateFile))
		if err != nil {
			return sharedState{}, fmt.Errorf("read source common state: %w", err)
		}
		return decodeSharedState(raw)
	}
	if len(opts.SourceExport) == 0 {
		if len(opts.ReservationFloors) == 0 && len(opts.AdditionalReservedIDs) == 0 {
			return sharedState{}, errors.New("independent clone rejoin requires an explicit reservation floor, additional reserved IDs, or a source export")
		}
		return sharedState{}, errors.New("independent clone rejoin requires the source common state export that names the source namespace")
	}
	return decodeSharedState(opts.SourceExport)
}

func rejoinBoardSourceFiles(source string, policy bool) ([]OwnerRejoinSourceFile, error) {
	roles := sameCommonOwnerRejoinRoles(policy)
	files := make([]OwnerRejoinSourceFile, 0, len(roles))
	for _, role := range roles {
		name := filepath.Join(source, sameCommonOwnerRejoinPaths[role])
		info, err := os.Lstat(name)
		if err != nil {
			return nil, fmt.Errorf("read source %s: %w", role, err)
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("source %s is not a regular file", role)
		}
		raw, err := os.ReadFile(name)
		if err != nil {
			return nil, err
		}
		files = append(files, OwnerRejoinSourceFile{Role: role, Mode: uint32(info.Mode().Perm()), Raw: raw})
	}
	return files, nil
}

// ApplyRejoinBoard runs the durable rejoin transaction.  Apply and resume are
// the same call deliberately: the transaction is idempotent against its own
// recorded evidence, so an operator who does not know whether a previous
// attempt was interrupted cannot pick the wrong one.
func ApplyRejoinBoard(dir string, planRaw, payload []byte) (RejoinBoardResult, error) {
	var zero RejoinBoardResult
	plan, err := DecodeOwnerRejoinPlan(planRaw)
	if err != nil {
		return zero, err
	}
	apply := applyOwnerRejoinSameCommon
	if !plan.SourceCommonAvailable {
		apply = applyOwnerRejoinIndependentClone
	}
	if err := apply(dir, plan, payload, nil); err != nil {
		return zero, err
	}
	return rejoinBoardResult(plan, "completed"), nil
}

// RejoinBoardStatus reports the board's own recorded rejoin evidence without
// touching it.  It reads only local immutable artifacts, so it answers the same
// way whether or not the common directory is reachable.
func RejoinBoardStatus(dir string) (RejoinBoardResult, error) {
	var zero RejoinBoardResult
	r, err := openBoard(dir)
	if err != nil {
		return zero, err
	}
	defer r.Close()
	plan, err := ownerRejoinCompletedLocal(r)
	if errors.Is(err, errOwnerRejoinAbsent) {
		return RejoinBoardResult{Phase: "absent", ReservationFloors: []ReservationFloor{}, AdditionalReservedIDs: []string{}, Files: []OwnerRejoinFile{}, Artifacts: []OwnerRejoinArtifact{}}, nil
	}
	if err != nil {
		return zero, err
	}
	return rejoinBoardResult(plan, "completed"), nil
}

func rejoinBoardResult(plan OwnerRejoinPlan, phase string) RejoinBoardResult {
	planRaw, _ := OwnerRejoinPlanBytes(plan)
	result := RejoinBoardResult{
		Mode: rejoinMode(!plan.SourceCommonAvailable), Phase: phase, RejoinID: plan.RejoinID,
		SourceOwner: plan.SourceOwner, TargetOwner: plan.TargetOwner,
		SourceNamespace: plan.SourceNamespace, TargetNamespace: plan.TargetNamespace,
		SourcePolicyAuthority: plan.SourcePolicyAuthority, TargetPolicyAuthority: plan.TargetPolicyAuthority,
		SourceStorageProtocol: plan.SourceStorageProtocol, TargetStorageProtocol: plan.TargetStorageProtocol,
		PlanSHA256: bytesDigest(planRaw), PayloadSHA256: plan.PayloadSHA256,
		ReservationFloors: plan.ReservationFloors, AdditionalReservedIDs: plan.AdditionalReservedIDs,
		Files: plan.Files, Artifacts: plan.Artifacts,
	}
	if result.ReservationFloors == nil {
		result.ReservationFloors = []ReservationFloor{}
	}
	if result.AdditionalReservedIDs == nil {
		result.AdditionalReservedIDs = []string{}
	}
	if result.Files == nil {
		result.Files = []OwnerRejoinFile{}
	}
	if result.Artifacts == nil {
		result.Artifacts = []OwnerRejoinArtifact{}
	}
	return result
}
