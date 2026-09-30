// Package taskflow reproduces the CE `task` noun's board semantics against a
// plain tasks/ tree: lenient reading, zone-decides-status, and the byte shapes
// the CE parity fixtures pin. It is deliberately separate from internal/card
// and internal/taskstore: card.Parse is a strict reader and taskstore runs a
// claim protocol, while this engine must never refuse a board a CE user could
// still list. Everything here mirrors the pinned reference (bb970b24).
package taskflow

import "strings"

// Status is the workflow state a zone directory or a card body encodes.
type Status string

const (
	StatusPending    Status = "pending"
	StatusInProgress Status = "in-progress"
	StatusReview     Status = "review"
	StatusBlocked    Status = "blocked"
	StatusDone       Status = "done"
	StatusCancelled  Status = "cancelled"
)

// Dir is the canonical zone directory spelling of the status.
func (s Status) Dir() string {
	switch s {
	case StatusInProgress:
		return "doing"
	case StatusPending:
		return "todo"
	case StatusReview:
		return "review"
	case StatusBlocked:
		return "blocked"
	case StatusDone:
		return "done"
	}
	return ""
}

// Symbol is the checkbox marker the body's **Status** cell uses.
func (s Status) Symbol() string {
	switch s {
	case StatusInProgress:
		return "[~]"
	case StatusReview:
		return "[>]"
	case StatusDone:
		return "[x]"
	case StatusBlocked:
		return "[!]"
	case StatusCancelled:
		return "[-]"
	}
	return "[ ]"
}

// Label is the Title-case human label shown beside the symbol.
func (s Status) Label() string {
	switch s {
	case StatusInProgress:
		return "In Progress"
	case StatusReview:
		return "Review"
	case StatusDone:
		return "Done"
	case StatusBlocked:
		return "Blocked"
	case StatusCancelled:
		return "Cancelled"
	}
	return "Pending"
}

// StatusCell is the canonical **Status** table cell value.
func (s Status) StatusCell() string { return s.Symbol() + " " + s.Label() }

// zoneDirAliases is every directory spelling recognised as a workflow zone,
// mapped to the status it encodes. Drift spellings stay readable forever.
var zoneDirAliases = map[string]Status{
	"doing": StatusInProgress, "doings": StatusInProgress,
	"in-progress": StatusInProgress, "in_progress": StatusInProgress,
	"inprogress": StatusInProgress, "wip": StatusInProgress,

	"todo": StatusPending, "todos": StatusPending, "pending": StatusPending,

	"review": StatusReview, "reviews": StatusReview,
	"in-review": StatusReview, "in_review": StatusReview,

	"blocked": StatusBlocked, "blockeds": StatusBlocked,
	"suspended": StatusBlocked, "suspend": StatusBlocked,
	"on-hold": StatusBlocked, "on_hold": StatusBlocked, "waiting": StatusBlocked,

	"done": StatusDone, "dones": StatusDone,
	"completed": StatusDone, "complete": StatusDone, "finished": StatusDone,
}

// statusWordAliases extends zoneDirAliases with spellings sound as a
// frontmatter value but too vague to name a directory.
var statusWordAliases = map[string]Status{
	"active": StatusPending, "open": StatusPending, "ready": StatusPending,
	"closed":    StatusDone,
	"cancelled": StatusCancelled, "canceled": StatusCancelled,
	"wontfix": StatusCancelled, "dropped": StatusCancelled,
}

// StatusFromDir maps a workflow directory to its status.
func StatusFromDir(dir string) (Status, bool) {
	st, ok := zoneDirAliases[strings.ToLower(strings.TrimSpace(dir))]
	return st, ok
}

// StatusFromWord maps a frontmatter status word to its status.
func StatusFromWord(word string) (Status, bool) {
	key := strings.ToLower(strings.TrimSpace(word))
	if st, ok := zoneDirAliases[key]; ok {
		return st, true
	}
	st, ok := statusWordAliases[key]
	return st, ok
}

// StorageWriteDir is where archive files cards into; LegacyStorageDir is the
// pre-rename spelling every reader still accepts.
const (
	StorageWriteDir  = "_archive"
	LegacyStorageDir = "archive"
)

// IsStorageDir reports whether a segment names long-term storage.
func IsStorageDir(name string) bool {
	return name == StorageWriteDir || name == LegacyStorageDir
}

// IsKindDir reports whether a segment parks a document by kind.
func IsKindDir(name string) bool {
	switch name {
	case "plan", "issue", "backlog":
		return true
	}
	return false
}

// idCardKind maps a card id's prefix to its kind word. An id that names no
// known kind is read as the default: the board's work card.
func idCardKind(id string) string {
	prefix, _, _ := strings.Cut(id, "-")
	switch prefix {
	case "TASK":
		return "task"
	case "PLAN":
		return "plan"
	case "ISSUE":
		return "issue"
	case "BACKLOG":
		return "backlog"
	}
	return "task"
}

// typesForKind is the type vocabulary a kind's cards draw from. One table
// serves both the creator (which writes a type) and the validator (which
// checks one), so the two readings cannot drift.
func typesForKind(kind string) []string {
	switch kind {
	case "plan":
		return []string{"plan", "roadmap", "phase"}
	case "issue":
		return []string{"bug", "blocker", "tech-debt"}
	case "backlog":
		return []string{"idea", "feature", "improvement", "tech-debt"}
	}
	return TaskTypes
}

// ZoneSegment returns the index of the first workflow-zone segment of an
// already-split task path (the file name is never considered) and the status
// that segment encodes.
func ZoneSegment(parts []string) (int, Status, bool) {
	for i := 0; i < len(parts)-1; i++ {
		if st, ok := StatusFromDir(parts[i]); ok {
			return i, st, true
		}
	}
	return -1, "", false
}

// MoveDestination is where one move lands. The default CE vocabulary has no
// declared zones or terminals, so resolution is StatusFromWord or nothing.
type MoveDestination struct {
	Zone     string
	Status   Status
	Terminal string
}

// ResolveMoveDestination turns the word a caller typed into a destination.
// A status word with no directory (cancelled) is refused: a cancelled card
// goes to storage, not to a sixth zone.
func ResolveMoveDestination(word string) (MoveDestination, bool) {
	trimmed := strings.ToLower(strings.TrimSpace(word))
	st, ok := StatusFromWord(trimmed)
	if !ok {
		return MoveDestination{}, false
	}
	if st.Dir() == "" {
		return MoveDestination{}, false
	}
	return MoveDestination{Zone: st.Dir(), Status: st}, true
}

// movedStatusWord is the frontmatter status for a destination: the zone
// directory spelling for workflow zones.
func movedStatusWord(dest MoveDestination) string {
	if dest.Status == "" {
		return ""
	}
	return dest.Zone
}
