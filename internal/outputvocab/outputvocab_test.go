package outputvocab_test

import (
	"testing"

	"github.com/Gizzahub/taskchain-task-manager/internal/boardpolicy"
	"github.com/Gizzahub/taskchain-task-manager/internal/outputvocab"
)

// These are tests, not a proof of exhaustiveness. Go has no sum types: a
// switch that consumes one of these vocabularies can drop a case, and a new
// member can be declared here without being added to its All<X>() slice,
// and neither mistake fails to compile. What follows only catches mistakes
// this test suite happens to exercise.

func assertNoDuplicates[T comparable](t *testing.T, label string, values []T) {
	t.Helper()
	if len(values) == 0 {
		t.Fatalf("%s: declared zero members", label)
	}
	seen := make(map[T]bool, len(values))
	for _, v := range values {
		if seen[v] {
			t.Fatalf("%s: %v is declared more than once in All%s()", label, v, label)
		}
		seen[v] = true
	}
}

func TestAllValidationStates(t *testing.T) {
	assertNoDuplicates(t, "ValidationState", outputvocab.AllValidationStates())
}

func TestAllResultStatuses(t *testing.T) {
	assertNoDuplicates(t, "ResultStatus", outputvocab.AllResultStatuses())
}

func TestAllRejoinPhases(t *testing.T) {
	assertNoDuplicates(t, "RejoinPhase", outputvocab.AllRejoinPhases())
}

func TestAllSharedPhases(t *testing.T) {
	assertNoDuplicates(t, "SharedPhase", outputvocab.AllSharedPhases())
}

func TestAllRejoinModes(t *testing.T) {
	assertNoDuplicates(t, "RejoinMode", outputvocab.AllRejoinModes())
}

func TestAllArchiveOperations(t *testing.T) {
	assertNoDuplicates(t, "ArchiveOperation", outputvocab.AllArchiveOperations())
}

func TestAllZones(t *testing.T) {
	assertNoDuplicates(t, "Zone", outputvocab.AllZones())
}

func TestAllStatuses(t *testing.T) {
	assertNoDuplicates(t, "Status", outputvocab.AllStatuses())
}

func TestAllScopes(t *testing.T) {
	assertNoDuplicates(t, "Scope", outputvocab.AllScopes())
}

func TestAllContextStatuses(t *testing.T) {
	assertNoDuplicates(t, "ContextStatus", outputvocab.AllContextStatuses())
}

func TestAllReferenceCheckStates(t *testing.T) {
	assertNoDuplicates(t, "ReferenceCheckState", outputvocab.AllReferenceCheckStates())
}

func TestAllAuthorityScopes(t *testing.T) {
	assertNoDuplicates(t, "AuthorityScope", outputvocab.AllAuthorityScopes())
}

func TestAllRejoinRoles(t *testing.T) {
	assertNoDuplicates(t, "RejoinRole", outputvocab.AllRejoinRoles())
}

func TestAllSeverities(t *testing.T) {
	assertNoDuplicates(t, "Severity", outputvocab.AllSeverities())
}

func TestAllClaimStatuses(t *testing.T) {
	assertNoDuplicates(t, "ClaimStatus", outputvocab.AllClaimStatuses())
}

// TestZoneVocabularyMatchesDefaultPolicy exercises the one real switch that
// consumes the Zone vocabulary end to end: boardpolicy.Default() builds its
// known-directory list from the same literals this package now names. If a
// zone is added to one side and not the other, this fails.
func TestZoneVocabularyMatchesDefaultPolicy(t *testing.T) {
	known := boardpolicy.Default().KnownDirs()
	zones := outputvocab.AllZones()
	if len(known) != len(zones) {
		t.Fatalf("boardpolicy.Default().KnownDirs() has %d entries, outputvocab declares %d zones: %v vs %v", len(known), len(zones), known, zones)
	}
	for i, z := range zones {
		if known[i] != string(z) {
			t.Fatalf("zone %d: boardpolicy.Default().KnownDirs() has %q, outputvocab declares %q", i, known[i], z)
		}
	}
}

// TestVocabularySpelling is a golden spelling test: each expected string
// below is retyped by hand from the design docs / API contract, not copied
// from outputvocab.go's own constant declarations or derived from
// All<X>() slices. That separate origin is the entire point: if someone
// edits a constant's literal value (say, "completed" -> "complete") without
// meaning to change the wire format, every other test in this package that
// builds its expectation from the constant itself (or from All<X>()) would
// silently keep passing, because the "expected" side would have moved with
// the "actual" side. Typing the byte value out again here, independently,
// is the only way this package catches that kind of accidental rename. If
// this test ever needs updating, that is itself the signal: it means a
// stdout byte actually changed, which is exactly what design constraint D3
// forbids for this package without an explicit, reviewed exception.
func TestVocabularySpelling(t *testing.T) {
	validationStates := map[outputvocab.ValidationState]string{
		outputvocab.NotEvaluated: "not_evaluated",
	}
	resultStatuses := map[outputvocab.ResultStatus]string{
		outputvocab.Completed: "completed",
	}
	rejoinPhases := map[outputvocab.RejoinPhase]string{
		outputvocab.RejoinAbsent:    "absent",
		outputvocab.RejoinPrepared:  "prepared",
		outputvocab.RejoinCompleted: "completed",
	}
	sharedPhases := map[outputvocab.SharedPhase]string{
		outputvocab.SharedInitializing: "initializing",
		outputvocab.SharedActive:       "active",
	}
	rejoinModes := map[outputvocab.RejoinMode]string{
		outputvocab.IndependentClone: "independent-clone",
		outputvocab.SameCommon:       "same-common",
	}
	archiveOperations := map[outputvocab.ArchiveOperation]string{
		outputvocab.ArchiveOp:      "archive",
		outputvocab.Supersede:      "supersede",
		outputvocab.Force:          "force",
		outputvocab.LegacyAdoption: "legacy-adoption",
	}
	zones := map[outputvocab.Zone]string{
		outputvocab.ZoneTodo:          "todo",
		outputvocab.ZoneDoing:         "doing",
		outputvocab.ZoneReview:        "review",
		outputvocab.ZoneBlocked:       "blocked",
		outputvocab.ZoneDone:          "done",
		outputvocab.ZoneIssue:         "issue",
		outputvocab.ZonePlan:          "plan",
		outputvocab.ZoneBacklog:       "backlog",
		outputvocab.ZoneArchive:       "archive",
		outputvocab.ZoneArchiveHidden: "_archive",
	}
	statuses := map[outputvocab.Status]string{
		outputvocab.StatusPending:    "pending",
		outputvocab.StatusInProgress: "in-progress",
		outputvocab.StatusReview:     "review",
		outputvocab.StatusBlocked:    "blocked",
		outputvocab.StatusDone:       "done",
		outputvocab.StatusCancelled:  "cancelled",
	}
	scopes := map[outputvocab.Scope]string{
		outputvocab.ScopePolicyDocument:            "policy-document",
		outputvocab.ScopeBoardContext:              "board-context",
		outputvocab.ScopeIntentBatchDocument:       "intent-batch-document",
		outputvocab.ScopeIterationDocument:         "iteration-document",
		outputvocab.ScopeCard:                      "card",
		outputvocab.ScopeCardCompletionObservation: "card-completion-observation",
	}
	contextStatuses := map[outputvocab.ContextStatus]string{
		outputvocab.ContextUnchanged:  "unchanged",
		outputvocab.ContextRegistered: "registered",
		outputvocab.ContextStored:     "stored",
	}
	referenceCheckStates := map[outputvocab.ReferenceCheckState]string{
		outputvocab.ReferenceNotApplicable: "not_applicable",
		outputvocab.ReferenceVerified:      "verified",
		outputvocab.ReferenceNotRechecked:  "not_rechecked",
	}
	authorityScopes := map[outputvocab.AuthorityScope]string{
		outputvocab.AuthorityScopeLocal:  "local",
		outputvocab.AuthorityScopeShared: "shared",
	}
	rejoinRoles := map[outputvocab.RejoinRole]string{
		outputvocab.RejoinRoleArchive:                "archive",
		outputvocab.RejoinRoleArchiveCapacity:        "archive-capacity",
		outputvocab.RejoinRoleIDs:                    "ids",
		outputvocab.RejoinRolePolicy:                 "policy",
		outputvocab.RejoinRolePolicyActivation:       "policy-activation",
		outputvocab.RejoinRoleRelocations:            "relocations",
		outputvocab.RejoinRoleRepairs:                "repairs",
		outputvocab.RejoinRoleTransitions:            "transitions",
		outputvocab.RejoinRoleArchiveCapacityPayload: "archive-capacity-payload",
	}
	severities := map[outputvocab.Severity]string{
		outputvocab.SeverityError:   "error",
		outputvocab.SeverityWarning: "warning",
	}
	claimStatuses := map[outputvocab.ClaimStatus]string{
		outputvocab.ClaimHeld:     "held",
		outputvocab.ClaimReleased: "released",
	}

	checkSpelling(t, "ValidationState", validationStates)
	checkSpelling(t, "ResultStatus", resultStatuses)
	checkSpelling(t, "RejoinPhase", rejoinPhases)
	checkSpelling(t, "SharedPhase", sharedPhases)
	checkSpelling(t, "RejoinMode", rejoinModes)
	checkSpelling(t, "ArchiveOperation", archiveOperations)
	checkSpelling(t, "Zone", zones)
	checkSpelling(t, "Status", statuses)
	checkSpelling(t, "Scope", scopes)
	checkSpelling(t, "ContextStatus", contextStatuses)
	checkSpelling(t, "ReferenceCheckState", referenceCheckStates)
	checkSpelling(t, "AuthorityScope", authorityScopes)
	checkSpelling(t, "RejoinRole", rejoinRoles)
	checkSpelling(t, "Severity", severities)
	checkSpelling(t, "ClaimStatus", claimStatuses)

	// Cross-check: every member this hand-typed table covers must also be a
	// declared All<X>() member, and vice versa, so this golden test cannot
	// silently drift out of sync with the vocabulary it is meant to pin.
	// This compares the actual KEY SETS, not just their sizes: comparing
	// only len(table) against len(All<X>()) would let two compensating
	// errors -- one member added to All<X>() and a different one dropped --
	// keep the counts equal and pass.
	checkMatchesAll(t, "ValidationState", validationStates, outputvocab.AllValidationStates())
	checkMatchesAll(t, "ResultStatus", resultStatuses, outputvocab.AllResultStatuses())
	checkMatchesAll(t, "RejoinPhase", rejoinPhases, outputvocab.AllRejoinPhases())
	checkMatchesAll(t, "SharedPhase", sharedPhases, outputvocab.AllSharedPhases())
	checkMatchesAll(t, "RejoinMode", rejoinModes, outputvocab.AllRejoinModes())
	checkMatchesAll(t, "ArchiveOperation", archiveOperations, outputvocab.AllArchiveOperations())
	checkMatchesAll(t, "Zone", zones, outputvocab.AllZones())
	checkMatchesAll(t, "Status", statuses, outputvocab.AllStatuses())
	checkMatchesAll(t, "Scope", scopes, outputvocab.AllScopes())
	checkMatchesAll(t, "ContextStatus", contextStatuses, outputvocab.AllContextStatuses())
	checkMatchesAll(t, "ReferenceCheckState", referenceCheckStates, outputvocab.AllReferenceCheckStates())
	checkMatchesAll(t, "AuthorityScope", authorityScopes, outputvocab.AllAuthorityScopes())
	checkMatchesAll(t, "RejoinRole", rejoinRoles, outputvocab.AllRejoinRoles())
	checkMatchesAll(t, "Severity", severities, outputvocab.AllSeverities())
	checkMatchesAll(t, "ClaimStatus", claimStatuses, outputvocab.AllClaimStatuses())
}

// checkMatchesAll fails if the hand-typed spelling table's key set and the
// package's own All<X>() slice disagree in either direction: a member in
// one and not the other. Comparing sets (not just counts) is what catches
// two compensating errors -- a member added on one side, a different one
// dropped on the other -- that would leave len(table) == len(all) and slip
// past a size-only check.
func checkMatchesAll[T comparable](t *testing.T, label string, table map[T]string, all []T) {
	t.Helper()
	inAll := make(map[T]bool, len(all))
	for _, v := range all {
		inAll[v] = true
	}
	for k := range table {
		if !inAll[k] {
			t.Errorf("%s: spelling table has member %v that All%s() does not declare", label, k, label)
		}
	}
	inTable := make(map[T]bool, len(table))
	for k := range table {
		inTable[k] = true
	}
	for _, v := range all {
		if !inTable[v] {
			t.Errorf("%s: All%s() declares member %v that the spelling table is missing", label, label, v)
		}
	}
}

// checkSpelling fails if a member's spelling in the hand-typed table does not
// match its own string value — a constant's literal edited without updating
// this independent witness.
//
// It checks nothing else. It cannot see a member added to outputvocab.go and
// forgotten here, because it only walks the table it is given; the len()
// comparisons against All<X>() in the caller are what catch that. A member
// removed from outputvocab.go is caught by the compiler, since the table's
// keys are the constants themselves.
func checkSpelling[T ~string](t *testing.T, label string, want map[T]string) {
	t.Helper()
	for member, wantSpelling := range want {
		if string(member) != wantSpelling {
			t.Errorf("%s: member has spelling %q, golden table says %q", label, string(member), wantSpelling)
		}
	}
}

// TestStatusVocabularyAcceptedByPolicy exercises boardpolicy.New's
// zone-status validation switch: every declared Status member must be
// accepted as a parked-zone status, and a value outside the vocabulary must
// be rejected. This is the "switch covers every declared member" check for
// the Status vocabulary.
func TestStatusVocabularyAcceptedByPolicy(t *testing.T) {
	for _, status := range outputvocab.AllStatuses() {
		zone := "zz-" + string(status)
		_, err := boardpolicy.New(boardpolicy.Declaration{
			Zones:      []string{zone},
			ZoneStatus: map[string]string{zone: string(status)},
		})
		if err != nil {
			t.Fatalf("status %q: declared a member boardpolicy.New rejected: %v", status, err)
		}
	}
	_, err := boardpolicy.New(boardpolicy.Declaration{
		Zones:      []string{"zz-bogus"},
		ZoneStatus: map[string]string{"zz-bogus": "not-a-real-status"},
	})
	if err == nil {
		t.Fatal("boardpolicy.New accepted a status outside the declared Status vocabulary; the switch may have grown a default case")
	}
}
