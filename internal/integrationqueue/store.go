package integrationqueue

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"syscall"
	"time"
)

type Store struct{ root, statePath, lockPath, markerPath string }
type persisted struct {
	NextSequence uint64    `json:"nextSequence"`
	Requests     []Request `json:"requests"`
}

func New(root string) (*Store, error) {
	if !filepath.IsAbs(root) {
		return nil, errors.New("integration queue root must be absolute")
	}
	if err := os.MkdirAll(root, 0755); err != nil {
		return nil, err
	}
	s := &Store{root: root, statePath: filepath.Join(root, "integration-requests.json"), lockPath: filepath.Join(root, "integration-requests.lock"), markerPath: filepath.Join(root, "integration-requests.initialized")}
	f, err := os.OpenFile(s.lockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		_ = f.Close()
		return nil, err
	}
	defer unlock(f)
	_, markerErr := os.Stat(s.markerPath)
	_, stateErr := os.Stat(s.statePath)
	if markerErr == nil && errors.Is(stateErr, os.ErrNotExist) {
		return nil, errors.New("integration queue state is missing after initialization")
	}
	if markerErr != nil && !errors.Is(markerErr, os.ErrNotExist) {
		return nil, markerErr
	}
	if errors.Is(markerErr, os.ErrNotExist) && errors.Is(stateErr, os.ErrNotExist) {
		if err = s.write(persisted{}); err != nil {
			return nil, err
		}
		if err = s.writeMarker(); err != nil {
			return nil, err
		}
	} else if errors.Is(markerErr, os.ErrNotExist) && stateErr == nil {
		if _, err = s.read(); err != nil {
			return nil, err
		}
		if err = s.writeMarker(); err != nil {
			return nil, err
		}
	} else if stateErr != nil {
		return nil, stateErr
	}
	return s, nil
}
func (s *Store) writeMarker() error {
	if err := os.WriteFile(s.markerPath, []byte("v1\n"), 0600); err != nil {
		return err
	}
	f, err := os.OpenFile(s.markerPath, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	err = f.Sync()
	_ = f.Close()
	return err
}

// Open opens an existing coordinator without creating directories, locks, or
// state. It validates the durable state before returning it for query/cancel.
func Open(root string) (*Store, error) {
	if !filepath.IsAbs(root) {
		return nil, errors.New("integration queue root must be absolute")
	}
	s := &Store{root: root, statePath: filepath.Join(root, "integration-requests.json"), lockPath: filepath.Join(root, "integration-requests.lock"), markerPath: filepath.Join(root, "integration-requests.initialized")}
	if _, err := os.Stat(s.markerPath); err != nil {
		return nil, fmt.Errorf("open integration queue marker: %w", err)
	}
	if _, err := os.Stat(s.lockPath); err != nil {
		return nil, fmt.Errorf("open integration queue lock: %w", err)
	}
	if _, err := os.Stat(s.statePath); err != nil {
		return nil, fmt.Errorf("open integration queue state: %w", err)
	}
	f, err := s.lock()
	if err != nil {
		return nil, err
	}
	defer unlock(f)
	if _, err = s.read(); err != nil {
		return nil, err
	}
	return s, nil
}
func (s *Store) lock() (*os.File, error) {
	f, e := os.OpenFile(s.lockPath, os.O_RDWR, 0600)
	if e != nil {
		return nil, fmt.Errorf("open integration queue lock: %w", e)
	}
	if e = syscall.Flock(int(f.Fd()), syscall.LOCK_EX); e != nil {
		_ = f.Close()
		return nil, e
	}
	return f, nil
}
func unlock(f *os.File) { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN); _ = f.Close() }
func (s *Store) read() (persisted, error) {
	b, e := os.ReadFile(s.statePath)
	if e != nil {
		return persisted{}, fmt.Errorf("read integration queue state: %w", e)
	}
	var p persisted
	if e = json.Unmarshal(b, &p); e != nil {
		return p, fmt.Errorf("decode integration queue state: %w", e)
	}
	if e = validatePersisted(p); e != nil {
		return p, fmt.Errorf("validate integration queue state: %w", e)
	}
	return p, nil
}
func (s *Store) write(p persisted) error {
	if err := validatePersisted(p); err != nil {
		return fmt.Errorf("refuse invalid integration queue publication: %w", err)
	}
	b, e := json.MarshalIndent(p, "", "  ")
	if e != nil {
		return e
	}
	tmp := filepath.Join(s.root, fmt.Sprintf(".integration-requests-%d.tmp", time.Now().UnixNano()))
	if e = os.WriteFile(tmp, append(b, '\n'), 0600); e != nil {
		return e
	}
	f, e := os.OpenFile(tmp, os.O_RDWR, 0)
	if e == nil {
		e = f.Sync()
		_ = f.Close()
	}
	if e != nil {
		_ = os.Remove(tmp)
		return e
	}
	if e = os.Rename(tmp, s.statePath); e != nil {
		_ = os.Remove(tmp)
		return e
	}
	d, e := os.Open(s.root)
	if e != nil {
		return e
	}
	defer d.Close()
	return d.Sync()
}
func validSpec(x Spec) error {
	if x.ID == "" || x.IdempotencyKey == "" || x.RepositoryID == "" || !validOID(x.ExpectedTaskSHA) || !validOID(x.ExpectedSourceSHA) || x.Source == "" || x.Target == "" || x.GateProfile == "" || x.Resource == "" {
		return errors.New("request immutable identity is incomplete")
	}
	if !filepath.IsAbs(x.RepositoryPath) || !filepath.IsAbs(x.CommonDir) {
		return errors.New("repository and common paths must be absolute")
	}
	g := x.Generation
	if g.Task == "" || !validType(g.Type) || g.Purpose != "delivery" || g.Source == "" || !strings.HasPrefix(g.Branch, "dev/") || !filepath.IsAbs(g.Worktree) || g.StartedAt.IsZero() || !validOwner(x.Owner) || !validOwner(g.Owner) || x.Owner != g.Owner || x.Source != g.Source {
		return errors.New("request owner or generation binding is incomplete")
	}
	return nil
}
func validOwner(o Owner) bool { return o.Actor != "" && o.Host != "" && o.Kind != "" }
func validOID(v string) bool {
	if len(v) != 40 && len(v) != 64 {
		return false
	}
	for _, r := range v {
		if !strings.ContainsRune("0123456789abcdef", r) {
			return false
		}
	}
	return true
}
func validType(v string) bool {
	switch v {
	case "feat", "fix", "refactor", "docs", "test", "chore", "perf":
		return true
	}
	return false
}
func sameClaim(a, b Claim) bool {
	a.ClaimedAt = a.ClaimedAt.UTC()
	b.ClaimedAt = b.ClaimedAt.UTC()
	a.Generation.StartedAt = a.Generation.StartedAt.UTC()
	b.Generation.StartedAt = b.Generation.StartedAt.UTC()
	return reflect.DeepEqual(a, b)
}
func sameGeneration(a, b Generation) bool {
	a.StartedAt = a.StartedAt.UTC()
	b.StartedAt = b.StartedAt.UTC()
	return a == b
}
func validClaim(c Claim, s Spec) bool {
	return c.Owner == s.Owner && sameGeneration(c.Generation, s.Generation) && c.PID > 0 && c.Host != "" && !c.ClaimedAt.IsZero()
}
func validRequestClaim(r Request) bool {
	wantsClaim := r.State == Admitted || r.State == Running || r.State == RevalidationRequired
	if wantsClaim {
		return r.Claim != nil && validClaim(*r.Claim, r.Spec)
	}
	return r.Claim == nil
}
func validatePersisted(p persisted) error {
	var max uint64
	seen, keys, sequences := map[string]bool{}, map[string]bool{}, map[uint64]bool{}
	activeResources := map[string]bool{}
	for _, r := range p.Requests {
		if validSpec(r.Spec) != nil || !validState(r.State) || r.Sequence == 0 || r.Revision == 0 || r.CreatedAt.IsZero() || r.UpdatedAt.IsZero() || seen[r.Spec.ID] || keys[r.Spec.IdempotencyKey] || sequences[r.Sequence] || !validRequestClaim(r) {
			return errors.New("invalid persisted request")
		}
		if r.State == Admitted || r.State == Running {
			if activeResources[r.Spec.Resource] {
				return errors.New("multiple active requests share a resource")
			}
			activeResources[r.Spec.Resource] = true
		}
		seen[r.Spec.ID] = true
		keys[r.Spec.IdempotencyKey] = true
		sequences[r.Sequence] = true
		if r.Sequence > max {
			max = r.Sequence
		}
	}
	if p.NextSequence < max {
		return errors.New("next sequence regressed")
	}
	return nil
}
func sameSpec(a, b Spec) bool {
	a.ID = ""
	b.ID = ""
	a.Generation.StartedAt = a.Generation.StartedAt.UTC()
	b.Generation.StartedAt = b.Generation.StartedAt.UTC()
	return reflect.DeepEqual(a, b)
}
func (s *Store) Enqueue(x Spec) (Request, error) {
	if e := validSpec(x); e != nil {
		return Request{}, e
	}
	f, e := s.lock()
	if e != nil {
		return Request{}, e
	}
	defer unlock(f)
	p, e := s.read()
	if e != nil {
		return Request{}, e
	}
	var idr, keyr *Request
	for i := range p.Requests {
		r := &p.Requests[i]
		if r.Spec.ID == x.ID {
			idr = r
		}
		if r.Spec.IdempotencyKey == x.IdempotencyKey {
			keyr = r
		}
	}
	if idr != nil || keyr != nil {
		if idr == nil && keyr != nil && sameSpec(keyr.Spec, x) {
			return *keyr, nil
		}
		if idr != nil && keyr != nil && idr.Spec.ID == keyr.Spec.ID && sameSpec(idr.Spec, x) {
			return *idr, nil
		}
		return Request{}, errors.New("request id or idempotency key conflicts with immutable request")
	}
	p.NextSequence++
	now := time.Now().UTC()
	r := Request{Spec: x, State: Queued, Sequence: p.NextSequence, Revision: 1, CreatedAt: now, UpdatedAt: now}
	p.Requests = append(p.Requests, r)
	if e = s.write(p); e != nil {
		return Request{}, e
	}
	return r, nil
}
func (s *Store) Get(id string) (View, error) {
	f, e := s.lock()
	if e != nil {
		return View{}, e
	}
	defer unlock(f)
	p, e := s.read()
	if e != nil {
		return View{}, e
	}
	for _, r := range p.Requests {
		if r.Spec.ID == id {
			return makeView(p, r), nil
		}
	}
	return View{}, errors.New("integration request not found")
}
func (s *Store) List() ([]View, error) {
	f, e := s.lock()
	if e != nil {
		return nil, e
	}
	defer unlock(f)
	p, e := s.read()
	if e != nil {
		return nil, e
	}
	sort.Slice(p.Requests, func(i, j int) bool { return p.Requests[i].Sequence < p.Requests[j].Sequence })
	v := make([]View, len(p.Requests))
	for i, r := range p.Requests {
		v[i] = makeView(p, r)
	}
	return v, nil
}
func makeView(p persisted, r Request) View {
	v := View{Request: r, ProviderPosition: "UNKNOWN", ProviderWaitReason: "UNKNOWN"}
	if r.State == Queued {
		n := 0
		for _, x := range p.Requests {
			if x.Spec.Resource == r.Spec.Resource && x.State == Queued && x.Sequence < r.Sequence {
				n++
			}
		}
		pos := n + 1
		v.CoordinatorPosition = &pos
		if n > 0 {
			v.WaitReason = "waiting for earlier local request"
		}
	}
	if r.State == Queued || r.State == RevalidationRequired {
		for _, x := range p.Requests {
			if x.Spec.ID != r.Spec.ID && x.Spec.Resource == r.Spec.Resource && (x.State == Admitted || x.State == Running) {
				v.WaitReason = "waiting for active local request"
				break
			}
		}
	}
	return v
}
func (s *Store) Cancel(id string, caller Owner, rev uint64) (Request, error) {
	return s.mutate(id, rev, func(r *Request) error {
		if r.Spec.Owner != caller {
			return errors.New("only request owner may cancel")
		}
		if r.State == Running {
			return errors.New("running request cannot be cancelled")
		}
		if r.State == Succeeded || r.State == Failed || r.State == Cancelled {
			return errors.New("request is terminal")
		}
		r.State = Cancelled
		r.Claim = nil
		return nil
	})
}
func (s *Store) Admit(id string, rev uint64, claim Claim) (Request, error) {
	return s.mutateWithState(id, rev, func(p persisted, r *Request) error {
		if r.State != Queued {
			return errors.New("request is not queued")
		}
		if !validClaim(claim, r.Spec) {
			return errors.New("claim does not match request owner or generation")
		}
		for _, other := range p.Requests {
			if other.Spec.ID != r.Spec.ID && other.Spec.Resource == r.Spec.Resource && (other.State == Admitted || other.State == Running || (other.State == Queued && other.Sequence < r.Sequence)) {
				return errors.New("resource admission is blocked by an earlier or active request")
			}
		}
		r.State = Admitted
		r.Claim = &claim
		return nil
	})
}
func (s *Store) Mark(id string, rev uint64, expected *Claim, state State, lastError string) (Request, error) {
	return s.mutate(id, rev, func(r *Request) error {
		if r.State == Succeeded || r.State == Failed || r.State == Cancelled || r.Claim == nil || expected == nil || !sameClaim(*r.Claim, *expected) || !validState(state) || !allowedTransition(r.State, state) {
			return errors.New("request state or claim transition is invalid")
		}
		r.State = state
		r.LastError = lastError
		if state == Succeeded || state == Failed || state == Cancelled {
			r.Claim = nil
		}
		return nil
	})
}
func (s *Store) RecoverAdmitted(id string, caller Owner, rev uint64, expected Claim, claim Claim, observation ClaimLiveness) (Request, error) {
	return s.mutate(id, rev, func(r *Request) error {
		if r.State != Admitted || observation != ClaimDead || r.Claim == nil || !sameClaim(*r.Claim, expected) {
			return errors.New("request is not admitted")
		}
		if r.Spec.Owner != caller || !validClaim(claim, r.Spec) {
			return errors.New("recovery owner or generation does not match request")
		}
		r.Claim = &claim
		return nil
	})
}

// ResumeRevalidation returns a request to admitted without changing its FIFO
// sequence. The caller must still prove ownership, generation, and claim CAS.
func (s *Store) ResumeRevalidation(id string, caller Owner, rev uint64, expected Claim) (Request, error) {
	return s.mutateWithState(id, rev, func(p persisted, r *Request) error {
		if r.State != RevalidationRequired || r.Claim == nil || !sameClaim(*r.Claim, expected) || r.Spec.Owner != caller || !sameGeneration(r.Spec.Generation, expected.Generation) {
			return errors.New("revalidation resume does not match request owner, generation, or claim")
		}
		for _, other := range p.Requests {
			if other.Spec.ID != r.Spec.ID && other.Spec.Resource == r.Spec.Resource && (other.State == Admitted || other.State == Running) {
				return errors.New("resource already has an admitted or running request")
			}
		}
		r.State = Admitted
		return nil
	})
}
func validState(s State) bool {
	switch s {
	case Queued, Admitted, RevalidationRequired, Running, Succeeded, Failed, Cancelled:
		return true
	}
	return false
}
func allowedTransition(from, to State) bool {
	switch from {
	case Queued:
		return to == Cancelled || to == RevalidationRequired
	case Admitted:
		return to == Running || to == Failed || to == Cancelled || to == RevalidationRequired
	case Running:
		return to == Succeeded || to == Failed || to == RevalidationRequired
	}
	return false
}
func (s *Store) mutate(id string, rev uint64, fn func(*Request) error) (Request, error) {
	return s.mutateWithState(id, rev, func(_ persisted, r *Request) error { return fn(r) })
}
func (s *Store) mutateWithState(id string, rev uint64, fn func(persisted, *Request) error) (Request, error) {
	f, e := s.lock()
	if e != nil {
		return Request{}, e
	}
	defer unlock(f)
	p, e := s.read()
	if e != nil {
		return Request{}, e
	}
	for i := range p.Requests {
		r := &p.Requests[i]
		if r.Spec.ID != id {
			continue
		}
		if r.Revision != rev {
			return Request{}, errors.New("request revision conflict")
		}
		if e = fn(p, r); e != nil {
			return Request{}, e
		}
		r.Revision++
		r.UpdatedAt = time.Now().UTC()
		if e = s.write(p); e != nil {
			return Request{}, e
		}
		return *r, nil
	}
	return Request{}, errors.New("integration request not found")
}
