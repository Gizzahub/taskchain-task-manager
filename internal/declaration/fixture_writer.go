package declaration

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"time"

	"github.com/Gizzahub/taskchain-task-manager/internal/taskruntime"
)

var ErrFixtureConflict = errors.New("fixture immutable identity conflict")
var ErrFixtureStorage = errors.New("fixture declaration persistence failed")
var ErrFixtureDurability = errors.New("fixture declaration durability unknown")

// FixtureWriter is an internal synthetic-library foundation. No command or
// production caller constructs it. Topics are omitted before any serialization.
type FixtureWriter struct {
	sandbox string
	runtime *taskruntime.Service
	fault   func(string) error // private deterministic filesystem fault seam
}

func NewFixtureWriter(sandbox string, runtime *taskruntime.Service) (*FixtureWriter, error) {
	r, err := taskruntime.OpenFixtureSandbox(sandbox)
	if err != nil {
		return nil, err
	}
	if err := r.Close(); err != nil {
		return nil, ErrFixtureStorage
	}
	if runtime == nil {
		return nil, ErrInvalid
	}
	return &FixtureWriter{sandbox: sandbox, runtime: runtime}, nil
}

type FixtureResult struct {
	Durable     bool
	Disposition string
	Audit       string
	Stored      FixtureStored
}

// FixtureStored is process-local writer provenance, not an importable authority.
// A decoded record or arbitrary metadata cannot construct a trusted value.
type FixtureStored struct {
	record  Record
	trusted bool
}
type FixtureView struct{ DeclarationID, Provenance, Current, Reason string }

// View deliberately excludes topic, absolute paths, owner and session identity.
// Historical binding survives current generation changes without granting rights.
func (s FixtureStored) View(repo Repository, current *Snapshot, status string) FixtureView {
	if !s.trusted {
		return FixtureView{Provenance: "unknown", Current: "unavailable", Reason: "unverified-writer"}
	}
	a := Assess(s.record, repo, current, status)
	return FixtureView{s.record.ID, "bound-at-write", a.Current, a.Reason}
}

// Write persists a bound fixture declaration and an immutable link under the
// existing runtime mutation lock. Consumer delivery occurs after lock release.
// Its failure never rolls back persistence or reruns an execution/provider.
func (w *FixtureWriter) Write(ctx context.Context, record Record, link Link, consume func(Link) error) (FixtureResult, error) {
	out := FixtureResult{Audit: "unconfirmed"}
	if record.Subject.Topic != nil || record.Privacy.State != "omitted" {
		return out, ErrInvalid
	}
	if err := record.Validate(); err != nil {
		return out, err
	}
	if err := link.Validate(); err != nil {
		return out, err
	}
	if record.Execution == nil || link.DeclarationID != record.ID || !reflect.DeepEqual(link.Repository, record.Repository) || !reflect.DeepEqual(link.Runtime, record.Runtime) {
		return out, ErrInvalid
	}
	// Freeze caller-owned pointers/slices before entering the mutation boundary.
	b, err := json.Marshal(record)
	if err != nil {
		return out, ErrInvalid
	}
	record, err = Decode(b)
	if err != nil {
		return out, err
	}
	lb, err := json.Marshal(link)
	if err != nil {
		return out, ErrInvalid
	}
	link, err = DecodeLink(lb)
	if err != nil {
		return out, err
	}
	x := record.Execution.Snapshot
	started, _ := time.Parse(time.RFC3339Nano, x.StartedAt)
	captured := taskruntime.Execution{Task: x.Task, Type: x.Type, Purpose: taskruntime.Purpose(x.Purpose), Source: x.Source, Owner: x.Owner, Branch: x.Branch, Worktree: x.Worktree, StartedAt: started}
	err = w.runtime.WithFixtureExecution(ctx, w.sandbox, record.Repository.ID, record.Repository.CommonDir, captured, func() error {
		r, e := w.collection(record.Repository.CommonDir)
		if e != nil {
			return e
		}
		defer r.Close()
		out.Disposition, e = w.persist(r, "declaration", record.ID, b, func(old []byte) error {
			v, e := Decode(old)
			if e != nil {
				return ErrFixtureStorage
			}
			same, e := SamePayload(v, record)
			if e != nil {
				return e
			}
			if !same {
				return ErrFixtureConflict
			}
			return nil
		})
		if e != nil {
			return e
		}
		if e = w.verifyCollection(record.Repository.CommonDir, r); e != nil {
			return e
		}
		out.Durable = true
		out.Stored = FixtureStored{record: record, trusted: true}
		_, e = w.persist(r, "link", link.EventID, lb, func(old []byte) error {
			v, e := DecodeLink(old)
			if e != nil {
				return ErrFixtureStorage
			}
			state, e := LinkRetransmission(v, link)
			if e != nil {
				return e
			}
			if state != "duplicate" {
				return ErrFixtureConflict
			}
			return nil
		})
		if e != nil {
			return e
		}
		if e = w.verifyCollection(record.Repository.CommonDir, r); e != nil {
			out.Durable = false
			out.Stored = FixtureStored{}
			return e
		}
		return nil
	})
	if err != nil {
		return out, err
	}
	if consume != nil {
		if err := consume(link); err != nil {
			return out, errors.New("fixture audit delivery unconfirmed")
		}
		out.Audit = "confirmed"
	}
	return out, nil
}
