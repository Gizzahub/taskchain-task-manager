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
	ledger, err := decodeIDs(raw)
	if err != nil || ledger.SchemaVersion != 3 || ledger.Namespace != namespace || !sharedHex32.MatchString(namespace) {
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
	ledger.SchemaVersion = 4
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
	if err := validateOwnerRejoinBoards(sourceBoard, targetBoard); err != nil {
		return nil, err
	}
	j, err := decodeArchiveCapacityJournal(raw)
	if err != nil || j.BoardPath != sourceBoard {
		return nil, errors.New("owner rejoin archive journal scope invalid")
	}
	canonical, err := archiveJournalWire(j)
	if err != nil || !bytes.Equal(raw, canonical) {
		return nil, errors.New("owner rejoin archive journal is not exact")
	}
	target := j
	target.BoardPath = targetBoard
	target.Records = append([]archiveRecord{}, j.Records...)
	for i := range target.Records {
		r := &target.Records[i]
		if r.State != "completed" || r.BoardPath != sourceBoard || r.Namespace != j.Namespace {
			return nil, errors.New("owner rejoin archive journal has pending or foreign record")
		}
		r.BoardPath = targetBoard
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
	want, err := TransformOwnerRejoinArchiveJournal(original, sourceBoard, targetBoard)
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
