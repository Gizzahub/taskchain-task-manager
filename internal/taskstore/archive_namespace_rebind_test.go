package taskstore

import (
	"bytes"
	"strings"
	"testing"

	"github.com/Gizzahub/taskchain-task-manager/internal/card"
)

func TestArchiveNamespaceRebindPlanPreservesRecordsAndInventory(t *testing.T) {
	t.Parallel()
	j, rec, completion, raw := archiveJournalFixture(t)
	j.Namespace = ""
	rec.Namespace = ""
	rec.State, rec.Original, rec.Patched = "completed", nil, nil
	rec.Completion = &completion
	j.Records = []archiveRecord{rec}
	original, err := archiveJournalBytes(j)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := PlanArchiveNamespaceRebind(original, bytesDigest(original), j.BoardPath, "", strings.Repeat("b", 32), 0644)
	if err != nil {
		t.Fatal(err)
	}
	if plan.TargetNamespace != strings.Repeat("b", 32) || plan.OriginalJournalSHA256 != bytesDigest(original) || bytes.Equal(plan.TargetJournal, original) {
		t.Fatalf("bad plan=%+v", plan)
	}
	cards := map[string]ArchiveNamespaceCard{rec.Target: {Path: rec.Target, ID: rec.ID, Raw: raw, Mode: rec.Mode}}
	if err := VerifyArchiveNamespaceRebindInventory(plan, cards); err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeArchiveJournal(plan.TargetJournal)
	if err != nil || decoded.Namespace != plan.TargetNamespace || decoded.Records[0].PolicyDigest != rec.PolicyDigest || decoded.Records[0].Completion == nil {
		t.Fatalf("target=%+v err=%v", decoded, err)
	}
}

func TestArchiveNamespaceRebindRejectsPendingScopeAndInventoryTamper(t *testing.T) {
	t.Parallel()
	j, rec, _, _ := archiveJournalFixture(t)
	j.Records[0].State, j.Records[0].Original, j.Records[0].Patched = "completed", nil, nil
	original, err := archiveJournalBytes(j)
	if err != nil {
		t.Fatal(err)
	}
	pendingJournal, _, _, _ := archiveJournalFixture(t)
	pendingRaw, err := archiveJournalBytes(pendingJournal)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := PlanArchiveNamespaceRebind(pendingRaw, bytesDigest(pendingRaw), pendingJournal.BoardPath, pendingJournal.Namespace, strings.Repeat("b", 32), 0644); err == nil || !strings.Contains(err.Error(), "pending records") {
		t.Fatalf("pending journal boundary err=%v", err)
	}
	forgedBoard := j
	forgedBoard.BoardPath = "/other/tasks"
	forgedBoard.Records = []archiveRecord{j.Records[0]}
	forgedBoard.Records[0].Operation = "force"
	forgedBoard.Records[0].Assertion = "operator evidence"
	forgedBoard.Records[0].Completion = nil
	forgedBoard.Records[0].BoardPath = forgedBoard.BoardPath
	forgedRaw, err := archiveJournalBytes(forgedBoard)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := PlanArchiveNamespaceRebind(forgedRaw, bytesDigest(forgedRaw), j.BoardPath, j.Namespace, strings.Repeat("b", 32), 0644); err == nil || !strings.Contains(err.Error(), "source scope") {
		t.Fatalf("board scope boundary err=%v", err)
	}
	if _, err := PlanArchiveNamespaceRebind(original, bytesDigest(original), j.BoardPath, strings.Repeat("a", 32), strings.Repeat("b", 32), 0644); err == nil || !strings.Contains(err.Error(), "established namespace") {
		t.Fatalf("namespace scope boundary err=%v", err)
	}
	plan, err := PlanArchiveNamespaceRebind(original, bytesDigest(original), j.BoardPath, j.Namespace, strings.Repeat("b", 32), 0644)
	if err != nil {
		t.Fatal(err)
	}
	card := ArchiveNamespaceCard{Path: rec.Target, ID: rec.ID, Raw: []byte("tampered"), Mode: rec.Mode}
	if err := VerifyArchiveNamespaceRebindInventory(plan, map[string]ArchiveNamespaceCard{rec.Target: card}); err == nil {
		t.Fatal("tampered card accepted")
	}
	if err := VerifyArchiveNamespaceRebindInventory(plan, map[string]ArchiveNamespaceCard{}); err == nil {
		t.Fatal("missing card accepted")
	}
}

func TestArchiveNamespaceRebindRejectsHashModeAndTargetMutation(t *testing.T) {
	t.Parallel()
	j, _, _, _ := archiveJournalFixture(t)
	j.Records[0].State, j.Records[0].Original, j.Records[0].Patched = "completed", nil, nil
	original, err := archiveJournalBytes(j)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := PlanArchiveNamespaceRebind(original, strings.Repeat("0", 64), j.BoardPath, j.Namespace, strings.Repeat("b", 32), 0644); err == nil {
		t.Fatal("wrong original hash accepted")
	}
	if _, err := PlanArchiveNamespaceRebind(original, bytesDigest(original), j.BoardPath, j.Namespace, strings.Repeat("b", 32), 0); err == nil {
		t.Fatal("zero journal mode accepted")
	}
	if _, err := PlanArchiveNamespaceRebind(original, bytesDigest(original), j.BoardPath, j.Namespace, "bad", 0644); err == nil {
		t.Fatal("invalid target namespace accepted")
	}
	var plan ArchiveNamespaceRebindPlan
	plan, err = PlanArchiveNamespaceRebind(original, bytesDigest(original), j.BoardPath, j.Namespace, strings.Repeat("b", 32), 0644)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateArchiveNamespaceRebindPlan(plan); err != nil {
		t.Fatalf("positive plan failed exact transformation validation: %v", err)
	}
}

func TestArchiveNamespaceRebindAcceptsOperationDialectsAndEmptyJournal(t *testing.T) {
	t.Parallel()
	base, pending, _, raw := archiveJournalFixture(t)
	base.Records = nil
	base.Records = []archiveRecord{}
	empty, err := archiveJournalBytes(base)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := PlanArchiveNamespaceRebind(empty, bytesDigest(empty), base.BoardPath, "", strings.Repeat("c", 32), 0644); err != nil {
		t.Fatal("empty journal rejected:", err)
	}
	for _, operation := range []string{"archive", "legacy-adoption", "force", "supersede"} {
		t.Run(operation, func(t *testing.T) {
			rec := pending
			rec.State, rec.Original, rec.Patched, rec.Completion = "completed", nil, nil, nil
			rec.Operation, rec.Assertion = operation, "operator evidence"
			if operation == "archive" {
				rec.Completion = pending.Completion
				rec.Assertion = ""
			}
			if operation == "legacy-adoption" {
				rec.Source, rec.Target = "_archive/done/TASK-001.md", "_archive/done/TASK-001.md"
			}
			if operation == "supersede" {
				doc, parseErr := card.Parse(raw)
				if parseErr != nil {
					t.Fatal(parseErr)
				}
				patched, _, patchErr := doc.SetFrontmatterStatus("superseded")
				if patchErr != nil {
					t.Fatal(patchErr)
				}
				rec.FinalSHA256 = bytesDigest(patched)
			}
			j := base
			j.Records = []archiveRecord{rec}
			original, marshalErr := archiveJournalBytes(j)
			if marshalErr != nil {
				t.Fatal(marshalErr)
			}
			if _, planErr := PlanArchiveNamespaceRebind(original, bytesDigest(original), j.BoardPath, "", strings.Repeat("d", 32), 0644); planErr != nil {
				t.Fatal(planErr)
			}
		})
	}
	if _, err := PlanArchiveNamespaceRebind(empty, bytesDigest(empty), base.BoardPath, strings.Repeat("a", 32), strings.Repeat("b", 32), 0644); err == nil {
		t.Fatal("established namespace changed")
	}
}

func TestArchiveNamespaceRebindRejectsForgedPlanAndCardBindings(t *testing.T) {
	t.Parallel()
	j, rec, completion, raw := archiveJournalFixture(t)
	j.Namespace, rec.Namespace = "", ""
	rec.State, rec.Original, rec.Patched, rec.Completion = "completed", nil, nil, &completion
	j.Records = []archiveRecord{rec}
	original, err := archiveJournalBytes(j)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := PlanArchiveNamespaceRebind(original, bytesDigest(original), j.BoardPath, "", strings.Repeat("b", 32), 0644)
	if err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*archiveJournal){
		"owner": func(x *archiveJournal) { x.Records[0].Owner = "forged-owner" },
		"token": func(x *archiveJournal) { x.Records[0].Token = strings.Repeat("c", 32) },
	} {
		t.Run(name, func(t *testing.T) {
			decoded, decodeErr := decodeArchiveJournal(plan.TargetJournal)
			if decodeErr != nil {
				t.Fatal(decodeErr)
			}
			mutate(&decoded)
			forged, marshalErr := archiveJournalBytes(decoded)
			if marshalErr != nil {
				t.Fatal(marshalErr)
			}
			candidate := plan
			candidate.TargetJournal, candidate.TargetJournalSHA256 = forged, bytesDigest(forged)
			if err := ValidateArchiveNamespaceRebindPlan(candidate); err == nil {
				t.Fatal("well-formed forged target accepted")
			}
		})
	}
	card := ArchiveNamespaceCard{Path: rec.Target, ID: rec.ID, Raw: raw, Mode: rec.Mode}
	for name, mutate := range map[string]func(*ArchiveNamespaceCardBinding){
		"id":   func(b *ArchiveNamespaceCardBinding) { b.ID = "TASK-2" },
		"hash": func(b *ArchiveNamespaceCardBinding) { b.SHA256 = strings.Repeat("0", 64) },
		"mode": func(b *ArchiveNamespaceCardBinding) { b.Mode = 0600 },
	} {
		t.Run("binding-"+name, func(t *testing.T) {
			candidate := plan
			candidate.Cards = append([]ArchiveNamespaceCardBinding(nil), plan.Cards...)
			mutate(&candidate.Cards[0])
			if err := VerifyArchiveNamespaceRebindInventory(candidate, map[string]ArchiveNamespaceCard{rec.Target: card}); err == nil {
				t.Fatal("forged card binding accepted")
			}
		})
	}
	wrongOriginal := plan
	wrongOriginal.OriginalJournal = append([]byte(nil), plan.OriginalJournal...)
	wrongOriginal.OriginalJournal[0] ^= 1
	if err := ValidateArchiveNamespaceRebindPlan(wrongOriginal); err == nil {
		t.Fatal("tampered original journal accepted")
	}
	lieRaw := bytes.Replace(raw, []byte("TASK-001"), []byte("TASK-002"), 1)
	lying := j
	lying.Records = append([]archiveRecord(nil), j.Records...)
	lying.Records[0] = rec
	lying.Records[0].Operation = "force"
	lying.Records[0].Assertion = "operator evidence"
	lying.Records[0].Completion = nil
	lying.Records[0].OriginalSHA256 = bytesDigest(lieRaw)
	lying.Records[0].FinalSHA256 = bytesDigest(lieRaw)
	lyingRaw, err := archiveJournalBytes(lying)
	if err != nil {
		t.Fatal(err)
	}
	lyingPlan, err := PlanArchiveNamespaceRebind(lyingRaw, bytesDigest(lyingRaw), lying.BoardPath, "", strings.Repeat("c", 32), 0644)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyArchiveNamespaceRebindInventory(lyingPlan, map[string]ArchiveNamespaceCard{rec.Target: {Path: rec.Target, ID: rec.ID, Raw: lieRaw, Mode: rec.Mode}}); err == nil {
		t.Fatal("raw card identity lie accepted")
	}
}

func TestArchiveNamespaceRebindRejectsCurrentCompletionTamper(t *testing.T) {
	t.Parallel()
	j, rec, completion, raw := archiveJournalFixture(t)
	j.Namespace, rec.Namespace = "", ""
	rec.State, rec.Original, rec.Patched, rec.Completion = "completed", nil, nil, &completion
	for name, mutateRaw := range map[string]func([]byte) []byte{
		"superseded": func(value []byte) []byte {
			return bytes.Replace(value, []byte("---\n"), []byte("---\nstatus: superseded\n"), 1)
		},
		"missing review": func(value []byte) []byte {
			value = bytes.Replace(value, []byte("review-result: pass\n"), nil, 1)
			return bytes.Replace(value, []byte("review-proof: independent review\n"), nil, 1)
		},
	} {
		t.Run(name, func(t *testing.T) {
			current := mutateRaw(raw)
			if bytes.Equal(current, raw) {
				t.Fatal("mutation did not change the fixture")
			}
			candidateRecord := rec
			candidateRecord.OriginalSHA256 = bytesDigest(current)
			candidateRecord.FinalSHA256 = bytesDigest(current)
			candidateCompletion := completion
			candidateCompletion.FinalSHA256 = bytesDigest(current)
			candidateRecord.Completion = &candidateCompletion
			candidateJournal := j
			candidateJournal.Records = []archiveRecord{candidateRecord}
			journalRaw, err := archiveJournalBytes(candidateJournal)
			if err != nil {
				t.Fatal(err)
			}
			plan, err := PlanArchiveNamespaceRebind(journalRaw, bytesDigest(journalRaw), j.BoardPath, "", strings.Repeat("e", 32), 0644)
			if err != nil {
				t.Fatal(err)
			}
			if err := VerifyArchiveNamespaceRebindInventory(plan, map[string]ArchiveNamespaceCard{rec.Target: {Path: rec.Target, ID: rec.ID, Raw: current, Mode: rec.Mode}}); err == nil || !strings.Contains(err.Error(), map[string]string{"superseded": "superseded card cannot grant archive completion", "missing review": "historical admission is not satisfied"}[name]) {
				t.Fatalf("tampered completion current card err=%v", err)
			}
		})
	}
}

func TestArchiveNamespaceRebindRejectsOversizedForceCard(t *testing.T) {
	t.Parallel()
	j, rec, _, _ := archiveJournalFixture(t)
	j.Namespace, rec.Namespace = "", ""
	over := []byte("---\nid: TASK-001\ntitle: oversized\n---\n")
	over = append(over, bytes.Repeat([]byte("x"), maxCardBytes+1-len(over))...)
	if doc, parseErr := card.Parse(over); parseErr != nil || doc.View().ID != "TASK-001" {
		t.Fatalf("oversized fixture is not a valid card: %v", parseErr)
	}
	rec.State, rec.Original, rec.Patched, rec.Completion = "completed", nil, nil, nil
	rec.Operation, rec.Assertion = "force", "operator evidence"
	rec.OriginalSHA256, rec.FinalSHA256 = bytesDigest(over), bytesDigest(over)
	j.Records = []archiveRecord{rec}
	original, err := archiveJournalBytes(j)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := PlanArchiveNamespaceRebind(original, bytesDigest(original), j.BoardPath, "", strings.Repeat("f", 32), 0644)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyArchiveNamespaceRebindInventory(plan, map[string]ArchiveNamespaceCard{rec.Target: {Path: rec.Target, ID: rec.ID, Raw: over, Mode: rec.Mode}}); err == nil || !strings.Contains(err.Error(), "current card differs") {
		t.Fatalf("oversized force card err=%v", err)
	}
}
