package taskstore

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/Gizzahub/taskchain-task-manager/internal/boardpolicy"
	"github.com/Gizzahub/taskchain-task-manager/internal/card"
)

func completionIndexEntries(b archiveCompletionBinding) ([]Entry, Entry) {
	archived := Entry{Path: b.Target, Card: cardViewForCompletion(b.ID, "done", nil)}
	dependent := Entry{Path: "todo/TASK-2.md", Card: cardViewForCompletion("TASK-2", "pending", []string{"TASK-1"})}
	return []Entry{archived, dependent}, archived
}

func cardViewForCompletion(id, status string, deps []string) card.View {
	return card.View{ID: id, Status: status, DependsOn: deps}
}

func TestCompletionIndexBaselineArchivedBindingAndReady(t *testing.T) {
	b, raw, _, _ := archiveCompletionFixture(t)
	entries, archived := completionIndexEntries(b)
	policy := boardpolicy.Default()
	doneEntry := Entry{Path: "done/TASK-1.md", Card: cardViewForCompletion("TASK-1", "done", nil)}
	baselineEntries := []Entry{doneEntry, entries[1]}
	index, err := buildCompletionIndex(baselineEntries, policy, nil, "", nil)
	if err != nil || !index.done("TASK-1") {
		t.Fatalf("baseline index=%v err=%v", index, err)
	}
	ready := readyWithCompletion(baselineEntries, claimsLedger{}, policy, index)
	if len(ready) != 1 || ready[0].Card.ID != "TASK-2" {
		t.Fatalf("baseline ready=%+v", ready)
	}

	index, err = buildCompletionIndex(entries, policy, nil, b.BoardPath, nil)
	if err != nil || index.done("TASK-1") {
		t.Fatalf("archived without binding index=%v err=%v", index, err)
	}
	ready = readyWithCompletion(entries, claimsLedger{}, policy, index)
	if len(ready) != 0 {
		t.Fatalf("unbound archive became ready: %+v", ready)
	}

	bindings := map[string]archiveCompletionBinding{b.Identity: b}
	index, err = buildCompletionIndex(entries, policy, bindings, b.BoardPath, func(entry Entry) ([]byte, error) {
		if entry.Path == archived.Path {
			return raw, nil
		}
		return nil, errors.New("unexpected entry read")
	})
	if err != nil || !index.done("TASK-1") {
		t.Fatalf("validated binding index=%v err=%v", index, err)
	}
	ready = readyWithCompletion(entries, claimsLedger{}, policy, index)
	if len(ready) != 1 || ready[0].Card.ID != "TASK-2" {
		t.Fatalf("validated binding ready=%+v", ready)
	}
	held := readyWithCompletion(entries, claimsLedger{Records: []ClaimRecord{{ID: "TASK-2", Status: "held"}}}, policy, index)
	if len(held) != 0 {
		t.Fatalf("held dependent remained ready: %+v", held)
	}
}

func TestCompletionIndexDoesNotResurrectMissingCards(t *testing.T) {
	b, _, _, _ := archiveCompletionFixture(t)
	policy := boardpolicy.Default()
	missingReference := []Entry{{Path: "todo/TASK-2.md", Card: cardViewForCompletion("TASK-2", "pending", []string{"TASK-1"})}}
	if _, err := buildCompletionIndex(missingReference, policy, map[string]archiveCompletionBinding{b.Identity: b}, b.BoardPath, nil); err == nil || !strings.Contains(err.Error(), "missing dependency") {
		t.Fatalf("missing referenced card was resurrected: %v", err)
	}
	unreferenced := []Entry{{Path: "todo/TASK-2.md", Card: cardViewForCompletion("TASK-2", "pending", nil)}}
	index, err := buildCompletionIndex(unreferenced, policy, map[string]archiveCompletionBinding{b.Identity: b}, b.BoardPath, nil)
	if err != nil || index.done("TASK-1") {
		t.Fatalf("missing unreferenced card affected index=%v err=%v", index, err)
	}
}

func TestCompletionIndexRejectsDuplicatesTamperingReadErrorsAndSuperseded(t *testing.T) {
	b, raw, _, _ := archiveCompletionFixture(t)
	entries, _ := completionIndexEntries(b)
	policy := boardpolicy.Default()
	for _, tc := range []struct {
		name    string
		binding func(archiveCompletionBinding) archiveCompletionBinding
		read    func(Entry) ([]byte, error)
		want    string
	}{
		{"duplicate identity", func(x archiveCompletionBinding) archiveCompletionBinding { return x }, func(Entry) ([]byte, error) { return raw, nil }, "duplicate task identity"},
		{"hash tamper", func(x archiveCompletionBinding) archiveCompletionBinding {
			x.FinalSHA256 = strings.Repeat("0", 64)
			return x
		}, func(Entry) ([]byte, error) { return raw, nil }, "current card or board mismatch"},
		{"path tamper", func(x archiveCompletionBinding) archiveCompletionBinding {
			x.Target = "_archive/done/TASK-9.md"
			return x
		}, func(Entry) ([]byte, error) { return raw, nil }, "normal workflow-done"},
		{"raw identity tamper", func(x archiveCompletionBinding) archiveCompletionBinding {
			tampered := []byte(strings.Replace(string(raw), "TASK-001", "TASK-002", 1))
			x.FinalSHA256 = bytesDigest(tampered)
			return x
		}, func(Entry) ([]byte, error) {
			return []byte(strings.Replace(string(raw), "TASK-001", "TASK-002", 1)), nil
		}, "raw identity mismatch"},
		{"read error", func(x archiveCompletionBinding) archiveCompletionBinding { return x }, func(Entry) ([]byte, error) { return nil, fmt.Errorf("synthetic read failure") }, "read archive completion card"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.name == "duplicate identity" {
				duplicate := append([]Entry(nil), entries...)
				duplicate = append(duplicate, Entry{Path: "todo/TASK-001.md", Card: cardViewForCompletion("TASK-001", "pending", nil)})
				if _, err := buildCompletionIndex(duplicate, policy, nil, b.BoardPath, nil); err == nil || !strings.Contains(err.Error(), tc.want) {
					t.Fatalf("duplicate accepted: %v", err)
				}
				return
			}
			binding := tc.binding(b)
			if _, err := buildCompletionIndex(entries, policy, map[string]archiveCompletionBinding{binding.Identity: binding}, b.BoardPath, tc.read); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("tamper accepted or wrong error: %v", err)
			}
		})
	}
	superseded := []byte(strings.Replace(string(raw), "title: archived\n", "title: archived\nstatus: superseded\n", 1))
	supersededBinding := b
	supersededBinding.FinalSHA256 = bytesDigest(superseded)
	if _, err := buildCompletionIndex(entries, policy, map[string]archiveCompletionBinding{supersededBinding.Identity: supersededBinding}, b.BoardPath, func(Entry) ([]byte, error) { return superseded, nil }); err == nil || !strings.Contains(err.Error(), "superseded") {
		t.Fatalf("superseded card accepted: %v", err)
	}
}
