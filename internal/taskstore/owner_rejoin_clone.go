package taskstore

import (
	"bytes"
	"errors"
	"sort"

	"github.com/Gizzahub/taskchain-task-manager/internal/boardpolicy"
	"github.com/Gizzahub/taskchain-task-manager/internal/outputvocab"
)

// An independent clone does not share the source common directory, so the
// source's common-only ID reservations cannot be reconstructed from the tracked
// board alone: the ledger a returning worktree carries only proves what that
// worktree saw.  Bootstrapping without that evidence would let the clone hand
// out IDs the source has already issued, which is precisely the corruption the
// namespace exists to prevent.  The bootstrap therefore refuses unless the
// operator supplies either explicit reservation evidence or an export of the
// source common state, from which the same evidence is derived here.
//
// OwnerRejoinCloneReservationEvidence turns such an export into the explicit
// evidence the plan records.  The export is the exact bytes of the source
// common state; nothing about it is trusted beyond what the ordinary common
// state decoder already validates.
func OwnerRejoinCloneReservationEvidence(export, ledgerRaw []byte, sourceNamespace string) ([]ReservationFloor, []string, error) {
	if !sharedHex32.MatchString(sourceNamespace) {
		return nil, nil, errors.New("source export namespace invalid")
	}
	state, err := decodeSharedState(export)
	if err != nil {
		return nil, nil, err
	}
	// The export has crossed a trust boundary as a plain file, so it must be
	// the exact bytes the source would have written, not merely something that
	// decodes to the same state.
	canonical, err := sharedStateCanonicalBytes(state)
	if err != nil || !bytes.Equal(canonical, export) {
		return nil, nil, errors.New("source export is not the exact common state bytes")
	}
	if state.NamespaceID != sourceNamespace {
		return nil, nil, errors.New("source export belongs to another namespace")
	}
	if state.PendingOwnerRejoin != nil || state.PendingRepair != nil || state.PendingRelocation != nil || state.PendingArchive != nil || state.PendingArchiveCapacity != nil || state.PendingBundle != nil {
		return nil, nil, errors.New("source export has a pending transaction; export the source only when it is quiescent")
	}
	ledger, err := decodeIDs(ledgerRaw)
	if err != nil || ledger.Namespace != sourceNamespace {
		return nil, nil, errors.New("source export cannot be compared against this board's ID ledger")
	}
	tracked := make(map[string]bool, len(ledger.Reserved))
	for _, id := range ledger.Reserved {
		tracked[id] = true
	}
	additional := []string{}
	for _, id := range state.Reserved {
		if identityKey(id) != id || id == "" {
			return nil, nil, errors.New("source export reserved ID is not canonical")
		}
		if !tracked[id] {
			additional = append(additional, id)
		}
	}
	sort.Strings(additional)
	for i := 1; i < len(additional); i++ {
		if additional[i-1] == additional[i] {
			return nil, nil, errors.New("source export reserved IDs are not unique")
		}
	}
	floors := append([]ReservationFloor{}, state.ReservationFloors...)
	if err := validateReservationFloors(floors); err != nil {
		return nil, nil, err
	}
	if len(floors) == 0 && len(additional) == 0 {
		return nil, nil, errors.New("source export carries no reservation evidence beyond this board's ledger; supply an explicit reservation floor instead")
	}
	return floors, additional, nil
}

// PrepareIndependentCloneOwnerRejoin derives the complete protocol-6 target set
// for a board whose clone does NOT share the source common directory.  The
// clone mints its own namespace and, where the source board bore a policy, its
// own policy authority over the same policy bytes; the source namespace and
// authority survive only as provenance in the plan header.  The writer fence on
// the source is an operator assertion (SourceFencedNonempty) - there is no
// distributed lock between clones and no automatic reconciliation.
func PrepareIndependentCloneOwnerRejoin(p OwnerRejoinPlan, sources []OwnerRejoinSourceFile) (OwnerRejoinPlan, []byte, []byte, error) {
	if err := validateIndependentCloneOwnerRejoinHeader(p); err != nil {
		return OwnerRejoinPlan{}, nil, nil, err
	}
	byRole, err := ownerRejoinSourcesByRole(p, sources)
	if err != nil {
		return OwnerRejoinPlan{}, nil, nil, err
	}
	derived, err := deriveIndependentCloneOwnerRejoin(p, byRole)
	if err != nil {
		return OwnerRejoinPlan{}, nil, nil, err
	}
	p.Files, p.Artifacts, p.ArchiveCards = derived.metadata, []OwnerRejoinArtifact{derived.artifact}, derived.archiveCards
	p, payload, err := BindOwnerRejoinPayload(p, derived.files)
	if err != nil {
		return OwnerRejoinPlan{}, nil, nil, err
	}
	return p, payload, derived.capacityPayload, nil
}

func ownerRejoinSourcesByRole(p OwnerRejoinPlan, sources []OwnerRejoinSourceFile) (map[string]OwnerRejoinSourceFile, error) {
	roles := sameCommonOwnerRejoinRoles(p.SourcePolicyAuthority != "")
	if len(sources) != len(roles) {
		return nil, errors.New("owner rejoin source set incomplete")
	}
	byRole := make(map[string]OwnerRejoinSourceFile, len(sources))
	for _, source := range sources {
		if _, exists := byRole[source.Role]; exists || source.Mode == 0 || source.Mode&^0777 != 0 || source.Raw == nil {
			return nil, errors.New("owner rejoin source set invalid")
		}
		byRole[source.Role] = source
	}
	for _, role := range roles {
		if _, ok := byRole[role]; !ok {
			return nil, errors.New("owner rejoin source role missing")
		}
	}
	return byRole, nil
}

func validateIndependentCloneOwnerRejoinHeader(p OwnerRejoinPlan) error {
	if p.Files != nil {
		if err := validateOwnerRejoinPlan(p, p.PayloadSHA256 != ""); err != nil {
			return err
		}
	} else if p.SchemaVersion != 1 || !sharedHex32.MatchString(p.RejoinID) || !validSharedRoot(p.SourceOwner) || !validSharedRoot(p.TargetOwner) || p.SourceOwner == p.TargetOwner || !validSharedBoardPath(p.BoardPath) || !sharedHex40Or64.MatchString(p.SourceHEAD) || !sharedHex64.MatchString(p.SourceRefsSHA256) || !sharedHex64.MatchString(p.SourceInventorySHA256) || !sharedHex32.MatchString(p.SourceNamespace) || !sharedHex32.MatchString(p.TargetNamespace) || p.ReservationFloors == nil || p.AdditionalReservedIDs == nil || validateReservationFloors(p.ReservationFloors) != nil || !sortedUniqueIDs(p.AdditionalReservedIDs) {
		return errors.New("invalid independent clone owner rejoin preparation header")
	}
	if p.SourceCommonAvailable || !p.SourceFencedNonempty || p.SourceStorageProtocol != 5 || p.TargetStorageProtocol != 6 {
		return errors.New("invalid independent clone owner rejoin header")
	}
	if p.SourceNamespace == p.TargetNamespace {
		return errors.New("independent clone rejoin must mint its own namespace")
	}
	if (p.SourcePolicyAuthority == "") != (p.TargetPolicyAuthority == "") || p.SourcePolicySHA256 != p.TargetPolicySHA256 {
		return errors.New("independent clone rejoin must preserve policy presence and digest")
	}
	if p.SourcePolicyAuthority != "" && p.SourcePolicyAuthority == p.TargetPolicyAuthority {
		return errors.New("independent clone rejoin must mint its own policy authority")
	}
	if len(p.ReservationFloors) == 0 && len(p.AdditionalReservedIDs) == 0 {
		return errors.New("independent clone rejoin requires an explicit reservation floor, additional reserved IDs, or a source export")
	}
	return nil
}

func cloneOwnerRejoinBindings(p OwnerRejoinPlan) (policyAuthorityBinding, policyAuthorityBinding) {
	if p.SourcePolicyAuthority == "" {
		return policyAuthorityBinding{}, policyAuthorityBinding{}
	}
	return policyAuthorityBinding{AuthorityID: p.SourcePolicyAuthority, Scope: "shared", Namespace: p.SourceNamespace},
		policyAuthorityBinding{AuthorityID: p.TargetPolicyAuthority, Scope: "shared", Namespace: p.TargetNamespace}
}

func deriveIndependentCloneOwnerRejoin(p OwnerRejoinPlan, sources map[string]OwnerRejoinSourceFile) (ownerRejoinDerived, error) {
	var out ownerRejoinDerived
	sourceBoard, targetBoard := p.SourceOwner, p.TargetOwner
	if err := validateOwnerRejoinBoards(sourceBoard, targetBoard); err != nil {
		return out, err
	}
	sourceBinding, targetBinding := cloneOwnerRejoinBindings(p)
	targets := map[string][]byte{}
	var err error
	if targets["transitions"], err = transformOwnerRejoinCloneTransitions(sources["transitions"].Raw, sourceBinding, targetBinding); err != nil {
		return out, err
	}
	if targets["ids"], err = transformOwnerRejoinIDs(sources["ids"].Raw, p.SourceNamespace, p.TargetNamespace, p.ReservationFloors, p.AdditionalReservedIDs); err != nil {
		return out, err
	}
	if targets["repairs"], err = TransformOwnerRejoinRepairJournal(sources["repairs"].Raw, sourceBoard, targetBoard); err != nil {
		return out, err
	}
	if targets["relocations"], err = TransformOwnerRejoinRelocationJournal(sources["relocations"].Raw, sourceBoard, targetBoard); err != nil {
		return out, err
	}
	archiveSourceNS, archiveTargetNS, err := cloneArchiveNamespaces(sources["archive"].Raw, p)
	if err != nil {
		return out, err
	}
	if targets["archive"], err = transformOwnerRejoinArchiveJournal(sources["archive"].Raw, sourceBoard, targetBoard, archiveSourceNS, archiveTargetNS); err != nil {
		return out, err
	}
	targets["archive-capacity"], out.capacityPayload, out.artifact, err = prepareCloneOwnerRejoinArchiveCapacity(sources["archive-capacity"].Raw, sources["archive"].Raw, targets["archive"], sourceBoard, targetBoard, archiveSourceNS, archiveTargetNS, sources["archive"].Mode)
	if err != nil {
		return out, err
	}
	if err := deriveClonePolicy(p, sources, targets, sourceBoard, targetBoard, sourceBinding, targetBinding); err != nil {
		return out, err
	}
	for _, role := range sameCommonOwnerRejoinRoles(p.SourcePolicyAuthority != "") {
		source, target := sources[role], targets[role]
		meta := OwnerRejoinFile{Role: outputvocab.RejoinRole(role), Path: sameCommonOwnerRejoinPaths[role], OriginalPresent: true, TargetPresent: true, Mode: source.Mode, OriginalLength: len(source.Raw), OriginalSHA256: bytesDigest(source.Raw), TargetLength: len(target), TargetSHA256: bytesDigest(target)}
		out.metadata = append(out.metadata, meta)
		out.files = append(out.files, OwnerRejoinFileBytes{Role: role, Original: append([]byte(nil), source.Raw...), Target: append([]byte(nil), target...)})
	}
	j, _ := decodeArchiveCapacityJournal(targets["archive"])
	for _, record := range j.Records {
		out.archiveCards = append(out.archiveCards, ArchiveNamespaceCardBinding{Path: record.Target, ID: identityKey(record.ID), SHA256: record.FinalSHA256, Mode: record.Mode})
	}
	sort.Slice(out.archiveCards, func(i, j int) bool { return out.archiveCards[i].Path < out.archiveCards[j].Path })
	return out, nil
}

func deriveClonePolicy(p OwnerRejoinPlan, sources map[string]OwnerRejoinSourceFile, targets map[string][]byte, sourceBoard, targetBoard string, sourceBinding, targetBinding policyAuthorityBinding) error {
	transition, _ := decodeTransitionJournal(sources["transitions"].Raw)
	if p.SourcePolicyAuthority == "" {
		if transition.SchemaVersion != 1 || transition.PolicyAuthority != nil || sources["policy"].Raw != nil || sources["policy-activation"].Raw != nil || validateTransitionRecords(transition, boardpolicy.Default()) != nil {
			return errors.New("policy-absent clone rejoin contains policy provenance")
		}
		return nil
	}
	policy, activation := sources["policy"].Raw, sources["policy-activation"].Raw
	parsed, policyErr := boardpolicy.Parse(policy)
	if policyErr != nil || bytesDigest(policy) != p.SourcePolicySHA256 || transition.PolicyDigest != p.SourcePolicySHA256 || transition.PolicyAuthority == nil || *transition.PolicyAuthority != sourceBinding || validateTransitionRecords(transition, parsed) != nil {
		return errors.New("clone rejoin source policy authority differs")
	}
	rebound, err := transformOwnerRejoinClonePolicyActivation(activation, sourceBoard, targetBoard, sourceBinding, targetBinding)
	if err != nil {
		return err
	}
	var state policyActivationState
	if err := decodeExact(activation, &state); err != nil || !bytes.Equal(state.Canonical, policy) || state.Digest != p.SourcePolicySHA256 {
		return errors.New("clone rejoin source policy activation provenance invalid")
	}
	targets["policy"], targets["policy-activation"] = append([]byte(nil), policy...), rebound
	return nil
}

func validateIndependentCloneOwnerRejoinPlan(p OwnerRejoinPlan, files []OwnerRejoinFileBytes) error {
	if err := validateIndependentCloneOwnerRejoinHeader(p); err != nil {
		return err
	}
	roles := sameCommonOwnerRejoinRoles(p.SourcePolicyAuthority != "")
	if len(files) != len(roles) || len(p.Files) != len(roles) || len(p.Artifacts) != 1 {
		return errors.New("independent clone owner rejoin inventory incomplete")
	}
	sources := make(map[string]OwnerRejoinSourceFile, len(files))
	for i, role := range roles {
		meta, file := p.Files[i], files[i]
		if meta.Role != outputvocab.RejoinRole(role) || file.Role != role || meta.Path != sameCommonOwnerRejoinPaths[role] || !meta.OriginalPresent || !meta.TargetPresent || file.Original == nil || file.Target == nil {
			return errors.New("independent clone owner rejoin role or path invalid")
		}
		sources[role] = OwnerRejoinSourceFile{Role: role, Mode: meta.Mode, Raw: file.Original}
	}
	derived, err := deriveIndependentCloneOwnerRejoin(p, sources)
	if err != nil {
		return err
	}
	for i := range files {
		if !bytes.Equal(files[i].Target, derived.files[i].Target) || p.Files[i] != derived.metadata[i] {
			return errors.New("independent clone owner rejoin target differs from derivation")
		}
	}
	if p.Artifacts[0] != derived.artifact || !sameArchiveCardBindings(p.ArchiveCards, derived.archiveCards) {
		return errors.New("independent clone owner rejoin artifact or archive inventory differs")
	}
	return nil
}

// cloneArchiveNamespaces decides which namespace pair the archive journal and
// its capacity receipt move between.  A board that archived before it enabled
// sharing carries an empty namespace there, and an empty namespace asserted no
// authority, so there is nothing for the clone to rebind and it stays empty;
// rebinding it to the clone's new namespace would invent a binding the source
// never made.  Anything else must be exactly the source namespace the plan
// names, which is also what cross-checks the capacity receipt against the
// journal it was adopted for.
func cloneArchiveNamespaces(archive []byte, p OwnerRejoinPlan) (string, string, error) {
	j, err := decodeArchiveCapacityJournal(archive)
	if err != nil {
		return "", "", errors.New("owner rejoin archive journal scope invalid")
	}
	switch j.Namespace {
	case "":
		return "", "", nil
	case p.SourceNamespace:
		return p.SourceNamespace, p.TargetNamespace, nil
	}
	return "", "", errors.New("owner rejoin archive journal namespace is not the plan source namespace")
}
