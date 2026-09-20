package taskstore

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestArchiveActivationBindingRequiresProtocol5CapacityAndExactArtifactRule(t *testing.T) {
	t.Parallel()
	base := &archiveActivationBinding{
		SchemaVersion:         1,
		StorageProtocol:       5,
		JournalSchema:         2,
		JournalMode:           0600,
		OriginalJournalSHA256: strings.Repeat("a", 64),
		TargetJournalSHA256:   strings.Repeat("a", 64),
		CapacityReceiptSHA256: strings.Repeat("b", 64),
		CapacityPayloadSHA256: strings.Repeat("c", 64),
	}
	if err := validateArchiveActivationBinding(base); err != nil {
		t.Fatal(err)
	}
	changed := *base
	changed.TargetJournalSHA256 = strings.Repeat("d", 64)
	if err := validateArchiveActivationBinding(&changed); err == nil {
		t.Fatal("changed target without artifact was accepted")
	}
	changed.RebindArtifactSHA256 = strings.Repeat("e", 64)
	if err := validateArchiveActivationBinding(&changed); err != nil {
		t.Fatal(err)
	}
	changed.StorageProtocol = 4
	if err := validateArchiveActivationBinding(&changed); err == nil {
		t.Fatal("non-protocol-5 binding was accepted")
	}
}

func TestArchiveActivationBindingStrictAndLegacyJSONShapes(t *testing.T) {
	t.Parallel()
	legacyShared, err := json.Marshal(validSharedFixture())
	if err != nil || validateSharedShape(legacyShared) != nil {
		t.Fatalf("legacy shared shape: %v", err)
	}
	binding := &archiveActivationBinding{SchemaVersion: 1, StorageProtocol: 5, JournalSchema: 2, JournalMode: 0600, OriginalJournalSHA256: strings.Repeat("a", 64), TargetJournalSHA256: strings.Repeat("a", 64), CapacityReceiptSHA256: strings.Repeat("b", 64), CapacityPayloadSHA256: strings.Repeat("c", 64)}
	boundShared := validSharedFixture()
	boundShared.Participants[0].ArchiveActivationBinding = binding
	boundRaw, err := json.Marshal(boundShared)
	if err != nil || validateSharedShape(boundRaw) != nil {
		t.Fatalf("bound shared shape: %v", err)
	}
	legacyPolicy := activationFixture(t)
	legacyRaw, err := json.Marshal(legacyPolicy)
	if err != nil || validatePolicyActivationShape(legacyRaw) != nil {
		t.Fatalf("legacy policy shape: %v", err)
	}
	legacyPolicy.Plan.ArchiveActivationBinding = binding
	policyRaw, err := json.Marshal(legacyPolicy)
	if err != nil || validatePolicyActivationShape(policyRaw) != nil {
		t.Fatalf("bound policy shape: %v", err)
	}
	unknown := bytes.Replace(policyRaw, []byte(`"archiveActivationBinding":{"schemaVersion":1`), []byte(`"archiveActivationBinding":{"schemaVersion":1,"unknown":true`), 1)
	if err := validatePolicyActivationShape(unknown); err == nil {
		t.Fatal("unknown binding field accepted")
	}
	nullBinding := bytes.Replace(policyRaw, []byte(`"archiveActivationBinding":{`), []byte(`"archiveActivationBinding":null,"discarded":{`), 1)
	if err := validatePolicyActivationShape(nullBinding); err == nil {
		t.Fatal("null binding accepted")
	}
}
