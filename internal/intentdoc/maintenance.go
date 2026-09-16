package intentdoc

import (
	"errors"
	"fmt"
)

// Maintenance declares bounded, caller-observed iteration policy. It is a
// data contract only: parsing it does not schedule work or enforce budgets.
type Maintenance struct {
	Triggers []MaintenanceTrigger `json:"triggers"`
	Budget   MaintenanceBudget    `json:"budget"`
}

type MaintenanceTrigger struct {
	Key             string  `json:"key"`
	Kind            string  `json:"kind"`
	IntervalSeconds *uint32 `json:"intervalSeconds,omitempty"`
}

type MaintenanceBudget struct {
	MaxIterations     uint32 `json:"maxIterations"`
	MaxTasks          uint32 `json:"maxTasks"`
	MaxElapsedSeconds uint32 `json:"maxElapsedSeconds"`
	NoProgressLimit   uint32 `json:"noProgressLimit"`
}

func validateMaintenance(m Maintenance) error {
	if len(m.Triggers) == 0 || len(m.Triggers) > 128 {
		return errors.New("maintenance triggers requires 1..128 entries")
	}
	seen := make(map[string]struct{}, len(m.Triggers))
	for i, trigger := range m.Triggers {
		if !criterionKey.MatchString(trigger.Key) {
			return fmt.Errorf("maintenance trigger %d has invalid key", i)
		}
		if _, exists := seen[trigger.Key]; exists {
			return fmt.Errorf("maintenance trigger %q is duplicated", trigger.Key)
		}
		seen[trigger.Key] = struct{}{}
		switch trigger.Kind {
		case "manual", "event":
			if trigger.IntervalSeconds != nil {
				return fmt.Errorf("maintenance trigger %q cannot define intervalSeconds", trigger.Key)
			}
		case "interval":
			if trigger.IntervalSeconds == nil || *trigger.IntervalSeconds == 0 {
				return fmt.Errorf("maintenance trigger %q requires a positive intervalSeconds", trigger.Key)
			}
		default:
			return fmt.Errorf("maintenance trigger %q has unsupported kind", trigger.Key)
		}
	}
	if m.Budget.MaxIterations == 0 || m.Budget.MaxTasks == 0 || m.Budget.MaxElapsedSeconds == 0 || m.Budget.NoProgressLimit == 0 {
		return errors.New("maintenance budget values must be positive")
	}
	return nil
}
