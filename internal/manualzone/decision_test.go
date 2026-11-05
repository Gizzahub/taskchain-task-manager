package manualzone

import "testing"

func TestParseCardDoesNotStopAtAFalseFence(t *testing.T) {
	const early = "receipts/TASK-001/manual-0123456789abcdef0123456789abcdef.json"
	const late = "receipts/TASK-001/manual-ffffffffffffffffffffffffffffffff.json"
	card := "---\nid: TASK-001\nexpected-receipt: {path: " + early + ", facts: early}\n---hidden\nexpected-receipt: {path: " + late + ", facts: late}\n---\nbody\n"
	fields, err := ParseCard(card)
	if err == nil && fields.ReceiptPath != late {
		t.Fatalf("admission receipt %q, want refusal or %q", fields.ReceiptPath, late)
	}
}

func TestDecodeReceiptRejectsDuplicateKeys(t *testing.T) {
	if _, err := DecodeReceipt([]byte(`{"version":1,"version":1}`)); err == nil {
		t.Fatal("duplicate key was accepted")
	}
	if _, err := DecodeEntry([]byte("{\"card-id\":\"TASK-001\",\"card-id\":\"TASK-001\"}\n")); err == nil {
		t.Fatal("duplicate entry key was accepted")
	}
	if _, err := DecodeReceipt([]byte(`{"version":1}{"version":1}`)); err == nil {
		t.Fatal("trailing json value was accepted")
	}
}
