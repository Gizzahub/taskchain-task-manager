package intentdoc

import (
	"encoding/json"
	"errors"
	"math"
)

func matchContextReference(doc Document, kind string, ref ContextRef) error {
	digest, err := doc.Digest()
	if err != nil {
		return err
	}
	if doc.Kind() != kind || doc.ID() != ref.ID || doc.Revision() != ref.Revision || digest != ref.Digest {
		return errors.New("referenced context kind, identity or digest mismatch")
	}
	return nil
}

// ValidateBatchIntentReference checks the exact relationship, not task
// existence, actor authority, evidence truth or the batch's other fields.
func ValidateBatchIntentReference(batch Batch, intent Document) error {
	if err := matchContextReference(intent, "intent", ContextRef(batch.Intent)); err != nil {
		return err
	}
	d, err := intent.IntentSnapshot()
	if err != nil {
		return err
	}
	if d.Mode == "maintenance" && batch.Evaluation != nil && batch.Evaluation.Decision == "achieved" {
		return errors.New("maintenance intent cannot be declared achieved by a batch")
	}
	return nil
}

// ValidateIterationReferences checks supplied immutable documents. Storage
// resolves exact keys under its lock; no latest selection or execution occurs.
func ValidateIterationReferences(record, intent Document, previous, batch *Document) error {
	d, err := record.IterationSnapshot()
	if err != nil {
		return err
	}
	if err := matchContextReference(intent, "intent", ContextRef(d.Intent)); err != nil {
		return err
	}
	i, err := intent.IntentSnapshot()
	if err != nil {
		return err
	}
	if i.SchemaVersion != 2 || i.Mode != "maintenance" || i.Maintenance == nil {
		return errors.New("iteration requires a maintenance intent")
	}
	triggerFound := false
	for _, trigger := range i.Maintenance.Triggers {
		triggerFound = triggerFound || trigger.Key == d.Trigger.Key
	}
	if !triggerFound {
		return errors.New("iteration trigger is not declared by intent")
	}
	keys := map[string]bool{}
	for _, c := range i.SuccessCriteria {
		keys[c.Key] = true
	}
	if len(d.Evaluation.Criteria) != len(keys) {
		return errors.New("iteration observations must cover every intent criterion")
	}
	for _, c := range d.Evaluation.Criteria {
		if !keys[c.Key] {
			return errors.New("iteration observation has unknown criterion key")
		}
	}
	if err := validatePredecessor(d, previous); err != nil {
		return err
	}
	if (d.Batch == nil) != (batch == nil) {
		return errors.New("iteration batch reference resolution mismatch")
	}
	if batch != nil {
		if err := matchContextReference(*batch, "batch", *d.Batch); err != nil {
			return err
		}
		var b Batch
		if err := json.Unmarshal(batch.canonical, &b); err != nil {
			return err
		}
		if b.Intent != d.Intent {
			return errors.New("iteration batch must reference the same intent revision")
		}
		if err := ValidateBatchIntentReference(b, intent); err != nil {
			return err
		}
	}
	return nil
}

func validatePredecessor(d Iteration, previous *Document) error {
	if (d.Previous == nil) != (previous == nil) {
		return errors.New("iteration predecessor resolution mismatch")
	}
	if previous == nil {
		return nil // Root constraints are already enforced by Parse.
	}
	if err := matchContextReference(*previous, "iteration", *d.Previous); err != nil {
		return err
	}
	p, err := previous.IterationSnapshot()
	if err != nil {
		return err
	}
	if p.Intent != d.Intent || p.Ordinal == math.MaxUint32 || d.Ordinal != p.Ordinal+1 {
		return errors.New("predecessor intent or ordinal mismatch/overflow")
	}
	if p.Evaluation.Decision == "stopped" || p.Evaluation.Decision == "revise" {
		return errors.New("cannot extend a terminal iteration lineage")
	}
	if d.Usage.Tasks < p.Usage.Tasks || d.Usage.ElapsedSeconds < p.Usage.ElapsedSeconds {
		return errors.New("cumulative observations cannot decrease")
	}
	want := p.Usage.NoProgressCount
	if d.Evaluation.Decision != "idle" {
		if d.Evaluation.Progress {
			want = 0
		} else {
			if want == math.MaxUint32 {
				return errors.New("noProgressCount overflow")
			}
			want++
		}
	}
	if d.Usage.NoProgressCount != want {
		return errors.New("noProgressCount does not match predecessor and observation")
	}
	return nil
}
