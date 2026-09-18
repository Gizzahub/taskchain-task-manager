package taskstore

import (
	"errors"
	"fmt"
	"io/fs"
	"sort"
)

// applyOwnerRejoinSameCommon is intentionally package-private. Preparation
// and independent-clone authority are outside this transaction core.
func applyOwnerRejoinSameCommon(dir string, plan OwnerRejoinPlan, payload []byte, step func(string) error) (err error) {
	if err := validateOwnerRejoinPlan(plan, true); err != nil || !plan.SourceCommonAvailable {
		return errors.New("same-common owner rejoin requires a strict same-common plan")
	}
	files, err := DecodeOwnerRejoinPayload(payload, plan)
	if err != nil {
		return err
	}
	if err := validateSameCommonOwnerRejoinPlan(plan, files); err != nil {
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
	r, err := openBoard(dir)
	if err != nil {
		return err
	}
	unlock, err := lock(r)
	if err != nil {
		return errors.Join(err, r.Close())
	}
	defer func() { err = errors.Join(err, unlock(), r.Close()) }()

	evidence, err := inspectOwnerRejoinEvidence(shared, plan)
	if err != nil {
		return err
	}
	if err := preflightOwnerRejoin(shared, r, plan, files, evidence, planDigest); err != nil {
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
	if err := ensureOwnerRejoinCommonPending(shared, plan, planDigest, files, evidence); err != nil {
		return err
	}
	if err := storageStep(step, "after-owner-rejoin-common-pending"); err != nil {
		return err
	}
	if err := publishOwnerRejoinTargets(r, plan, files, step); err != nil {
		return err
	}
	if err := fullRevalidateOwnerRejoin(shared, r, plan, files, evidence, pending); err != nil {
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

func ownerRejoinReceipt(p OwnerRejoinPlan, digest, phase string) OwnerRejoinReceipt {
	return OwnerRejoinReceipt{
		SchemaVersion: 1, Phase: phase, RejoinID: p.RejoinID,
		PlanSHA256: digest, PayloadSHA256: p.PayloadSHA256,
		SourceAggregateSHA256: ownerRejoinAggregate(p.Files, false),
		TargetAggregateSHA256: ownerRejoinAggregate(p.Files, true),
	}
}

func ownerRejoinCommonCompleted(state *sharedState, p OwnerRejoinPlan, digest string) bool {
	if state == nil || state.Phase != "active" || state.StorageProtocol != 6 || state.PendingOwnerRejoin != nil {
		return false
	}
	for _, done := range state.CompletedOwnerRejoins {
		if done.RejoinID == p.RejoinID && done.Owner == p.TargetOwner && done.PlanSHA256 == digest && done.PayloadSHA256 == p.PayloadSHA256 {
			return true
		}
	}
	return false
}

func ownerRejoinPublicationOrder(p OwnerRejoinPlan) ([]int, error) {
	order := make([]int, len(p.Files))
	for i, file := range p.Files {
		if _, err := ownerRejoinFileClass(file); err != nil {
			return nil, err
		}
		order[i] = i
	}
	sort.Slice(order, func(i, j int) bool {
		left, _ := ownerRejoinFileClass(p.Files[order[i]])
		right, _ := ownerRejoinFileClass(p.Files[order[j]])
		if left != right {
			return left < right
		}
		return p.Files[order[i]].Role < p.Files[order[j]].Role
	})
	return order, nil
}

func ownerRejoinFileClass(file OwnerRejoinFile) (int, error) {
	switch file.Role {
	case "transitions":
		return 0, nil
	case "ids":
		return 1, nil
	case "repairs":
		return 2, nil
	case "relocations":
		return 3, nil
	case "archive":
		return 4, nil
	case "archive-capacity":
		return 5, nil
	case "policy", "policy-activation":
		return 6, nil
	default:
		return 0, fmt.Errorf("owner rejoin file role %q has no fixed publication class", file.Role)
	}
}

func ownerRejoinClassName(class int) string {
	return []string{"transition", "ids", "repair", "relocation", "archive", "capacity", "policy"}[class]
}
