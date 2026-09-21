// Package outputvocab names the string literals this CLI writes on stdout.
//
// The JSON this program emits on stdout is its entire public interface: no
// consumer sees a Go type, only the byte stream. That makes these literals
// the contract, and until now nothing in the source named them — each was a
// bare string typed again at its construction site. This package gives each
// one a name.
//
// Every type here is a defined string type with one exported constant per
// value it is known to take on stdout. A named type marshals to the exact
// same bytes as the bare string it replaces, so replacing a literal at its
// construction site changes naming only: the output is unchanged, byte for
// byte. Two of these vocabularies are no longer naming-only, though:
// boardpolicy derives its known directories and default zone-to-status map
// from AllZones() and AllStatuses() (internal/boardpolicy/policy.go's
// baseDirs and defaultStatuses), so adding a member to either now changes
// which directories and statuses the board policy accepts — a behavioural
// coupling this package's own TestZoneVocabularyMatchesDefaultPolicy pins.
//
// ArchiveOperation is a third exception, and unlike those two it is a defect
// rather than a design. Its members are also written to disk: the archive
// journal's archiveRecord.Operation holds them (internal/taskstore/
// archive_record.go:19), and that record's validator still compares bare
// literals (archive_record.go:71-151). Renaming an ArchiveOperation constant
// would therefore change the on-disk format and the validator would stop
// matching journals already written. One field being both a public output
// and a storage format is the thing to fix — by splitting the two contracts,
// not by widening this package's scope to cover the journal. Every other
// journal write in this repository deliberately uses a bare literal for
// exactly that reason.
//
// completed means TRANSACTION completion, not work completion. A rejoin,
// archive, repair, relocation, transition, or policy activation reports
// Status/Phase "completed" the instant its own durable step lands — never as
// a claim about the work item's lifecycle. Design doc 11 warns about exactly
// this: a consumer that branches on status == "completed" and reads it as
// "the task is done" collapses a distinction this project maintains
// everywhere else, between a transaction settling and a task finishing.
//
// Go has no sum type, so nothing here stops a call site from writing a new
// bare string instead of reaching for a constant, and nothing here stops a
// switch from a silently missing case. That is not a mistake: it is a
// limit of the language. Exhaustiveness — every declared member reachable,
// every declared member covered by the switches that consume it — is
// checked by the tests in this package, not proven by the compiler. Treat a
// green test suite as current evidence, not a permanent guarantee: a new
// member added without updating its All<X>() slice, or a switch grown
// without a matching case, will not fail to compile.
package outputvocab

// ValidationState is the tri-state result of a validation dimension this
// call path did not check. The only member ever observed on stdout is
// NotEvaluated: no code path in this repository reports a validation
// dimension as evaluated-and-passed or evaluated-and-failed through this
// type. Fields of this type exist to make that omission explicit rather
// than silently absent from the JSON.
//
// No switch in this codebase consumes ValidationState: every field of this
// type is only ever constructed as NotEvaluated, and nothing reads such a
// field back to branch on its value. Grounding search (from the repository
// root): `grep -rn "BoardValidation\|EvaluationValidation\|outputvocab.NotEvaluated" internal cmd | grep -v _test.go`
// — every match is a struct-field declaration or a NotEvaluated
// construction site, never a switch or comparison against another member.
type ValidationState string

const (
	// NotEvaluated marks a validation dimension (board membership, evidence,
	// shared readiness, reference resolution) that this call path did not
	// check. It is not a verdict — it is the absence of one.
	NotEvaluated ValidationState = "not_evaluated"
)

// AllValidationStates lists every ValidationState member this package
// declares. The exhaustiveness test in this package fails if a declared
// member is missing here.
func AllValidationStates() []ValidationState {
	return []ValidationState{NotEvaluated}
}

// ResultStatus is the status of a completed storage transaction: archive,
// repair, relocation, transition, policy activation, or archive-capacity
// upgrade. See the package doc for why "completed" here is a transaction
// verdict, not a work-item verdict.
type ResultStatus string

const (
	// Completed marks the transaction as durably landed. It is the only
	// ResultStatus member observed on stdout: every one of these operations
	// either lands or returns an error, so there is no separate "failed"
	// or "pending" status to report here.
	Completed ResultStatus = "completed"
)

// AllResultStatuses lists every ResultStatus member this package declares.
func AllResultStatuses() []ResultStatus {
	return []ResultStatus{Completed}
}

// No switch in this codebase consumes ResultStatus: every field of this type
// is only ever constructed as Completed; the one read site
// (policy_activate.go's finishPolicyActivation) is a single equality guard
// against that same sole member, not a multi-case switch. Grounding search
// (from the repository root):
// `grep -rn "outputvocab.Completed\b|ResultStatus\b" internal cmd | grep -v _test.go | grep -v outputvocab.go`
// — every match is a struct-field declaration, a Completed construction
// site, or the one equality guard above.

// RejoinPhase is the lifecycle phase of one owner-rejoin board, as reported
// by RejoinBoardResult.Phase.
//
// This is deliberately narrower than the board's internal shared-state
// phase machine (which also uses "initializing", "active", and other
// values for its own on-disk journal bookkeeping): those values never
// reach stdout through RejoinBoardResult and are out of scope for this
// package, whose contract is the byte stream, not the journal format.
type RejoinPhase string

const (
	// RejoinAbsent means no owner-rejoin evidence exists for this board yet.
	RejoinAbsent RejoinPhase = "absent"
	// RejoinPrepared means a rejoin plan and payload were derived but not
	// yet applied.
	RejoinPrepared RejoinPhase = "prepared"
	// RejoinCompleted means the rejoin transaction durably landed. See the
	// package doc: this is transaction completion, not work completion.
	RejoinCompleted RejoinPhase = "completed"
)

// AllRejoinPhases lists every RejoinPhase member this package declares.
func AllRejoinPhases() []RejoinPhase {
	return []RejoinPhase{RejoinAbsent, RejoinPrepared, RejoinCompleted}
}

// No switch in this codebase consumes RejoinPhase: RejoinBoardResult.Phase is
// only ever constructed (rejoinBoardResult and the "absent" literal result in
// owner_rejoin_command.go), never read back to branch on its value — the CLI
// command that produces it (cmd/taskchain-task-manager/rejoin_board.go)
// writes the whole result straight to JSON. Grounding search (from the
// repository root): `grep -rn "\.Phase\b" internal cmd | grep -v _test.go`
// — every RejoinBoardResult.Phase match is a construction site; the many
// other ".Phase" matches belong to unrelated internal journal/state types
// (sharedState, policyActivationState, archiveJournal, ...), not this type.

// SharedPhase is the lifecycle phase of a shared (multi-worktree) board's
// storage state, as reported by SharedResult.Phase. Like RejoinPhase, this
// names only the values that reach stdout through SharedResult; the shared
// storage journal itself carries other phase values used purely as
// internal bookkeeping and never echoed to the caller through this field.
type SharedPhase string

const (
	// SharedInitializing means the shared board was just enabled or newly
	// joined and has not yet completed its first shared step.
	SharedInitializing SharedPhase = "initializing"
	// SharedActive means the shared board is in normal, steady-state use.
	SharedActive SharedPhase = "active"
)

// AllSharedPhases lists every SharedPhase member this package declares.
func AllSharedPhases() []SharedPhase {
	return []SharedPhase{SharedInitializing, SharedActive}
}

// No switch in this codebase consumes SharedPhase: SharedResult.Phase is only
// ever constructed, at shared_enable.go's one call site
// (`Phase: outputvocab.SharedPhase(s.Phase)`), and nothing reads a
// SharedResult.Phase field back to branch on it. Grounding search (from the
// repository root): `grep -rn "outputvocab.SharedPhase\|SharedResult" internal cmd | grep -v _test.go`
// — the only non-declaration matches are that one construction site and the
// SharedResult struct definition.

// RejoinMode says whether an owner-rejoin board shares a common Git
// directory with its source, or was rejoined from an independent clone.
type RejoinMode string

const (
	// IndependentClone means the target board was cloned independently of
	// the source and does not share its common Git directory.
	IndependentClone RejoinMode = "independent-clone"
	// SameCommon means the target board shares the source's common Git
	// directory (an ordinary worktree relationship).
	SameCommon RejoinMode = "same-common"
)

// AllRejoinModes lists every RejoinMode member this package declares.
func AllRejoinModes() []RejoinMode {
	return []RejoinMode{IndependentClone, SameCommon}
}

// No switch in this codebase consumes RejoinMode: rejoinMode(clone bool)
// constructs it from a boolean with a two-way if/else (not a switch, and
// both branches are necessarily covered by the type checker's own boolean
// exhaustiveness), and nothing reads RejoinBoardResult.Mode back to branch on
// it. Grounding search (from the repository root):
// `grep -rn "RejoinMode" internal cmd | grep -v _test.go | grep -v outputvocab.go`
// — the only matches are the Mode field declaration and the rejoinMode
// constructor.

// ArchiveOperation is the kind of archive transaction requested or
// recorded, as reported by ArchiveResult.Operation.
//
// Unlike every other type here, these values are not confined to stdout.
// They are also stored in the on-disk archiveRecord.Operation
// (internal/taskstore/archive_record.go:19), written at
// legacy_archive_request.go:86 and, by way of a flag default, at
// cmd/taskchain-task-manager/archive.go:32. The record validator at
// archive_record.go:71-151 checks that stored value against bare literals.
// Renaming a constant here changes the on-disk format; the validator will
// not follow it.
type ArchiveOperation string

const (
	// ArchiveOp is a normal archive: the source card moves to its policy
	// destination unchanged.
	ArchiveOp ArchiveOperation = "archive"
	// Supersede archives a card after marking it superseded.
	Supersede ArchiveOperation = "supersede"
	// Force archives a card outside the normal policy gate, with an
	// explicit operator assertion recorded in its place.
	Force ArchiveOperation = "force"
	// LegacyAdoption archives a card that predates this project's archive
	// record format, under an explicit adoption assertion.
	LegacyAdoption ArchiveOperation = "legacy-adoption"
)

// AllArchiveOperations lists every ArchiveOperation member this package
// declares.
func AllArchiveOperations() []ArchiveOperation {
	return []ArchiveOperation{ArchiveOp, Supersede, Force, LegacyAdoption}
}

// Zone is the name of one board directory (a workflow zone, a parked kind
// directory, or an archive directory) in the default board policy.
type Zone string

const (
	ZoneTodo    Zone = "todo"
	ZoneDoing   Zone = "doing"
	ZoneReview  Zone = "review"
	ZoneBlocked Zone = "blocked"
	ZoneDone    Zone = "done"
	ZoneIssue   Zone = "issue"
	ZonePlan    Zone = "plan"
	ZoneBacklog Zone = "backlog"
	ZoneArchive Zone = "archive"
	// ZoneArchiveHidden is the dotfile-adjacent "_archive" spelling.
	ZoneArchiveHidden Zone = "_archive"
)

// AllZones lists every Zone member this package declares, in the default
// board policy's own order.
func AllZones() []Zone {
	return []Zone{
		ZoneTodo, ZoneDoing, ZoneReview, ZoneBlocked, ZoneDone,
		ZoneIssue, ZonePlan, ZoneBacklog, ZoneArchive, ZoneArchiveHidden,
	}
}

// Status is a workflow zone's card status, as recorded by the default board
// policy and validated by any declared policy's zone-status and
// kind-status bindings.
type Status string

const (
	StatusPending    Status = "pending"
	StatusInProgress Status = "in-progress"
	StatusReview     Status = "review"
	StatusBlocked    Status = "blocked"
	StatusDone       Status = "done"
	StatusCancelled  Status = "cancelled"
)

// AllStatuses lists every Status member this package declares.
func AllStatuses() []Status {
	return []Status{
		StatusPending, StatusInProgress, StatusReview,
		StatusBlocked, StatusDone, StatusCancelled,
	}
}

// Scope names the kind of document or observation a validate/register/show
// result describes, as reported by the "scope" field of several unrelated
// result types (standalone document validation, board-context registration,
// card validation, and card-completion observation). It is unrelated to
// AuthorityScope below, which answers a different question (is this policy
// authority local or shared) under the same English word.
type Scope string

const (
	// ScopePolicyDocument marks a standalone policy-document validation
	// result (validate-policy).
	ScopePolicyDocument Scope = "policy-document"
	// ScopeBoardContext marks a board-context registration or show result
	// (RegisterContext, ShowContext).
	ScopeBoardContext Scope = "board-context"
	// ScopeIntentBatchDocument marks a standalone intent or batch document
	// validation result (validate-context).
	ScopeIntentBatchDocument Scope = "intent-batch-document"
	// ScopeIterationDocument marks a standalone iteration document
	// validation result (validate-context).
	ScopeIterationDocument Scope = "iteration-document"
	// ScopeCard marks a work-task card validation result.
	ScopeCard Scope = "card"
	// ScopeCardCompletionObservation marks a card-completion observation
	// result: it reports whether a card looks complete without acting on
	// that observation.
	ScopeCardCompletionObservation Scope = "card-completion-observation"
)

// AllScopes lists every Scope member this package declares.
func AllScopes() []Scope {
	return []Scope{
		ScopePolicyDocument, ScopeBoardContext, ScopeIntentBatchDocument,
		ScopeIterationDocument, ScopeCard, ScopeCardCompletionObservation,
	}
}

// ContextStatus is the outcome of one board-context registration call, as
// reported by ContextResult.Status.
type ContextStatus string

const (
	// ContextUnchanged means the same immutable key and content were already
	// registered; nothing was written.
	ContextUnchanged ContextStatus = "unchanged"
	// ContextRegistered means this call durably registered a new document.
	ContextRegistered ContextStatus = "registered"
	// ContextStored means a previously registered document was read back
	// (ShowContext), not written by this call.
	ContextStored ContextStatus = "stored"
)

// AllContextStatuses lists every ContextStatus member this package
// declares.
func AllContextStatuses() []ContextStatus {
	return []ContextStatus{ContextUnchanged, ContextRegistered, ContextStored}
}

// ReferenceCheckState is the outcome of checking a board-context document's
// cross-document references, as reported by ContextResult.ReferenceValidation.
// This is a richer, board-aware check than ValidationState above (which only
// ever reports the absence of a check); it is a separate type because its
// members answer a different question and are never interchangeable with
// ValidationState's.
type ReferenceCheckState string

const (
	// ReferenceNotApplicable means this document kind carries no references
	// to check (an intent document).
	ReferenceNotApplicable ReferenceCheckState = "not_applicable"
	// ReferenceVerified means this call resolved and checked every
	// reference the document declares.
	ReferenceVerified ReferenceCheckState = "verified"
	// ReferenceNotRechecked means the document was already registered and
	// its references were verified at that time, not re-verified now.
	ReferenceNotRechecked ReferenceCheckState = "not_rechecked"
)

// AllReferenceCheckStates lists every ReferenceCheckState member this
// package declares.
func AllReferenceCheckStates() []ReferenceCheckState {
	return []ReferenceCheckState{ReferenceNotApplicable, ReferenceVerified, ReferenceNotRechecked}
}

// AuthorityScope says whether a policy authority is local to one board or
// shared across a namespace of boards, as reported by
// PolicyActivationResult.Scope.
type AuthorityScope string

const (
	// AuthorityScopeLocal means the policy authority governs this board
	// alone.
	AuthorityScopeLocal AuthorityScope = "local"
	// AuthorityScopeShared means the policy authority governs every board
	// sharing this namespace.
	AuthorityScopeShared AuthorityScope = "shared"
)

// AllAuthorityScopes lists every AuthorityScope member this package
// declares.
func AllAuthorityScopes() []AuthorityScope {
	return []AuthorityScope{AuthorityScopeLocal, AuthorityScopeShared}
}

// RejoinRole names one file or artifact carried by an owner-rejoin payload,
// as reported by RejoinBoardResult.Files[].Role and
// RejoinBoardResult.Artifacts[].Role. The eight same-common file roles
// identify which journal a file is; ArchiveCapacityPayload identifies the
// one artifact kind a rejoin can carry (a storage-protocol upgrade payload).
// Both use the same JSON key ("role") on their respective array elements,
// so they share one vocabulary here even though they populate two different
// struct fields.
//
// This type is applied only at the two struct fields above, which are the
// only Role-shaped values that reach stdout. The same eight file-role
// strings also key several internal-only maps (paths, source/target file
// bytes, a custom binary payload encoding) across owner_rejoin_*.go; those
// stay plain strings because retyping them would ripple through a wire
// format this package does not own, for no change in what reaches stdout.
type RejoinRole string

const (
	RejoinRoleArchive          RejoinRole = "archive"
	RejoinRoleArchiveCapacity  RejoinRole = "archive-capacity"
	RejoinRoleIDs              RejoinRole = "ids"
	RejoinRolePolicy           RejoinRole = "policy"
	RejoinRolePolicyActivation RejoinRole = "policy-activation"
	RejoinRoleRelocations      RejoinRole = "relocations"
	RejoinRoleRepairs          RejoinRole = "repairs"
	RejoinRoleTransitions      RejoinRole = "transitions"
	// RejoinRoleArchiveCapacityPayload is the one OwnerRejoinArtifact role:
	// a storage-protocol-upgrade payload derived when a rejoin also adopts
	// a newer archive capacity.
	RejoinRoleArchiveCapacityPayload RejoinRole = "archive-capacity-payload"
)

// AllRejoinRoles lists every RejoinRole member this package declares.
func AllRejoinRoles() []RejoinRole {
	return []RejoinRole{
		RejoinRoleArchive, RejoinRoleArchiveCapacity, RejoinRoleIDs, RejoinRolePolicy,
		RejoinRolePolicyActivation, RejoinRoleRelocations, RejoinRoleRepairs, RejoinRoleTransitions,
		RejoinRoleArchiveCapacityPayload,
	}
}

// Severity classifies one ValidationFinding as blocking (Error, which also
// flips ValidationReport.Valid to false) or advisory (Warning, which does
// not), as reported by ValidationFinding.Severity.
//
// No switch in this codebase consumes Severity: every ValidationFinding is
// only ever constructed with one of the two literals below (validation.go's
// add() closure and its filename-pattern check, and completion.go's two
// completion-criteria findings), and nothing reads a Severity field back to
// branch on it — a consumer would have to do that branching on the other
// side of stdout. Grounding search (from the repository root):
// `grep -rn "\.Severity\b|ValidationFinding{" internal cmd | grep -v _test.go`
// — every match is the field declaration or one of those four construction
// sites.
type Severity string

const (
	// SeverityError marks a finding that also makes the report invalid.
	SeverityError Severity = "error"
	// SeverityWarning marks a finding that does not affect report validity.
	SeverityWarning Severity = "warning"
)

// AllSeverities lists every Severity member this package declares.
func AllSeverities() []Severity {
	return []Severity{SeverityError, SeverityWarning}
}

// ClaimStatus is the lifecycle state of one task claim, as reported by
// ClaimRecord.Status (encoded to stdout by the claim and release commands).
//
// Unlike most vocabularies in this package, ClaimStatus DOES have a real
// consumer: validateClaims in claims.go rejects any ledger record whose
// Status is neither ClaimHeld nor ClaimReleased
// (`record.Status != ClaimHeld && record.Status != ClaimReleased`), so a
// value outside this two-member set is a genuine, checked error rather than
// silently accepted. TestClaimStatusVocabularyExhaustive in claims_test.go
// exercises that check end to end.
type ClaimStatus string

const (
	// ClaimHeld means the claim currently blocks the task from being
	// claimed again.
	ClaimHeld ClaimStatus = "held"
	// ClaimReleased means the claim no longer blocks the task.
	ClaimReleased ClaimStatus = "released"
)

// AllClaimStatuses lists every ClaimStatus member this package declares.
func AllClaimStatuses() []ClaimStatus {
	return []ClaimStatus{ClaimHeld, ClaimReleased}
}
