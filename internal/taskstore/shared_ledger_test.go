package taskstore

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func validSharedFixture() sharedState {
	return sharedState{SchemaVersion: 1, NamespaceID: strings.Repeat("a", 32), BoardPath: "tasks", Phase: "active", Reserved: []string{"PLAN-2", "TASK-1"}, Participants: []sharedParticipant{{Root: "/work/a", HEAD: strings.Repeat("0", 40), Snapshot: strings.Repeat("1", 64), OriginalLedger: strings.Repeat("2", 64), TargetLedger: strings.Repeat("3", 64)}}}
}

func TestSharedStateRoundTripAndAtomicPublish(t *testing.T) {
	r, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	want := validSharedFixture()
	if err := publishSharedState(r, want, true); err != nil {
		t.Fatal(err)
	}
	got, err := loadSharedState(r)
	if err != nil {
		t.Fatal(err)
	}
	wantRaw, _ := json.Marshal(want)
	gotRaw, _ := json.Marshal(got)
	if !bytes.Equal(wantRaw, gotRaw) {
		t.Fatalf("got=%s want=%s", gotRaw, wantRaw)
	}
	before, err := r.ReadFile(sharedStateFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := publishSharedState(r, want, true); err == nil {
		t.Fatal("initial publish overwrote existing state")
	}
	after, _ := r.ReadFile(sharedStateFile)
	if !bytes.Equal(before, after) {
		t.Fatal("failed initial publish changed state")
	}
}

func TestSharedStateRejectsMalformedAndInvalid(t *testing.T) {
	root := t.TempDir()
	r, err := os.OpenRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	valid, _ := json.Marshal(validSharedFixture())
	nullParticipants := validSharedFixture()
	nullParticipants.Participants = nil
	nullRaw, _ := json.Marshal(nullParticipants)
	cases := map[string][]byte{
		"unknown":           bytes.Replace(valid, []byte(`"schemaVersion":1`), []byte(`"schemaVersion":1,"extra":1`), 1),
		"duplicate":         bytes.Replace(valid, []byte(`"schemaVersion":1`), []byte(`"schemaVersion":1,"schemaVersion":1`), 1),
		"bad path":          bytes.Replace(valid, []byte(`"boardPath":"tasks"`), []byte(`"boardPath":"../tasks"`), 1),
		"bad reserved":      bytes.Replace(valid, []byte(`"TASK-1"`), []byte(`"TASK-01"`), 1),
		"bad participant":   bytes.Replace(valid, []byte(`"root":"/work/a"`), []byte(`"root":"relative"`), 1),
		"null participants": nullRaw,
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			if !json.Valid(raw) {
				t.Fatal("fixture must be syntactically valid JSON")
			}
			if err := os.WriteFile(filepath.Join(root, sharedStateFile), raw, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := loadSharedState(r); err == nil {
				t.Fatal("invalid state accepted")
			}
		})
	}
	if err := os.WriteFile(filepath.Join(root, sharedStateFile), bytes.Repeat([]byte("x"), maxSharedStateBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadSharedState(r); err == nil {
		t.Fatal("oversized state accepted")
	}
}

func TestSharedStateParticipantBound(t *testing.T) {
	s := validSharedFixture()
	s.Participants = make([]sharedParticipant, 257)
	for i := range s.Participants {
		s.Participants[i] = validSharedFixture().Participants[0]
		s.Participants[i].Root = "/work/" + string(rune('a'+i%26)) + "/" + string(rune('a'+i/26))
	}
	if err := validateSharedState(s); err == nil {
		t.Fatal("257 participants accepted")
	}
}
