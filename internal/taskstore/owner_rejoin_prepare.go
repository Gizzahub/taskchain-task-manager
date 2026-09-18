package taskstore

import (
	"bytes"
	"errors"
	"sort"

	"github.com/Gizzahub/taskchain-task-manager/internal/boardpolicy"
)

type OwnerRejoinSourceFile struct {
	Role string
	Mode uint32
	Raw  []byte
}

type ownerRejoinDerived struct {
	files           []OwnerRejoinFileBytes
	metadata        []OwnerRejoinFile
	artifact        OwnerRejoinArtifact
	capacityPayload []byte
	archiveCards    []ArchiveNamespaceCardBinding
}

var sameCommonOwnerRejoinPaths = map[string]string{
	"archive":           archivesFile,
	"archive-capacity":  archiveCapacityFile,
	"ids":               idsFile,
	"policy":            policyFile,
	"policy-activation": policyActivationFile,
	"relocations":       relocationsFile,
	"repairs":           repairsFile,
	"transitions":       transitionsFile,
}

func sameCommonOwnerRejoinRoles(policy bool) []string {
	roles := []string{"archive", "archive-capacity", "ids", "relocations", "repairs", "transitions"}
	if policy {
		roles = append(roles, "policy", "policy-activation")
	}
	sort.Strings(roles)
	return roles
}

// PrepareSameCommonOwnerRejoin derives the complete protocol-5 target set and
// returns the separately transported capacity artifact.
func PrepareSameCommonOwnerRejoin(p OwnerRejoinPlan, sources []OwnerRejoinSourceFile) (OwnerRejoinPlan, []byte, []byte, error) {
	if err := validateSameCommonOwnerRejoinHeader(p); err != nil {
		return OwnerRejoinPlan{}, nil, nil, err
	}
	roles := sameCommonOwnerRejoinRoles(p.SourcePolicyAuthority != "")
	if len(sources) != len(roles) {
		return OwnerRejoinPlan{}, nil, nil, errors.New("same-common owner rejoin source set incomplete")
	}
	byRole := make(map[string]OwnerRejoinSourceFile, len(sources))
	for _, source := range sources {
		if _, exists := byRole[source.Role]; exists || source.Mode == 0 || source.Mode&^0777 != 0 || source.Raw == nil {
			return OwnerRejoinPlan{}, nil, nil, errors.New("same-common owner rejoin source set invalid")
		}
		byRole[source.Role] = source
	}
	for _, role := range roles {
		if _, ok := byRole[role]; !ok {
			return OwnerRejoinPlan{}, nil, nil, errors.New("same-common owner rejoin source role missing")
		}
	}
	derived, err := deriveSameCommonOwnerRejoin(p, byRole)
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

func validateSameCommonOwnerRejoinPlan(p OwnerRejoinPlan, files []OwnerRejoinFileBytes) error {
	if err := validateSameCommonOwnerRejoinHeader(p); err != nil {
		return err
	}
	roles := sameCommonOwnerRejoinRoles(p.SourcePolicyAuthority != "")
	if len(files) != len(roles) || len(p.Files) != len(roles) || len(p.Artifacts) != 1 {
		return errors.New("same-common owner rejoin inventory incomplete")
	}
	sources := make(map[string]OwnerRejoinSourceFile, len(files))
	for i, role := range roles {
		meta, file := p.Files[i], files[i]
		if meta.Role != role || file.Role != role || meta.Path != sameCommonOwnerRejoinPaths[role] || !meta.OriginalPresent || !meta.TargetPresent || file.Original == nil || file.Target == nil {
			return errors.New("same-common owner rejoin role or path invalid")
		}
		sources[role] = OwnerRejoinSourceFile{Role: role, Mode: meta.Mode, Raw: file.Original}
	}
	derived, err := deriveSameCommonOwnerRejoin(p, sources)
	if err != nil {
		return err
	}
	for i := range files {
		if !bytes.Equal(files[i].Target, derived.files[i].Target) || p.Files[i] != derived.metadata[i] {
			return errors.New("same-common owner rejoin target differs from derivation")
		}
	}
	if p.Artifacts[0] != derived.artifact || !sameArchiveCardBindings(p.ArchiveCards, derived.archiveCards) {
		return errors.New("same-common owner rejoin artifact or archive inventory differs")
	}
	return nil
}

func validateSameCommonOwnerRejoinHeader(p OwnerRejoinPlan) error {
	if p.Files != nil {
		if err := validateOwnerRejoinPlan(p, p.PayloadSHA256 != ""); err != nil {
			return err
		}
	} else if p.SchemaVersion != 1 || !sharedHex32.MatchString(p.RejoinID) || !validSharedRoot(p.SourceOwner) || !validSharedRoot(p.TargetOwner) || p.SourceOwner == p.TargetOwner || !validSharedBoardPath(p.BoardPath) || !sharedHex40Or64.MatchString(p.SourceHEAD) || !sharedHex64.MatchString(p.SourceRefsSHA256) || !sharedHex64.MatchString(p.SourceInventorySHA256) || !sharedHex32.MatchString(p.SourceNamespace) || p.ReservationFloors == nil || p.AdditionalReservedIDs == nil || validateReservationFloors(p.ReservationFloors) != nil || !sortedUniqueIDs(p.AdditionalReservedIDs) {
		return errors.New("invalid same-common owner rejoin preparation header")
	}
	if !p.SourceCommonAvailable || p.SourceStorageProtocol != 5 || p.TargetStorageProtocol != 6 || p.SourceNamespace != p.TargetNamespace || p.SourcePolicyAuthority != p.TargetPolicyAuthority || p.SourcePolicySHA256 != p.TargetPolicySHA256 {
		return errors.New("invalid same-common owner rejoin header")
	}
	return nil
}

func deriveSameCommonOwnerRejoin(p OwnerRejoinPlan, sources map[string]OwnerRejoinSourceFile) (ownerRejoinDerived, error) {
	var out ownerRejoinDerived
	sourceBoard, targetBoard := p.SourceOwner, p.TargetOwner
	if err := validateOwnerRejoinBoards(sourceBoard, targetBoard); err != nil {
		return out, err
	}
	targets := map[string][]byte{}
	var err error
	if targets["transitions"], err = TransformOwnerRejoinTransitions(sources["transitions"].Raw); err != nil {
		return out, err
	}
	if targets["ids"], err = TransformOwnerRejoinIDs(sources["ids"].Raw, p.SourceNamespace, p.ReservationFloors, p.AdditionalReservedIDs); err != nil {
		return out, err
	}
	if targets["repairs"], err = TransformOwnerRejoinRepairJournal(sources["repairs"].Raw, sourceBoard, targetBoard); err != nil {
		return out, err
	}
	if targets["relocations"], err = TransformOwnerRejoinRelocationJournal(sources["relocations"].Raw, sourceBoard, targetBoard); err != nil {
		return out, err
	}
	if targets["archive"], err = TransformOwnerRejoinArchiveJournal(sources["archive"].Raw, sourceBoard, targetBoard); err != nil {
		return out, err
	}
	targets["archive-capacity"], out.capacityPayload, out.artifact, err = prepareOwnerRejoinArchiveCapacity(sources["archive-capacity"].Raw, sources["archive"].Raw, sourceBoard, targetBoard, sources["archive"].Mode)
	if err != nil {
		return out, err
	}
	if err := validateSameCommonPolicy(p, sources, targets, sourceBoard); err != nil {
		return out, err
	}
	roles := sameCommonOwnerRejoinRoles(p.SourcePolicyAuthority != "")
	for _, role := range roles {
		source, target := sources[role], targets[role]
		meta := OwnerRejoinFile{Role: role, Path: sameCommonOwnerRejoinPaths[role], OriginalPresent: true, TargetPresent: true, Mode: source.Mode, OriginalLength: len(source.Raw), OriginalSHA256: bytesDigest(source.Raw), TargetLength: len(target), TargetSHA256: bytesDigest(target)}
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

func validateSameCommonPolicy(p OwnerRejoinPlan, sources map[string]OwnerRejoinSourceFile, targets map[string][]byte, sourceBoard string) error {
	transition, _ := decodeTransitionJournal(sources["transitions"].Raw)
	if p.SourcePolicyAuthority == "" {
		if transition.SchemaVersion != 1 || transition.PolicyAuthority != nil || sources["policy"].Raw != nil || sources["policy-activation"].Raw != nil || validateTransitionRecords(transition, boardpolicy.Default()) != nil {
			return errors.New("policy-absent owner rejoin contains policy provenance")
		}
		return nil
	}
	policy, activation := sources["policy"].Raw, sources["policy-activation"].Raw
	parsed, policyErr := boardpolicy.Parse(policy)
	if policyErr != nil || bytesDigest(policy) != p.SourcePolicySHA256 || transition.PolicyDigest != p.SourcePolicySHA256 || transition.PolicyAuthority == nil || transition.PolicyAuthority.AuthorityID != p.SourcePolicyAuthority || transition.PolicyAuthority.Scope != "shared" || transition.PolicyAuthority.Namespace != p.SourceNamespace || validateTransitionRecords(transition, parsed) != nil {
		return errors.New("same-common policy authority differs")
	}
	var state policyActivationState
	if err := decodeExact(activation, &state); err != nil || state.Phase != "completed" || state.Scope != "shared" || state.AuthorityID != p.SourcePolicyAuthority || state.Namespace != p.SourceNamespace || state.Digest != p.SourcePolicySHA256 || state.Plan.Root != sourceBoard || !bytes.Equal(state.Canonical, policy) {
		return errors.New("same-common policy activation provenance invalid")
	}
	canonical, err := policyActivationBytes(state)
	if err != nil || !bytes.Equal(canonical, activation) {
		return errors.New("same-common policy activation receipt is not exact")
	}
	targets["policy"], targets["policy-activation"] = append([]byte(nil), policy...), append([]byte(nil), activation...)
	return nil
}

func sameArchiveCardBindings(a, b []ArchiveNamespaceCardBinding) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
