package taskstore

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"unicode/utf8"
)

// OwnerRejoinPlan is a pure, content-addressed preparation record.  It grants
// no filesystem or runtime authority; d2 is responsible for publication.
type OwnerRejoinPlan struct {
	SchemaVersion         int                           `json:"schemaVersion"`
	RejoinID              string                        `json:"rejoinId"`
	SourceOwner           string                        `json:"sourceOwner"`
	TargetOwner           string                        `json:"targetOwner"`
	BoardPath             string                        `json:"boardPath"`
	SourceHEAD            string                        `json:"sourceHead"`
	SourceRefsSHA256      string                        `json:"sourceRefsSha256"`
	SourceInventorySHA256 string                        `json:"sourceInventorySha256"`
	SourceNamespace       string                        `json:"sourceNamespace"`
	TargetNamespace       string                        `json:"targetNamespace"`
	SourcePolicyAuthority string                        `json:"sourcePolicyAuthority,omitempty"`
	SourcePolicySHA256    string                        `json:"sourcePolicySha256,omitempty"`
	TargetPolicyAuthority string                        `json:"targetPolicyAuthority,omitempty"`
	TargetPolicySHA256    string                        `json:"targetPolicySha256,omitempty"`
	SourceCommonAvailable bool                          `json:"sourceCommonAvailable"`
	SourceFencedNonempty  bool                          `json:"sourceFencedNonempty"`
	SourceStorageProtocol int                           `json:"sourceStorageProtocol"`
	TargetStorageProtocol int                           `json:"targetStorageProtocol"`
	ReservationFloors     []ReservationFloor            `json:"reservationFloors"`
	AdditionalReservedIDs []string                      `json:"additionalReservedIds"`
	Files                 []OwnerRejoinFile             `json:"files"`
	Artifacts             []OwnerRejoinArtifact         `json:"artifacts,omitempty"`
	ArchiveCards          []ArchiveNamespaceCardBinding `json:"archiveCards"`
	PayloadSHA256         string                        `json:"payloadSha256"`
}

type ReservationFloor struct {
	Prefix  string `json:"prefix"`
	Through uint64 `json:"through"`
}

type OwnerRejoinFile struct {
	Role            string `json:"role"`
	Path            string `json:"path"`
	OriginalPresent bool   `json:"originalPresent"`
	TargetPresent   bool   `json:"targetPresent"`
	Mode            uint32 `json:"mode"`
	OriginalLength  int    `json:"originalLength"`
	OriginalSHA256  string `json:"originalSha256"`
	TargetLength    int    `json:"targetLength"`
	TargetSHA256    string `json:"targetSha256"`
}

// OwnerRejoinArtifact inventories immutable content-addressed bytes that are
// transported separately from the bounded owner payload.
type OwnerRejoinArtifact struct {
	Role   string `json:"role"`
	Path   string `json:"path"`
	Mode   uint32 `json:"mode"`
	Length int    `json:"length"`
	SHA256 string `json:"sha256"`
}

type OwnerRejoinReceipt struct {
	SchemaVersion         int    `json:"schemaVersion"`
	Phase                 string `json:"phase"`
	RejoinID              string `json:"rejoinId"`
	PlanSHA256            string `json:"planSha256"`
	PayloadSHA256         string `json:"payloadSha256"`
	SourceAggregateSHA256 string `json:"sourceAggregateSha256"`
	TargetAggregateSHA256 string `json:"targetAggregateSha256"`
}

func validateReservationFloors(floors []ReservationFloor) error {
	for i, floor := range floors {
		if _, err := cardIDForFloor(floor); err != nil || (i > 0 && floors[i-1].Prefix >= floor.Prefix) {
			return errors.New("reservation floors must be sorted unique valid prefixes with positive bounds")
		}
	}
	return nil
}

func validateOwnerRejoinPlan(p OwnerRejoinPlan, requirePayload bool) error {
	if p.SchemaVersion != 1 || !sharedHex32.MatchString(p.RejoinID) || !validSharedRoot(p.SourceOwner) || !validSharedRoot(p.TargetOwner) || p.SourceOwner == p.TargetOwner || !validSharedBoardPath(p.BoardPath) || !sharedHex40Or64.MatchString(p.SourceHEAD) || !sharedHex64.MatchString(p.SourceRefsSHA256) || !sharedHex64.MatchString(p.SourceInventorySHA256) || !sharedHex32.MatchString(p.SourceNamespace) || !sharedHex32.MatchString(p.TargetNamespace) || !p.SourceFencedNonempty || p.SourceStorageProtocol < 1 || p.SourceStorageProtocol > 5 || p.TargetStorageProtocol != 6 {
		return errors.New("invalid owner rejoin plan header")
	}
	if (p.SourcePolicyAuthority == "") != (p.SourcePolicySHA256 == "") || (p.TargetPolicyAuthority == "") != (p.TargetPolicySHA256 == "") || (p.SourcePolicyAuthority != "" && (!sharedHex32.MatchString(p.SourcePolicyAuthority) || !sharedHex64.MatchString(p.SourcePolicySHA256))) || (p.TargetPolicyAuthority != "" && (!sharedHex32.MatchString(p.TargetPolicyAuthority) || !sharedHex64.MatchString(p.TargetPolicySHA256))) {
		return errors.New("invalid owner rejoin policy authority binding")
	}
	if (p.SourcePolicyAuthority == "") != (p.TargetPolicyAuthority == "") || p.SourcePolicySHA256 != p.TargetPolicySHA256 {
		return errors.New("owner rejoin must preserve policy presence and digest")
	}
	if p.ReservationFloors == nil || p.AdditionalReservedIDs == nil || p.Files == nil || p.ArchiveCards == nil {
		return errors.New("owner rejoin plan collections must be non-null")
	}
	if err := validateReservationFloors(p.ReservationFloors); err != nil {
		return err
	}
	if !sortedUniqueIDs(p.AdditionalReservedIDs) {
		return errors.New("additional reserved IDs must be sorted unique canonical IDs")
	}
	if p.SourceCommonAvailable {
		if p.SourceNamespace != p.TargetNamespace || p.SourcePolicyAuthority != p.TargetPolicyAuthority || p.SourcePolicySHA256 != p.TargetPolicySHA256 {
			return errors.New("same-common owner rejoin must preserve namespace and policy authority")
		}
	} else {
		if p.SourceNamespace == p.TargetNamespace || (p.SourcePolicyAuthority != "" && p.SourcePolicyAuthority == p.TargetPolicyAuthority) || (len(p.ReservationFloors) == 0 && len(p.AdditionalReservedIDs) == 0) {
			return errors.New("independent clone rejoin requires new authority and reservation evidence")
		}
	}
	seenRole, seenPath := map[string]bool{}, map[string]bool{}
	for i, f := range p.Files {
		if f.Role == "" || len(f.Role) > 255 || seenRole[f.Role] || seenPath[f.Path] || !validRejoinPath(f.Path) || f.Mode == 0 || f.Mode&^0777 != 0 || f.OriginalLength < 0 || f.TargetLength < 0 || f.OriginalLength > maxArchiveCapacityBytes || f.TargetLength > maxArchiveCapacityBytes || (!f.OriginalPresent && (f.OriginalLength != 0 || f.OriginalSHA256 != "")) || (!f.TargetPresent && (f.TargetLength != 0 || f.TargetSHA256 != "")) || (f.OriginalPresent && !sharedHex64.MatchString(f.OriginalSHA256)) || (f.TargetPresent && !sharedHex64.MatchString(f.TargetSHA256)) || (i > 0 && p.Files[i-1].Role >= f.Role) {
			return errors.New("invalid owner rejoin file inventory")
		}
		seenRole[f.Role], seenPath[f.Path] = true, true
	}
	for i, a := range p.Artifacts {
		if a.Role == "" || len(a.Role) > 255 || seenRole[a.Role] || seenPath[a.Path] || !validRejoinPath(a.Path) || a.Mode == 0 || a.Mode&^0777 != 0 || a.Length <= 0 || a.Length > maxOwnerRejoinCapacityArtifactBytes || !sharedHex64.MatchString(a.SHA256) || (i > 0 && p.Artifacts[i-1].Role >= a.Role) {
			return errors.New("invalid owner rejoin artifact inventory")
		}
		seenRole[a.Role], seenPath[a.Path] = true, true
	}
	if len(p.Files) == 0 || len(p.Files) > 64 || !sortedArchiveCards(p.ArchiveCards) || (requirePayload && !sharedHex64.MatchString(p.PayloadSHA256)) || (!requirePayload && p.PayloadSHA256 != "" && !sharedHex64.MatchString(p.PayloadSHA256)) {
		return errors.New("invalid owner rejoin plan inventory or payload")
	}
	return nil
}

func sortedUniqueIDs(ids []string) bool {
	for i, id := range ids {
		if identityKey(id) != id || id == "" || (i > 0 && ids[i-1] >= id) {
			return false
		}
	}
	return true
}
func sortedArchiveCards(cards []ArchiveNamespaceCardBinding) bool {
	for i, c := range cards {
		if !validRejoinPath(c.Path) || identityKey(c.ID) != c.ID || !sharedHex64.MatchString(c.SHA256) || c.Mode == 0 || c.Mode&^0777 != 0 || (i > 0 && cards[i-1].Path >= c.Path) {
			return false
		}
	}
	return true
}
func validRejoinPath(p string) bool { return validSharedBoardPath(p) && p != ".task-manager-lock" }

func OwnerRejoinPlanBytes(p OwnerRejoinPlan) ([]byte, error) {
	if err := validateOwnerRejoinPlan(p, true); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(p)
	if err != nil {
		return nil, err
	}
	raw = append(raw, '\n')
	if len(raw) > 1<<20 {
		return nil, errors.New("owner rejoin plan exceeds bound")
	}
	return raw, nil
}

func DecodeOwnerRejoinPlan(raw []byte) (OwnerRejoinPlan, error) {
	var p OwnerRejoinPlan
	if len(raw) == 0 || len(raw) > 1<<20 || !utf8.Valid(raw) {
		return p, errors.New("owner rejoin plan size or UTF-8 invalid")
	}
	if err := rejectDuplicateJSON(raw); err != nil {
		return p, err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return p, err
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return p, errors.New("owner rejoin plan trailing content")
	}
	if err := validateOwnerRejoinPlan(p, true); err != nil {
		return p, err
	}
	canonical, err := OwnerRejoinPlanBytes(p)
	if err != nil || !bytes.Equal(raw, canonical) {
		return p, errors.New("owner rejoin plan is not canonical")
	}
	return p, nil
}

func OwnerRejoinReceiptBytes(r OwnerRejoinReceipt) ([]byte, error) {
	if err := validateOwnerRejoinReceipt(r); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(r)
	return append(raw, '\n'), err
}
func validateOwnerRejoinReceipt(r OwnerRejoinReceipt) error {
	if r.SchemaVersion != 1 || (r.Phase != "pending" && r.Phase != "completed") || !sharedHex32.MatchString(r.RejoinID) || !sharedHex64.MatchString(r.PlanSHA256) || !sharedHex64.MatchString(r.PayloadSHA256) || !sharedHex64.MatchString(r.SourceAggregateSHA256) || !sharedHex64.MatchString(r.TargetAggregateSHA256) {
		return errors.New("invalid owner rejoin receipt")
	}
	return nil
}

func DecodeOwnerRejoinReceipt(raw []byte, p OwnerRejoinPlan) (OwnerRejoinReceipt, error) {
	var r OwnerRejoinReceipt
	if len(raw) == 0 || len(raw) > 4096 || !utf8.Valid(raw) {
		return r, errors.New("owner rejoin receipt size or UTF-8 invalid")
	}
	if err := rejectDuplicateJSON(raw); err != nil {
		return r, err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&r); err != nil {
		return r, err
	}
	var x any
	if err := dec.Decode(&x); !errors.Is(err, io.EOF) {
		return r, errors.New("owner rejoin receipt trailing content")
	}
	canonical, canonicalErr := OwnerRejoinReceiptBytes(r)
	planRaw, err := OwnerRejoinPlanBytes(p)
	if err != nil || canonicalErr != nil || !bytes.Equal(raw, canonical) || r.RejoinID != p.RejoinID || r.PlanSHA256 != bytesDigest(planRaw) || r.PayloadSHA256 != p.PayloadSHA256 || r.SourceAggregateSHA256 != ownerRejoinAggregate(p.Files, false) || r.TargetAggregateSHA256 != ownerRejoinAggregate(p.Files, true) {
		return r, errors.New("owner rejoin receipt plan binding mismatch")
	}
	return r, nil
}

func ownerRejoinAggregate(files []OwnerRejoinFile, target bool) string {
	h := make([]string, 0, len(files))
	for _, f := range files {
		if target {
			h = append(h, f.Role+":"+f.TargetSHA256)
		} else {
			h = append(h, f.Role+":"+f.OriginalSHA256)
		}
	}
	sort.Strings(h)
	return bytesDigest([]byte(fmt.Sprint(h)))
}
