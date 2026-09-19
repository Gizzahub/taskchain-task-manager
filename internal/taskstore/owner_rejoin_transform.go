package taskstore

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"sort"
	"unicode/utf8"
)

// The owner-rejoin capacity frame is intentionally distinct from the archive
// capacity frame. It only carries schema-2 journals already prepared for an
// owner move and never upgrades or interprets the older capacity framing.
const (
	ownerRejoinCapacityPayloadMagic     = "TCORCA\x00\x02"
	ownerRejoinCapacityPayloadHeader    = len(ownerRejoinCapacityPayloadMagic) + 8 + 8 + 4
	maxOwnerRejoinCapacityArtifactBytes = ownerRejoinCapacityPayloadHeader + 2*maxArchiveCapacityBytes
)

func ownerRejoinCapacityArtifactPath(digest string) string {
	return ".task-manager-owner-rejoin-capacity-" + digest + ".bin"
}

func validateOwnerRejoinBoards(source, target string) error {
	if !canonicalBoardPath(source) || !canonicalBoardPath(target) || source == target {
		return errors.New("invalid owner rejoin board transformation")
	}
	return nil
}

// TransformOwnerRejoinTransitions advances only the storage barrier. All
// transaction, policy-authority and policy-history evidence is retained.
func TransformOwnerRejoinTransitions(raw []byte) ([]byte, error) {
	j, err := decodeTransitionJournal(raw)
	if err != nil || j.StorageProtocol != 5 {
		return nil, errors.New("owner rejoin transition source must use protocol 5")
	}
	canonical, err := marshalOwnerRejoinJSON(j, true)
	if err != nil || !bytes.Equal(raw, canonical) {
		return nil, errors.New("owner rejoin transition source is not exact")
	}
	for _, record := range j.Records {
		if record.Kind != "completed" {
			return nil, errors.New("owner rejoin transition source is pending")
		}
	}
	j.StorageProtocol = 6
	target, err := marshalOwnerRejoinJSON(j, true)
	if err != nil {
		return nil, err
	}
	got, err := decodeTransitionJournal(target)
	if err != nil || got.StorageProtocol != 6 {
		return nil, errors.New("owner rejoin transition target invalid")
	}
	return target, nil
}

// TransformOwnerRejoinIDs performs the sole schema change: 3 to 4. Existing
// reservations and namespace are immutable; floors and IDs are monotonic.
func TransformOwnerRejoinIDs(raw []byte, namespace string, floors []ReservationFloor, additional []string) ([]byte, error) {
	return transformOwnerRejoinIDs(raw, namespace, namespace, floors, additional)
}

// transformOwnerRejoinIDs additionally moves the ledger into a new namespace.
// An independent clone must mint its own namespace rather than reuse the
// source's, so only the clone path passes a different target; every reserved ID
// and reservation floor still travels with the ledger, because the clone's
// freedom to allocate is exactly what those bound.
func transformOwnerRejoinIDs(raw []byte, sourceNamespace, targetNamespace string, floors []ReservationFloor, additional []string) ([]byte, error) {
	ledger, err := decodeIDs(raw)
	if err != nil || ledger.SchemaVersion != 3 || ledger.Namespace != sourceNamespace || !sharedHex32.MatchString(sourceNamespace) || !sharedHex32.MatchString(targetNamespace) {
		return nil, errors.New("owner rejoin ID source scope or schema invalid")
	}
	canonical, err := marshalOwnerRejoinJSON(ledger, false)
	if err != nil || !bytes.Equal(raw, canonical) {
		return nil, errors.New("owner rejoin ID source is not exact")
	}
	if !sortedUniqueIDs(additional) {
		return nil, errors.New("owner rejoin additional IDs invalid")
	}
	set := make(map[string]bool, len(ledger.Reserved)+len(additional))
	for _, id := range ledger.Reserved {
		set[id] = true
	}
	for _, id := range additional {
		set[id] = true
	}
	ledger.Reserved = append([]string{}, ledger.Reserved[:0]...)
	for id := range set {
		ledger.Reserved = append(ledger.Reserved, id)
	}
	sort.Strings(ledger.Reserved)
	ledger.ReservationFloors, err = unionReservationFloors(ledger.ReservationFloors, floors)
	if err != nil {
		return nil, errors.New("owner rejoin reservation floors invalid")
	}
	if ledger.ReservationFloors == nil {
		ledger.ReservationFloors = []ReservationFloor{}
	}
	ledger.SchemaVersion, ledger.Namespace = 4, targetNamespace
	return idLedgerBytes(ledger)
}

func marshalOwnerRejoinJSON(value any, indent bool) ([]byte, error) {
	var raw []byte
	var err error
	if indent {
		raw, err = json.MarshalIndent(value, "", "  ")
	} else {
		raw, err = json.Marshal(value)
	}
	return append(raw, '\n'), err
}

// TransformOwnerRejoinRepairJournal rewrites only the completed journal's
// board binding. Namespace and every record datum remain source evidence.
func TransformOwnerRejoinRepairJournal(raw []byte, sourceBoard, targetBoard string) ([]byte, error) {
	if err := validateOwnerRejoinBoards(sourceBoard, targetBoard); err != nil {
		return nil, err
	}
	j, err := decodeRepairJournal(raw)
	if err != nil || j.BoardPath != sourceBoard {
		return nil, errors.New("owner rejoin repair journal scope invalid")
	}
	canonical, err := repairJournalBytes(j)
	if err != nil || !bytes.Equal(raw, canonical) {
		return nil, errors.New("owner rejoin repair journal is not exact")
	}
	target := j
	target.BoardPath = targetBoard
	target.Records = append([]repairRecord{}, j.Records...)
	for i := range target.Records {
		if target.Records[i].Kind != "completed" || target.Records[i].BoardPath != sourceBoard {
			return nil, errors.New("owner rejoin repair journal has pending or foreign record")
		}
		target.Records[i].BoardPath = targetBoard
	}
	return repairJournalBytes(target)
}

// TransformOwnerRejoinRelocationJournal rewrites only completed records.
func TransformOwnerRejoinRelocationJournal(raw []byte, sourceBoard, targetBoard string) ([]byte, error) {
	if err := validateOwnerRejoinBoards(sourceBoard, targetBoard); err != nil {
		return nil, err
	}
	j, err := decodeRelocationJournal(raw)
	if err != nil || j.BoardPath != sourceBoard {
		return nil, errors.New("owner rejoin relocation journal scope invalid")
	}
	canonical, err := relocationJournalBytes(j)
	if err != nil || !bytes.Equal(raw, canonical) {
		return nil, errors.New("owner rejoin relocation journal is not exact")
	}
	target := j
	target.BoardPath = targetBoard
	target.Records = append([]relocationRecord{}, j.Records...)
	for i := range target.Records {
		if target.Records[i].Kind != "completed" || target.Records[i].BoardPath != sourceBoard {
			return nil, errors.New("owner rejoin relocation journal has pending or foreign record")
		}
		target.Records[i].BoardPath = targetBoard
	}
	return relocationJournalBytes(target)
}

// TransformOwnerRejoinArchiveJournal preserves a schema-1 or schema-2
// archive journal and its namespace while changing its board binding.
func TransformOwnerRejoinArchiveJournal(raw []byte, sourceBoard, targetBoard string) ([]byte, error) {
	j, err := decodeArchiveCapacityJournal(raw)
	if err != nil {
		return nil, errors.New("owner rejoin archive journal scope invalid")
	}
	return transformOwnerRejoinArchiveJournal(raw, sourceBoard, targetBoard, j.Namespace, j.Namespace)
}

// transformOwnerRejoinArchiveJournal additionally moves the journal into a new
// namespace.  An independent clone must never reuse the source namespace, so
// the clone path rebinds it here; the same-common path passes the source
// namespace for both and so cannot change it by accident.
func transformOwnerRejoinArchiveJournal(raw []byte, sourceBoard, targetBoard, sourceNamespace, targetNamespace string) ([]byte, error) {
	if err := validateOwnerRejoinBoards(sourceBoard, targetBoard); err != nil {
		return nil, err
	}
	// A namespace-preserving move keeps whatever the source journal had,
	// including the empty namespace of a board that never enabled sharing.
	// A namespace change is only ever a clone bootstrap, which requires two
	// distinct real namespaces.
	if sourceNamespace != targetNamespace && (!sharedHex32.MatchString(sourceNamespace) || !sharedHex32.MatchString(targetNamespace)) {
		return nil, errors.New("owner rejoin archive journal namespace invalid")
	}
	j, err := decodeArchiveCapacityJournal(raw)
	if err != nil || j.BoardPath != sourceBoard || j.Namespace != sourceNamespace {
		return nil, errors.New("owner rejoin archive journal scope invalid")
	}
	canonical, err := archiveJournalWire(j)
	if err != nil || !bytes.Equal(raw, canonical) {
		return nil, errors.New("owner rejoin archive journal is not exact")
	}
	target := j
	target.BoardPath, target.Namespace = targetBoard, targetNamespace
	target.Records = append([]archiveRecord{}, j.Records...)
	for i := range target.Records {
		r := &target.Records[i]
		if r.State != "completed" || r.BoardPath != sourceBoard || r.Namespace != sourceNamespace {
			return nil, errors.New("owner rejoin archive journal has pending or foreign record")
		}
		r.BoardPath, r.Namespace = targetBoard, targetNamespace
		if r.Completion != nil {
			completion := *r.Completion
			completion.BoardPath = targetBoard
			completion.PolicyCanonical = append([]byte(nil), completion.PolicyCanonical...)
			completion.RulesCanonical = append([]byte(nil), completion.RulesCanonical...)
			r.Completion = &completion
		}
		r.PolicyCanonical = append([]byte(nil), r.PolicyCanonical...)
		r.RulesCanonical = append([]byte(nil), r.RulesCanonical...)
	}
	return archiveJournalWire(target)
}

func decodeOwnerRejoinArchiveCapacityAdoption(raw []byte) (archiveCapacityAdoption, error) {
	var a archiveCapacityAdoption
	if len(raw) == 0 || len(raw) > 4096 || !utf8.Valid(raw) {
		return a, errors.New("archive capacity receipt size or UTF-8 invalid")
	}
	if err := rejectDuplicateJSON(raw); err != nil {
		return a, err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&a); err != nil {
		return a, err
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return a, errors.New("archive capacity receipt trailing content")
	}
	want, err := archiveCapacityAdoptionBytes(a)
	if err != nil || !bytes.Equal(raw, want) {
		return a, errors.New("archive capacity receipt is not canonical")
	}
	return a, nil
}

func ownerRejoinCapacityPayloadBytes(original, target []byte, sourceBoard, targetBoard string, mode uint32) ([]byte, error) {
	if err := validateOwnerRejoinBoards(sourceBoard, targetBoard); err != nil {
		return nil, err
	}
	if mode == 0 || mode&^0o777 != 0 {
		return nil, errors.New("invalid owner rejoin capacity journal mode")
	}
	originalJournal, err := decodeArchiveCapacityJournal(original)
	if err != nil || originalJournal.SchemaVersion != 2 {
		return nil, errors.New("owner rejoin capacity original must be schema 2")
	}
	canonical, err := archiveCapacityJournalBytes(originalJournal)
	if err != nil || !bytes.Equal(original, canonical) {
		return nil, errors.New("owner rejoin capacity original is not exact")
	}
	// The namespace pair is read off the two journals rather than preserved,
	// because an independent clone rebinds the journal into its own namespace.
	// This still proves the target is the board transformation of the original
	// in every other field, and the transform itself refuses a namespace change
	// that is not between two real namespaces.  Which namespace the clone is
	// entitled to remains pinned by the capacity receipt and the rejoin plan.
	targetJournal, err := decodeArchiveCapacityJournal(target)
	if err != nil {
		return nil, errors.New("owner rejoin capacity target is not a journal")
	}
	want, err := transformOwnerRejoinArchiveJournal(original, sourceBoard, targetBoard, originalJournal.Namespace, targetJournal.Namespace)
	if err != nil || !bytes.Equal(want, target) {
		return nil, errors.New("owner rejoin capacity target differs from board transformation")
	}
	if len(original)+len(target) > 2*maxArchiveCapacityBytes {
		return nil, errors.New("owner rejoin capacity payload exceeds bound")
	}
	raw := make([]byte, ownerRejoinCapacityPayloadHeader, ownerRejoinCapacityPayloadHeader+len(original)+len(target))
	copy(raw, ownerRejoinCapacityPayloadMagic)
	binary.BigEndian.PutUint64(raw[8:16], uint64(len(original)))
	binary.BigEndian.PutUint64(raw[16:24], uint64(len(target)))
	binary.BigEndian.PutUint32(raw[24:28], mode)
	return append(append(raw, original...), target...), nil
}

func decodeOwnerRejoinCapacityPayload(raw []byte, digest, sourceBoard, targetBoard string) ([]byte, []byte, uint32, error) {
	if len(raw) < ownerRejoinCapacityPayloadHeader || len(raw) > ownerRejoinCapacityPayloadHeader+2*maxArchiveCapacityBytes || !sharedHex64.MatchString(digest) || bytesDigest(raw) != digest || string(raw[:8]) != ownerRejoinCapacityPayloadMagic {
		return nil, nil, 0, errors.New("owner rejoin capacity payload header or digest invalid")
	}
	o, t := binary.BigEndian.Uint64(raw[8:16]), binary.BigEndian.Uint64(raw[16:24])
	if o == 0 || t == 0 || o > maxArchiveCapacityBytes || t > maxArchiveCapacityBytes || o+t != uint64(len(raw)-ownerRejoinCapacityPayloadHeader) {
		return nil, nil, 0, errors.New("owner rejoin capacity payload framing invalid")
	}
	original := append([]byte(nil), raw[ownerRejoinCapacityPayloadHeader:ownerRejoinCapacityPayloadHeader+int(o)]...)
	target := append([]byte(nil), raw[ownerRejoinCapacityPayloadHeader+int(o):]...)
	mode := binary.BigEndian.Uint32(raw[24:28])
	if _, err := ownerRejoinCapacityPayloadBytes(original, target, sourceBoard, targetBoard, mode); err != nil {
		return nil, nil, 0, err
	}
	return original, target, mode, nil
}

// TransformOwnerRejoinPolicyActivation rebinds a completed policy activation
// receipt to the returning board.  Only Plan.Root moves: it is the board
// identity the ordinary runtime checks, and leaving it on the source board
// would make every rejoined policy-bearing board refuse itself.  The authority,
// scope, namespace, canonical policy and every historical digest in the plan
// are retained byte for byte, so the receipt still records the activation
// transaction that actually happened; the source root survives as provenance in
// the owner-rejoin plan, which records SourceOwner alongside TargetOwner.
func TransformOwnerRejoinPolicyActivation(raw []byte, sourceBoard, targetBoard string) ([]byte, error) {
	if err := validateOwnerRejoinBoards(sourceBoard, targetBoard); err != nil {
		return nil, err
	}
	var state policyActivationState
	if err := decodeExact(raw, &state); err != nil {
		return nil, err
	}
	canonical, err := policyActivationBytes(state)
	if err != nil || !bytes.Equal(raw, canonical) {
		return nil, errors.New("owner rejoin policy activation source is not exact")
	}
	if state.Phase != "completed" {
		return nil, errors.New("owner rejoin policy activation source is pending")
	}
	if state.Plan.Root != sourceBoard {
		return nil, errors.New("owner rejoin policy activation belongs to another board")
	}
	state.Plan.Root = targetBoard
	target, err := policyActivationBytes(state)
	if err != nil {
		return nil, err
	}
	var got policyActivationState
	if err := decodeExact(target, &got); err != nil || got.Plan.Root != targetBoard {
		return nil, errors.New("owner rejoin policy activation target invalid")
	}
	return target, nil
}

// transformOwnerRejoinCloneTransitions advances the storage barrier and, for a
// policy-bearing board, rebinds the journal to the clone's OWN policy
// authority.  The policy bytes and their digest are untouched: a clone adopts
// the same rules under an authority of its own, it does not inherit the
// source's authority.  A policy-absent board must pass two zero bindings and is
// then handled exactly as the same-common path handles it.
func transformOwnerRejoinCloneTransitions(raw []byte, source, target policyAuthorityBinding) ([]byte, error) {
	if (source == policyAuthorityBinding{}) != (target == policyAuthorityBinding{}) {
		return nil, errors.New("owner rejoin clone transitions policy presence differs")
	}
	if (source == policyAuthorityBinding{}) {
		return TransformOwnerRejoinTransitions(raw)
	}
	if err := validatePolicyAuthority(source); err != nil {
		return nil, err
	}
	if err := validatePolicyAuthority(target); err != nil {
		return nil, err
	}
	if source.AuthorityID == target.AuthorityID || source.Namespace == target.Namespace || source.Scope != target.Scope {
		return nil, errors.New("owner rejoin clone requires a new authority in a new namespace")
	}
	j, err := decodeTransitionJournal(raw)
	if err != nil || j.StorageProtocol != 5 {
		return nil, errors.New("owner rejoin transition source must use protocol 5")
	}
	canonical, err := marshalOwnerRejoinJSON(j, true)
	if err != nil || !bytes.Equal(raw, canonical) {
		return nil, errors.New("owner rejoin transition source is not exact")
	}
	for _, record := range j.Records {
		if record.Kind != "completed" {
			return nil, errors.New("owner rejoin transition source is pending")
		}
	}
	if j.PolicyAuthority == nil || *j.PolicyAuthority != source {
		return nil, errors.New("owner rejoin transition source binds another policy authority")
	}
	rebound := target
	j.StorageProtocol, j.PolicyAuthority = 6, &rebound
	out, err := marshalOwnerRejoinJSON(j, true)
	if err != nil {
		return nil, err
	}
	got, err := decodeTransitionJournal(out)
	if err != nil || got.StorageProtocol != 6 || got.PolicyAuthority == nil || *got.PolicyAuthority != target || got.PolicyDigest != j.PolicyDigest {
		return nil, errors.New("owner rejoin clone transition target invalid")
	}
	return out, nil
}

// transformOwnerRejoinClonePolicyActivation rebinds the activation receipt to
// the clone's board, authority and namespace.  The canonical policy and its
// digest are preserved, so the receipt keeps proving which rules were
// activated; what changes is who activated them and where, which is the whole
// point of an independent clone.
func transformOwnerRejoinClonePolicyActivation(raw []byte, sourceBoard, targetBoard string, source, target policyAuthorityBinding) ([]byte, error) {
	if err := validateOwnerRejoinBoards(sourceBoard, targetBoard); err != nil {
		return nil, err
	}
	if err := validatePolicyAuthority(source); err != nil {
		return nil, err
	}
	if err := validatePolicyAuthority(target); err != nil {
		return nil, err
	}
	if source.AuthorityID == target.AuthorityID || source.Namespace == target.Namespace || source.Scope != target.Scope {
		return nil, errors.New("owner rejoin clone requires a new authority in a new namespace")
	}
	var state policyActivationState
	if err := decodeExact(raw, &state); err != nil {
		return nil, err
	}
	exact, err := policyActivationBytes(state)
	if err != nil || !bytes.Equal(raw, exact) {
		return nil, errors.New("owner rejoin policy activation source is not exact")
	}
	if state.Phase != "completed" || state.Plan.Root != sourceBoard || state.AuthorityID != source.AuthorityID || state.Scope != source.Scope || state.Namespace != source.Namespace {
		return nil, errors.New("owner rejoin clone policy activation provenance invalid")
	}
	if state.Revision != nil {
		return nil, errors.New("clone rejoin cannot rebind a revised policy authority; revise after the clone owns its own authority")
	}
	state.Plan.Root, state.AuthorityID, state.Namespace = targetBoard, target.AuthorityID, target.Namespace
	out, err := policyActivationBytes(state)
	if err != nil {
		return nil, err
	}
	var got policyActivationState
	if err := decodeExact(out, &got); err != nil || got.Plan.Root != targetBoard || got.AuthorityID != target.AuthorityID || got.Namespace != target.Namespace || got.Digest != state.Digest {
		return nil, errors.New("owner rejoin clone policy activation target invalid")
	}
	return out, nil
}
