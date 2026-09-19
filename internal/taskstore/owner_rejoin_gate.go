package taskstore

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"unicode/utf8"
)

// errOwnerRejoinAbsent reports that no completed owner-rejoin evidence is
// present.  Callers that reach protocol 6 without it must fail closed.
var errOwnerRejoinAbsent = errors.New("protocol 6 requires a completed owner-rejoin receipt")

// ownerRejoinCompletedLocal proves from local immutable evidence alone that an
// owner-rejoin transaction reached its completed receipt on this board.  It
// never consults the common directory: the local receipt plus the
// content-addressed plan are the recovery authority, exactly as the
// transaction core treats them.
//
// The bounded plan artifact is reloaded and re-decoded in full, and the live
// transition journal must equal the plan's transition target byte for byte -
// that is what makes the protocol-6 marker on this board legitimate rather
// than a receipt someone dropped next to unrelated bytes.  The separately
// transported payload and capacity artifacts are only proven present here;
// their exact bytes are re-verified by the rejoin transaction itself and by
// the archive capacity gate, which already cross-binds them.
func ownerRejoinCompletedLocal(r *os.Root) (OwnerRejoinPlan, error) {
	var zero OwnerRejoinPlan
	raw, err := loadOwnerRejoinArtifact(r, ownerRejoinReceiptFile, 4096)
	if errors.Is(err, fs.ErrNotExist) {
		return zero, errOwnerRejoinAbsent
	}
	if err != nil {
		return zero, err
	}
	planDigest, err := ownerRejoinReceiptPlanDigest(raw)
	if err != nil {
		return zero, err
	}
	planName, err := ownerRejoinPlanArtifactName(planDigest)
	if err != nil {
		return zero, err
	}
	planRaw, err := loadOwnerRejoinArtifact(r, planName, 1<<20)
	if err != nil {
		return zero, errors.New("owner-rejoin receipt names a plan artifact that is missing or unreadable")
	}
	if bytesDigest(planRaw) != planDigest {
		return zero, errors.New("owner-rejoin plan artifact contradicts its digest name")
	}
	plan, err := DecodeOwnerRejoinPlan(planRaw)
	if err != nil {
		return zero, err
	}
	receipt, err := DecodeOwnerRejoinReceipt(raw, plan)
	if err != nil {
		return zero, err
	}
	if receipt.Phase != "completed" {
		return zero, errors.New("owner rejoin is still pending; resume it before using this board")
	}
	board, err := canonicalStorageBoard(r)
	if err != nil {
		return zero, err
	}
	if plan.TargetOwner != board || plan.TargetStorageProtocol != 6 {
		return zero, errors.New("owner-rejoin receipt belongs to another board")
	}
	if err := ownerRejoinTransitionTargetMatches(r, plan); err != nil {
		return zero, err
	}
	payloadName, err := ownerRejoinPayloadArtifactName(plan.PayloadSHA256)
	if err != nil {
		return zero, err
	}
	if err := ownerRejoinArtifactPresent(r, payloadName); err != nil {
		return zero, err
	}
	for _, artifact := range plan.Artifacts {
		if err := ownerRejoinArtifactPresent(r, artifact.Path); err != nil {
			return zero, err
		}
	}
	return plan, nil
}

// ownerRejoinReceiptPlanDigest extracts only the plan digest that names the
// retained plan artifact.  The full receipt is re-decoded against that plan
// afterwards, so this deliberately proves nothing else.
func ownerRejoinReceiptPlanDigest(raw []byte) (string, error) {
	if len(raw) == 0 || len(raw) > 4096 || !utf8.Valid(raw) {
		return "", errors.New("owner rejoin receipt size or UTF-8 invalid")
	}
	if err := rejectDuplicateJSON(raw); err != nil {
		return "", err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var receipt OwnerRejoinReceipt
	if err := dec.Decode(&receipt); err != nil {
		return "", err
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return "", errors.New("owner rejoin receipt trailing content")
	}
	if !sharedHex64.MatchString(receipt.PlanSHA256) {
		return "", errors.New("owner rejoin receipt plan digest invalid")
	}
	return receipt.PlanSHA256, nil
}

func ownerRejoinTransitionTargetMatches(r *os.Root, p OwnerRejoinPlan) error {
	for _, meta := range p.Files {
		if meta.Path != transitionsFile {
			continue
		}
		if !meta.TargetPresent {
			return errors.New("owner-rejoin plan does not publish a transition journal")
		}
		info, err := r.Lstat(transitionsFile)
		if err != nil || !info.Mode().IsRegular() || uint32(info.Mode().Perm()) != meta.Mode {
			return errors.New("protocol 6 transition journal type or mode contradicts its owner-rejoin plan")
		}
		raw, err := boundedSnapshotFile(r, transitionsFile, maxTransitionBytes)
		if err != nil {
			return err
		}
		if len(raw) != meta.TargetLength || bytesDigest(raw) != meta.TargetSHA256 {
			return errors.New("protocol 6 transition journal is not the completed owner-rejoin target")
		}
		return nil
	}
	return errors.New("owner-rejoin plan lacks a transition journal binding")
}

func ownerRejoinArtifactPresent(r *os.Root, name string) error {
	info, err := r.Lstat(name)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 || info.Size() <= 0 {
		return errors.New("completed owner rejoin is missing a retained artifact; restore it, never reinitialize")
	}
	return nil
}

// ownerRejoinAdmitsProtocol6 is the single admission point every general
// runtime path uses.  Absent or pending evidence keeps the original
// fail-closed behaviour.
func ownerRejoinAdmitsProtocol6(r *os.Root) error {
	_, err := ownerRejoinCompletedLocal(r)
	if errors.Is(err, errOwnerRejoinAbsent) {
		return errors.New("protocol 6 requires a completed owner-rejoin receipt; none is present")
	}
	return err
}
