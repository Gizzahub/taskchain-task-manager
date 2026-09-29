package taskstore

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestArchiveCapacityOutputDiskContract(t *testing.T) {
	t.Parallel()
	dir, r, _ := archiveBoardFixture(t)
	upgradeID := strings.Repeat("d", 32)

	result, err := ArchiveCapacity(dir, upgradeID, true, false)
	if err != nil {
		t.Fatal(err)
	}
	resultJSON, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	wantResultJSON := `{"upgradeId":"` + upgradeID + `","status":"completed","storageProtocol":5,"journalSchema":2}`
	if string(resultJSON) != wantResultJSON {
		t.Fatalf("public result JSON changed:\n got %s\nwant %s", resultJSON, wantResultJSON)
	}

	raw, err := os.ReadFile(filepath.Join(dir, archiveCapacityFile))
	if err != nil {
		t.Fatal(err)
	}
	adoption, err := loadArchiveCapacityAdoption(r)
	if err != nil {
		t.Fatal(err)
	}
	if adoption.SchemaVersion != 1 || adoption.StorageProtocol != 5 || adoption.Phase != "completed" {
		t.Fatalf("completed adoption changed: %+v", adoption)
	}
	wantRaw, err := archiveCapacityAdoptionBytes(adoption)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(raw, wantRaw) {
		t.Fatalf("completed adoption bytes changed:\n got %q\nwant %q", raw, wantRaw)
	}
}
