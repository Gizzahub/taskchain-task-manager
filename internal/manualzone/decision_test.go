package manualzone

import "testing"

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
