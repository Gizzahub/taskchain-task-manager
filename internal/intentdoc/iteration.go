package intentdoc

import (
	"encoding/json"
	"errors"
	"fmt"
)

type ContextRef struct {
	ID       string `json:"id"`
	Revision uint32 `json:"revision"`
	Digest   string `json:"digest"`
}

type IterationTrigger struct {
	Key          string   `json:"key"`
	EvidenceRefs []string `json:"evidenceRefs"`
}

type IterationUsage struct {
	Tasks           uint32 `json:"tasks"`
	ElapsedSeconds  uint32 `json:"elapsedSeconds"`
	NoProgressCount uint32 `json:"noProgressCount"`
}

type CriterionEvaluation struct {
	Key          string   `json:"key"`
	Result       string   `json:"result"`
	EvidenceRefs []string `json:"evidenceRefs"`
}

type IterationEvaluation struct {
	Actor         string                `json:"actor"`
	Progress      bool                  `json:"progress"`
	Criteria      []CriterionEvaluation `json:"criteria"`
	Decision      string                `json:"decision"`
	StopReason    string                `json:"stopReason"`
	Reason        string                `json:"reason"`
	RemainingGaps []string              `json:"remainingGaps"`
	EvidenceRefs  []string              `json:"evidenceRefs"`
}

type Iteration struct {
	SchemaVersion uint32              `json:"schemaVersion"`
	Kind          string              `json:"kind"`
	ID            string              `json:"id"`
	Revision      uint32              `json:"revision"`
	Intent        IntentRef           `json:"intent"`
	Ordinal       uint32              `json:"ordinal"`
	Previous      *ContextRef         `json:"previous,omitempty"`
	Batch         *ContextRef         `json:"batch,omitempty"`
	Trigger       IterationTrigger    `json:"trigger"`
	Usage         IterationUsage      `json:"usage"`
	Evaluation    IterationEvaluation `json:"evaluation"`
}

func (d Document) IterationSnapshot() (Iteration, error) {
	var out Iteration
	if d.kind != "iteration" || len(d.canonical) == 0 {
		return out, errors.New("document is not an iteration")
	}
	err := json.Unmarshal(d.canonical, &out)
	return out, err
}

func (d Document) IntentSnapshot() (Intent, error) {
	var out Intent
	if d.kind != "intent" || len(d.canonical) == 0 {
		return out, errors.New("document is not an intent")
	}
	err := json.Unmarshal(d.canonical, &out)
	return out, err
}

func validateContextRef(ref ContextRef, kind string) error {
	if ValidateIdentity(kind, ref.ID, ref.Revision) != nil || !digestID.MatchString(ref.Digest) {
		return fmt.Errorf("invalid exact %s reference", kind)
	}
	return nil
}

func validateIteration(d Iteration) error {
	if d.SchemaVersion != 2 || d.Kind != "iteration" || ValidateIdentity(d.Kind, d.ID, d.Revision) != nil {
		return errors.New("invalid iteration schema, kind, ID or revision")
	}
	if err := validateRef(d.Intent); err != nil {
		return err
	}
	if d.Ordinal == 0 || (d.Previous == nil && d.Ordinal != 1) || (d.Previous != nil && d.Ordinal == 1) {
		return errors.New("iteration ordinal must start at one or follow a predecessor")
	}
	if d.Previous != nil {
		if err := validateContextRef(*d.Previous, "iteration"); err != nil {
			return err
		}
		if d.Previous.ID == d.ID {
			return errors.New("iteration cannot reference itself")
		}
	}
	if d.Batch != nil {
		if err := validateContextRef(*d.Batch, "batch"); err != nil {
			return err
		}
	}
	if !criterionKey.MatchString(d.Trigger.Key) || len(d.Trigger.EvidenceRefs) == 0 {
		return errors.New("trigger requires a key and evidence references")
	}
	if err := texts(d.Trigger.EvidenceRefs); err != nil {
		return err
	}
	if err := validateIterationEvaluation(d.Evaluation); err != nil {
		return err
	}
	if d.Evaluation.Decision == "idle" && (d.Batch != nil || d.Evaluation.Progress) {
		return errors.New("idle iteration has no batch or progress assertion")
	}
	if d.Evaluation.Progress && d.Usage.NoProgressCount != 0 {
		return errors.New("progress resets noProgressCount to zero")
	}
	if d.Previous == nil {
		want := uint32(1)
		if d.Evaluation.Progress || d.Evaluation.Decision == "idle" {
			want = 0
		}
		if d.Usage.NoProgressCount != want {
			return errors.New("invalid initial noProgressCount")
		}
	}
	return nil
}

func validateIterationEvaluation(e IterationEvaluation) error {
	if err := text(e.Actor, 128, false); err != nil {
		return fmt.Errorf("actor: %w", err)
	}
	if err := text(e.Reason, 16<<10, true); err != nil {
		return fmt.Errorf("reason: %w", err)
	}
	if err := texts(e.RemainingGaps); err != nil {
		return err
	}
	if err := texts(e.EvidenceRefs); err != nil {
		return err
	}
	if len(e.Criteria) == 0 || len(e.Criteria) > 128 {
		return errors.New("criteria requires 1..128 observations")
	}
	seen := map[string]bool{}
	for _, c := range e.Criteria {
		if !criterionKey.MatchString(c.Key) || seen[c.Key] {
			return errors.New("invalid or duplicate criterion observation key")
		}
		seen[c.Key] = true
		if err := texts(c.EvidenceRefs); err != nil {
			return err
		}
		switch c.Result {
		case "met", "unmet":
			if len(c.EvidenceRefs) == 0 {
				return errors.New("met/unmet assertion requires evidence")
			}
		case "unknown":
		default:
			return errors.New("unsupported criterion observation result")
		}
	}
	valid := false
	switch e.Decision {
	case "continue", "idle":
		valid = e.StopReason == "none"
	case "blocked":
		valid = e.StopReason == "permission-required" || e.StopReason == "user-decision"
	case "revise":
		valid = e.StopReason == "revision-change"
	case "stopped":
		valid = e.StopReason == "budget-exhausted" || e.StopReason == "no-progress" || e.StopReason == "user-stop"
	}
	if !valid {
		return errors.New("unsupported decision/stopReason combination")
	}
	return nil
}
