package taskruntime

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Purpose records why an execution was created.  The zero value is retained
// for ledgers written before purpose existed and means delivery.
type Purpose string

const (
	PurposeDelivery   Purpose = "delivery"
	PurposeExperiment Purpose = "experiment"
	PurposeReview     Purpose = "review"
)

// EffectivePurpose maps the legacy omitted value to delivery.
func EffectivePurpose(purpose Purpose) Purpose {
	if purpose == "" {
		return PurposeDelivery
	}
	return purpose
}

// ParsePurpose accepts only the purposes the runtime can record.
func ParsePurpose(value string) (Purpose, error) {
	purpose := Purpose(value)
	switch purpose {
	case PurposeDelivery, PurposeExperiment, PurposeReview:
		return purpose, nil
	default:
		return "", fmt.Errorf("purpose must be delivery, experiment, or review")
	}
}

// ValidatePurposeBranch ensures that the durable branch name carries the
// same purpose as its execution or receipt.
func ValidatePurposeBranch(purpose Purpose, branch string) error {
	effective := EffectivePurpose(purpose)
	prefix := ""
	switch effective {
	case PurposeDelivery:
		prefix = "dev/"
	case PurposeExperiment:
		prefix = "exp/"
	case PurposeReview:
		prefix = "review/"
	default:
		return fmt.Errorf("purpose must be delivery, experiment, or review")
	}
	if !strings.HasPrefix(branch, prefix) {
		return fmt.Errorf("branch %q does not match purpose %q", branch, effective)
	}
	return nil
}

func branchForPurpose(purpose Purpose, owner Owner, kind, task string) string {
	prefix := "dev"
	switch EffectivePurpose(purpose) {
	case PurposeExperiment:
		prefix = "exp"
	case PurposeReview:
		prefix = "review"
	}
	return fmt.Sprintf("%s/%s/%s/%s/%s", prefix, owner.Actor, owner.Host, kind, task)
}

func decodePurpose(raw map[string]json.RawMessage) (Purpose, error) {
	value, found := raw["purpose"]
	if !found {
		return PurposeDelivery, nil
	}
	var text string
	if err := json.Unmarshal(value, &text); err != nil {
		return "", fmt.Errorf("purpose must be a string")
	}
	return ParsePurpose(text)
}

func (execution *Execution) UnmarshalJSON(data []byte) error {
	type plain Execution
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	var decoded plain
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	purpose, err := decodePurpose(raw)
	if err != nil {
		return err
	}
	decoded.Purpose = purpose
	*execution = Execution(decoded)
	return nil
}

func (receipt *Receipt) UnmarshalJSON(data []byte) error {
	type plain Receipt
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	var decoded plain
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	purpose, err := decodePurpose(raw)
	if err != nil {
		return err
	}
	decoded.Purpose = purpose
	*receipt = Receipt(decoded)
	return nil
}
