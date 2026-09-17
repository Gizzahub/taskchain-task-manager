package taskstore

import "testing"

func TestModuleAdoptionRequiresRevisionForPreviousPolicy(t *testing.T) {
	for _, original := range []string{"", "target", "previous"} {
		t.Run(original, func(t *testing.T) {
			state := policyActivationState{
				SchemaVersion: 3,
				Digest:        "target",
				Plan:          policyActivationPlan{OriginalPolicy: original},
			}
			err := validateActivationRevision(state)
			if (err != nil) != (original == "previous") {
				t.Fatalf("original=%q: %v", original, err)
			}
		})
	}
}
