// Package declaration reads inactive, versioned declaration fixtures. It never
// creates declarations, claims executions, calls providers, or changes a ledger.
package declaration

import (
	"errors"
	"path"
	"path/filepath"
	"reflect"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Gizzahub/taskchain-task-manager/internal/taskruntime"
)

const (
	SchemaV1     = "task-manager.declaration/v1"
	LegacyScheme = "execution-snapshot/v1"
	MaxBytes     = 64 * 1024
)

var ErrUnsupported = errors.New("unsupported declaration schema or reference scheme")
var ErrInvalid = errors.New("invalid declaration")

// Optional pointers preserve absence; v1 permits no JSON null values.
type Record struct {
	Schema      string     `json:"schema"`
	ID          string     `json:"id"`
	OccurredAt  string     `json:"occurred_at"`
	Repository  Repository `json:"repository"`
	Runtime     Runtime    `json:"runtime"`
	Subject     Subject    `json:"subject"`
	Privacy     Privacy    `json:"privacy"`
	Execution   *Reference `json:"execution,omitempty"`
	Scope       *Scope     `json:"scope,omitempty"`
	Predecessor *string    `json:"predecessor,omitempty"`
}

type Repository struct {
	Scheme       string  `json:"scheme"`
	ID           string  `json:"id"`
	CommonDir    string  `json:"common_dir"`
	CatalogAlias *string `json:"catalog_alias,omitempty"`
}

type Runtime struct {
	Name              string  `json:"name"`
	Provenance        string  `json:"provenance"`
	SessionID         *string `json:"session_id,omitempty"`
	UnavailableReason *string `json:"unavailable_reason,omitempty"`
}

// Topic is already redacted producer input, never raw conversation or tool data.
// Decoding checks its shape, not whether a producer removed every possible secret.
type Subject struct {
	Task  *string `json:"task,omitempty"`
	Topic *string `json:"topic,omitempty"`
}

type Privacy struct {
	Version string  `json:"version"`
	State   string  `json:"state"`
	Reason  *string `json:"reason,omitempty"`
}

type Scope struct {
	Reads     []Read   `json:"reads"`
	Writes    []string `json:"writes"`
	Contracts []string `json:"contracts"`
}

type Read struct {
	Path   string  `json:"path"`
	Commit *string `json:"commit,omitempty"`
}

type Reference struct {
	Scheme   string   `json:"scheme"`
	Snapshot Snapshot `json:"snapshot"`
}

// Snapshot preserves the complete legacy generation tuple without defaulting
// purpose, normalizing identity/path, or treating updatedAt as a generation.
type Snapshot struct {
	Task      string            `json:"task"`
	Type      string            `json:"type"`
	Purpose   string            `json:"purpose"`
	Source    string            `json:"source"`
	Owner     taskruntime.Owner `json:"owner"`
	Branch    string            `json:"branch"`
	Worktree  string            `json:"worktree"`
	StartedAt string            `json:"startedAt"`
}

func text(s string, limit int) bool {
	if s == "" || len(s) > limit || !utf8.ValidString(s) || strings.TrimSpace(s) != s {
		return false
	}
	for _, r := range s {
		if r == utf8.RuneError || unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return false
		}
	}
	return true
}
func optional(s *string, limit int) bool { return s == nil || text(*s, limit) }
func timestamp(s string) bool {
	t, err := time.Parse(time.RFC3339Nano, s)
	return err == nil && !t.IsZero() && strings.HasSuffix(s, "Z")
}
func absolute(s string) bool { return text(s, 4096) && filepath.IsAbs(s) && filepath.Clean(s) == s }
func relative(s string) bool {
	return text(s, 4096) && !strings.Contains(s, "\\") && !strings.Contains(s, ":") && !path.IsAbs(s) && path.Clean(s) == s && s != ".." && !strings.HasPrefix(s, "../")
}
func oid(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func (s Snapshot) validate() error {
	if !text(s.Task, 256) || !taskruntime.ValidType(s.Type) || !text(s.Source, 256) || !text(s.Branch, 1024) || !absolute(s.Worktree) || !timestamp(s.StartedAt) {
		return ErrInvalid
	}
	// Empty purpose is the actual legacy omitted value, not an inserted default.
	if s.Purpose != "" && s.Purpose != "delivery" && s.Purpose != "review" && s.Purpose != "experiment" {
		return ErrInvalid
	}
	if !text(s.Owner.Actor, 256) || !text(s.Owner.Host, 256) || (s.Owner.Kind != "agent" && s.Owner.Kind != "human") {
		return ErrInvalid
	}
	return nil
}

func (r Record) Validate() error {
	if r.Schema != SchemaV1 || r.Repository.Scheme != "repository-id/v1" || r.Execution != nil && r.Execution.Scheme != LegacyScheme {
		return ErrUnsupported
	}
	if !text(r.ID, 256) || !timestamp(r.OccurredAt) || !optional(r.Predecessor, 256) {
		return ErrInvalid
	}
	if r.Predecessor != nil && *r.Predecessor == r.ID {
		return ErrInvalid
	}
	if err := validateIdentity(r.Repository, r.Runtime); err != nil {
		return err
	}
	if !optional(r.Subject.Task, 256) || !optional(r.Subject.Topic, 256) || r.Privacy.Version != "topic-redaction/v1" || !optional(r.Privacy.Reason, 256) {
		return ErrInvalid
	}
	switch r.Privacy.State {
	case "omitted":
		if r.Subject.Topic != nil || r.Privacy.Reason == nil {
			return ErrInvalid
		}
	case "redacted":
		if r.Subject.Topic == nil {
			return ErrInvalid
		}
	default:
		return ErrInvalid
	}
	if r.Execution != nil {
		if r.Subject.Task == nil || *r.Subject.Task != r.Execution.Snapshot.Task {
			return ErrInvalid
		}
		if err := r.Execution.Snapshot.validate(); err != nil {
			return err
		}
	}
	if r.Scope != nil {
		s := r.Scope
		if s.Reads == nil || s.Writes == nil || s.Contracts == nil || len(s.Reads) > 128 || len(s.Writes) > 128 || len(s.Contracts) > 128 {
			return ErrInvalid
		}
		for _, v := range s.Reads {
			if !relative(v.Path) || v.Commit != nil && !oid(*v.Commit) {
				return ErrInvalid
			}
		}
		for _, v := range s.Writes {
			if !relative(v) {
				return ErrInvalid
			}
		}
		for _, v := range s.Contracts {
			if !text(v, 256) {
				return ErrInvalid
			}
		}
	}
	return nil
}

// SamePayload compares validated immutable records, including optional presence,
// timestamps and array order. It does not serialize, hash, or retain raw topics.
func SamePayload(a, b Record) (bool, error) {
	if err := a.Validate(); err != nil {
		return false, err
	}
	if err := b.Validate(); err != nil {
		return false, err
	}
	return reflect.DeepEqual(a, b), nil
}

// Retransmission is advisory and has no persistence or exactly-once side effect.
func Retransmission(a, b Record) (string, error) {
	equal, err := SamePayload(a, b)
	if err != nil {
		return "", err
	}
	if a.ID != b.ID {
		return "distinct", nil
	}
	if equal {
		return "duplicate", nil
	}
	return "conflict", nil
}

type Assessment struct {
	Provenance string
	Current    string
	Reason     string
}

// Assess never awards bound-at-write from a payload's self-assertion. A future
// trusted writer adapter must establish that provenance under its mutation lock.
func Assess(r Record, repo Repository, current *Snapshot, status string) Assessment {
	a := Assessment{"unknown", "unavailable", "unverified-owner-provenance"}
	if err := r.Validate(); err != nil {
		a.Reason = "invalid-or-unsupported"
		return a
	}
	if r.Execution == nil {
		return Assessment{"declared-unbound", "unavailable", "no-execution-reference"}
	}
	if !reflect.DeepEqual(r.Repository.identity(), repo.identity()) {
		a.Reason = "repository-identity-unverified-or-different"
		return a
	}
	if current == nil || current.validate() != nil {
		a.Reason = "current-execution-unavailable"
		return a
	}
	if !reflect.DeepEqual(r.Execution.Snapshot, *current) {
		a.Current = "different-generation"
		return a
	}
	if status == taskruntime.StatusDone || status == taskruntime.StatusAborted {
		a.Current = "terminal"
		return a
	}
	if status == taskruntime.StatusActive || status == taskruntime.StatusFinishing || status == taskruntime.StatusBlocked {
		a.Current = "matching"
	}
	return a
}
func (r Repository) identity() Repository { r.CatalogAlias = nil; return r }

func validateIdentity(repo Repository, runtime Runtime) error {
	if repo.Scheme != "repository-id/v1" {
		return ErrUnsupported
	}
	if !text(repo.ID, 256) || !absolute(repo.CommonDir) || !optional(repo.CatalogAlias, 256) || !text(runtime.Name, 64) {
		return ErrInvalid
	}
	switch runtime.Provenance {
	case "native":
		if runtime.SessionID == nil || !text(*runtime.SessionID, 256) || runtime.UnavailableReason != nil {
			return ErrInvalid
		}
	case "unavailable":
		if runtime.SessionID != nil || runtime.UnavailableReason == nil || !text(*runtime.UnavailableReason, 256) {
			return ErrInvalid
		}
	default:
		return ErrInvalid
	}
	return nil
}
