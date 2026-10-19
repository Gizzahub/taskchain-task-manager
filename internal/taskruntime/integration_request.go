package taskruntime

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Gizzahub/taskchain-task-manager/internal/integrationqueue"
)

type requestBinding struct {
	spec                    integrationqueue.Spec
	executionID, generation string
}

// The native identifiers are deterministic over immutable request identity, not
// worker PID or queue revision. Restart must address the same native execution.
func newRequestBinding(spec integrationqueue.Spec) requestBinding {
	data, _ := json.Marshal(spec)
	generation, _ := json.Marshal(spec.Generation)
	return requestBinding{spec: spec, executionID: fmt.Sprintf("tm-%x", sha256.Sum256(data)), generation: fmt.Sprintf("tmg-%x", sha256.Sum256(generation))}
}

func bindRequestReceipt(r *Receipt, b *requestBinding) {
	if b == nil {
		return
	}
	r.RequestID, r.NativeExecutionID, r.NativeGeneration = b.spec.ID, b.executionID, b.generation
	r.ExecutionStartedAt, r.ExecutionType, r.Source = &b.spec.Generation.StartedAt, b.spec.Generation.Type, b.spec.Source
}

func (s *Service) recordRequestRevalidation(store stateStore, e Execution, b *requestBinding, diagnostics []CommandDiagnostic) error {
	refused := Receipt{Task: e.Task, Operation: "finish", Status: StatusBlocked, Purpose: EffectivePurpose(e.Purpose), Owner: e.Owner, Branch: e.Branch, Worktree: e.Worktree, CreatedAt: s.now().UTC(), TaskHead: b.spec.ExpectedTaskSHA, SourceHeadBefore: b.spec.ExpectedSourceSHA, Reason: "integration request requires revalidation: captured heads changed", RevalidationRequired: true, Diagnostics: diagnostics, ToolVersion: toolVersion(), ToolRevision: toolRevision()}
	bindRequestReceipt(&refused, b)
	return store.appendReceipt(refused)
}

func requestExecution(spec integrationqueue.Spec) Execution {
	g := spec.Generation
	return Execution{Task: g.Task, Type: g.Type, Purpose: Purpose(g.Purpose), Source: g.Source, Branch: g.Branch, Worktree: g.Worktree,
		Owner: Owner{Actor: g.Owner.Actor, Host: g.Owner.Host, Kind: g.Owner.Kind}, StartedAt: g.StartedAt}
}

// Called while the existing runtime mutation lock is held. The queue lock is
// never nested around runtime or provider work.
func (s *Service) validateRequestBinding(ctx context.Context, store stateStore, e Execution, b requestBinding) error {
	spec := b.spec
	if !containsExecutionGeneration([]Execution{e}, requestExecution(spec)) || EffectivePurpose(e.Purpose) != PurposeDelivery || ValidatePurposeBranch(e.Purpose, e.Branch) != nil {
		return fmt.Errorf("integration request execution generation or purpose changed")
	}
	cfg, err := s.config()
	if err != nil {
		return err
	}
	if cfg.IntegrationNetworkPolicy == NetworkNoFetch {
		return fmt.Errorf("request execution requires fresh remote proof; no-fetch request execution is unsupported")
	}
	root, err := filepath.EvalSymlinks(s.root)
	if err != nil {
		return err
	}
	common, err := filepath.EvalSymlinks(filepath.Join(store.dir, "..", "..", ".."))
	if err != nil {
		return err
	}
	if root != spec.RepositoryPath || common != spec.CommonDir || cfg.RepositoryID != spec.RepositoryID || spec.Target != integrationTarget(e) || spec.GateProfile != "repository-default" {
		return fmt.Errorf("integration request repository, target or gate profile changed or unsupported")
	}
	owner, err := s.owner(ctx)
	if err != nil || owner != e.Owner {
		return fmt.Errorf("only the recorded request owner may execute integration")
	}
	return nil
}

type boundIntegrationProvider interface {
	RunBound(context.Context, string, string, bool, string, string, *requestBinding) (CommandResult, error)
}

type requestCapableProvider interface {
	DiagnoseRequest(context.Context, string) ([]CommandDiagnostic, error)
}

func (p gzGitProvider) DiagnoseRequest(ctx context.Context, dir string) ([]CommandDiagnostic, error) {
	var diagnostics []CommandDiagnostic
	for _, op := range []struct {
		name  string
		flags []string
	}{
		{"run", []string{"--expect-source", "--expect-target", "--execution-id", "--generation", "--owner-actor", "--owner-host", "--owner-kind"}},
		{"receipt", []string{"--execution-id", "--generation", "--json"}},
		{"cleanup", []string{"--execution-id", "--generation", "--json"}},
	} {
		result, err := p.runner.Run(ctx, CommandRequest{Command: providerGZGit, Args: []string{"integrate", op.name, "--help"}, WorkDir: dir})
		diagnostics = append(diagnostics, diagnostic(result))
		if err != nil || result.ExitCode != 0 {
			return diagnostics, fmt.Errorf("request provider %s capability probe failed", op.name)
		}
		for _, flag := range op.flags {
			if !declaresFlag(result.Stdout+"\n"+result.Stderr, op.name, flag) {
				return diagnostics, fmt.Errorf("request provider %s does not declare %s", op.name, flag)
			}
		}
	}
	return diagnostics, nil
}

func (p gzGitProvider) RunBound(ctx context.Context, dir, target string, noFetch bool, taskSHA, targetSHA string, b *requestBinding) (CommandResult, error) {
	args := append(integrateArgs("run", target, noFetch), "--expect-source", taskSHA, "--expect-target", targetSHA)
	if b != nil {
		o := b.spec.Owner
		args = append(args, "--execution-id", b.executionID, "--generation", b.generation, "--owner-actor", o.Actor, "--owner-host", o.Host, "--owner-kind", o.Kind)
	}
	req := CommandRequest{Command: providerGZGit, Args: args, WorkDir: dir}
	if b != nil {
		req.Env = requestOwnerEnv(b.spec.Owner)
		ctx = context.WithoutCancel(ctx)
	}
	return p.runner.Run(ctx, req)
}

func requestOwnerEnv(o integrationqueue.Owner) map[string]string {
	env := map[string]string{"GIT_WORK_HOST": o.Host, "GZ_GIT_TASK_ACTOR": "", "GIT_WORK_ACTOR": ""}
	if o.Kind == "agent" {
		env["GZ_GIT_TASK_ACTOR"] = o.Actor
	} else {
		env["GIT_WORK_ACTOR"] = o.Actor
	}
	return env
}

// RequestClaimLiveness refuses unknown/foreign processes; time alone is not a
// lease. It uses the same positive local process proof as FINISHING recovery.
func (s *Service) RequestClaimLiveness(ctx context.Context, c integrationqueue.Claim) integrationqueue.ClaimLiveness {
	switch s.claimLiveness(ctx, Receipt{FinishPID: c.PID, FinishHost: c.Host, CreatedAt: c.ClaimedAt}) {
	case claimAlive:
		return integrationqueue.ClaimLive
	case claimDead:
		return integrationqueue.ClaimDead
	default:
		return integrationqueue.ClaimUnknown
	}
}

func (s *Service) NewRequestClaim(ctx context.Context, r integrationqueue.Request) (integrationqueue.Claim, error) {
	owner, err := s.owner(ctx)
	if err != nil {
		return integrationqueue.Claim{}, err
	}
	want := r.Spec.Owner
	if owner.Actor != want.Actor || owner.Host != want.Host || owner.Kind != want.Kind {
		return integrationqueue.Claim{}, fmt.Errorf("only request owner may execute or resume")
	}
	return integrationqueue.Claim{Owner: want, Generation: r.Spec.Generation, ClaimedAt: s.now().UTC(), PID: os.Getpid(), Host: lockHost()}, nil
}

func requestReceiptMatches(r Receipt, b requestBinding) bool {
	e := requestExecution(b.spec)
	return receiptBindsExecution(e, r) && r.ExecutionStartedAt != nil && r.ExecutionStartedAt.Equal(e.StartedAt) && r.ExecutionType == e.Type && r.Source == e.Source && r.RequestID == b.spec.ID && r.NativeExecutionID == b.executionID && r.NativeGeneration == b.generation && r.TaskHead == b.spec.ExpectedTaskSHA && r.SourceHeadBefore == b.spec.ExpectedSourceSHA
}

func requestDispositionRefusal(e Execution, r Receipt) (Response, bool) {
	if r.RequestID != "" && !r.RequestCancelled && receiptBindsExecution(e, r) && !validTerminalReceipt(e, r) {
		return response(StatusBlocked, e.Task, "request-bound execution requires original integration request disposition", "resume the original request, or cancel its proven pre-mutation refusal"), true
	}
	return Response{}, false
}

// CancelRequest releases only a proven pre-mutation refusal after queue CAS
// has durably cancelled it. Repeating this finalization is safe if interrupted.
// An uncertain running integration is never released by cancellation.
func (s *Service) CancelRequest(ctx context.Context, r integrationqueue.Request) error {
	if r.State != integrationqueue.Cancelled {
		return fmt.Errorf("request is not cancelled")
	}
	store, err := s.store(ctx)
	if err != nil {
		return err
	}
	unlock, err := store.lock()
	if err != nil {
		return err
	}
	defer unlock()
	latest, err := store.latestReceipts()
	if err != nil {
		return err
	}
	prior := latest[r.Spec.Generation.Task]
	b := newRequestBinding(r.Spec)
	if !requestReceiptMatches(prior, b) {
		return nil
	} // queued/admitted never entered runtime
	current, err := store.load()
	if err != nil {
		return err
	}
	e := requestExecution(r.Spec)
	if !containsExecutionGeneration(current, e) {
		return fmt.Errorf("cancelled request execution generation changed")
	}
	if err = s.validateRequestBinding(ctx, store, e, b); err != nil {
		return err
	}
	if prior.RequestCancelled {
		return nil
	}
	if prior.Status != StatusBlocked || !prior.RevalidationRequired {
		return fmt.Errorf("cannot release an uncertain request runtime outcome")
	}
	prior.RequestCancelled = true
	prior.CreatedAt = s.now().UTC()
	prior.Reason = "integration request cancelled after proven pre-mutation refusal"
	return store.appendReceipt(prior)
}

// Execute invokes Finish once. Any uncertain provider result stays running so
// the resource cannot be reused until durable reconciliation establishes facts.
func (s *Service) Execute(ctx context.Context, r integrationqueue.Request) integrationqueue.Outcome {
	b := newRequestBinding(r.Spec)
	res, code := s.finish(ctx, r.Spec.Generation.Task, &b)
	if code == 4 {
		return integrationqueue.Outcome{State: integrationqueue.RevalidationRequired, Reason: res.Reason, ExitCode: 4}
	}
	if res.Receipt != nil && requestReceiptMatches(*res.Receipt, b) && code == 0 {
		return s.reconcileRequest(ctx, r, false)
	}
	if !res.providerAttempted {
		return integrationqueue.Outcome{State: integrationqueue.Failed, Reason: res.Reason, ExitCode: codeOrUnknown(code)}
	}
	// A durable FINISHING claim may precede a lost invocation result. Never
	// retry Run on that ambiguity, including a failed runtime outcome write.
	return integrationqueue.Outcome{State: integrationqueue.Running, Reason: res.Reason, ExitCode: codeOrUnknown(code)}
}

func codeOrUnknown(code int) int {
	if code == 0 {
		return 2
	}
	return code
}

func (s *Service) Reconcile(ctx context.Context, r integrationqueue.Request) integrationqueue.Outcome {
	return s.reconcileRequest(ctx, r, true)
}

type nativeRequestReceipt struct {
	Schema          int    `json:"schema_version"`
	ExecutionID     string `json:"execution_id"`
	Generation      string `json:"generation"`
	Owner           Owner  `json:"owner"`
	Common          string `json:"repository_common_dir"`
	Branch          string `json:"task_branch"`
	Worktree        string `json:"task_worktree"`
	TaskSHA         string `json:"task_sha"`
	Target          string `json:"target_ref"`
	TargetBefore    string `json:"target_before_sha"`
	SourcePushed    bool   `json:"source_pushed"`
	WorktreeRemoved bool   `json:"worktree_removed"`
	LocalRemoved    bool   `json:"local_branch_removed"`
	RemoteRemoved   bool   `json:"remote_branch_removed"`
	Phase           string `json:"phase"`
}

func (s *Service) reconcileRequest(ctx context.Context, r integrationqueue.Request, allowCleanup bool) (out integrationqueue.Outcome) {
	unknown := func(reason string, code int) integrationqueue.Outcome {
		return integrationqueue.Outcome{State: integrationqueue.Running, Reason: reason, ExitCode: codeOrUnknown(code)}
	}
	b, e := newRequestBinding(r.Spec), requestExecution(r.Spec)
	store, err := s.store(ctx)
	if err != nil {
		return unknown(err.Error(), 2)
	}
	unlock, err := store.lock()
	if err != nil {
		return unknown(err.Error(), 2)
	}
	locked := true
	defer func() {
		if locked {
			unlock()
		}
	}()
	current, err := store.load()
	if err != nil {
		return unknown(err.Error(), 2)
	}
	if !containsExecutionGeneration(current, e) {
		return unknown("request execution generation changed", 2)
	}
	if err = s.validateRequestBinding(ctx, store, e, b); err != nil {
		return unknown(err.Error(), 2)
	}
	receipts, err := store.latestReceipts()
	if err != nil {
		return unknown(err.Error(), 2)
	}
	prior := receipts[e.Task]
	if prior.RequestCancelled {
		return unknown("request runtime binding was cancelled", 2)
	}
	if !requestReceiptMatches(prior, b) {
		return unknown("no exact request-bound runtime receipt; outcome unknown", 2)
	}
	if prior.Status == StatusBlocked && prior.RevalidationRequired {
		return integrationqueue.Outcome{State: integrationqueue.RevalidationRequired, Reason: prior.Reason, ExitCode: 4}
	}
	if prior.Status == StatusFinishing && s.claimLiveness(ctx, prior) != claimDead {
		return unknown("live or unknown runtime FINISHING owner", 2)
	}
	// Native queries and cleanup run outside the runtime lock. Install a fresh
	// CAS claim before cleanup so other lifecycle writers cannot race recovery.
	claim := prior
	claim.Status, claim.Operation, claim.CreatedAt = StatusFinishing, "finish", s.now().UTC()
	claim.FinishPID, claim.FinishHost = os.Getpid(), lockHost()
	if err = store.appendReceipt(claim); err != nil {
		return unknown(err.Error(), 2)
	}
	unlock()
	locked = false
	diagnostics := append([]CommandDiagnostic(nil), prior.Diagnostics...)
	// Preserve real query/cleanup diagnostics even on partial or unknown exits.
	// A failed post-provider write must never trigger another integration.
	defer func() {
		if out.State != integrationqueue.Running {
			return
		}
		if !locked {
			var lockErr error
			unlock, lockErr = store.lockWait(context.WithoutCancel(ctx), 10*time.Second, 20*time.Millisecond)
			if lockErr != nil {
				out.Reason += "; preserve diagnostics failed: " + lockErr.Error()
				return
			}
			locked = true
		}
		current, loadErr := store.load()
		latest, receiptErr := store.latestReceipts()
		if loadErr != nil || receiptErr != nil || !containsExecutionGeneration(current, e) || !sameFinishClaim(latest[e.Task], claim) {
			out.Reason += "; runtime outcome CAS changed; native evidence retained"
			return
		}
		recorded := claim
		recorded.Status = StatusBlocked
		recorded.Reason = out.Reason
		recorded.Diagnostics = diagnostics
		recorded.FinishPID = 0
		recorded.FinishHost = ""
		if writeErr := store.appendReceipt(recorded); writeErr != nil {
			out.Reason += "; preserve diagnostics failed: " + writeErr.Error()
		}
	}()
	args := []string{"integrate", "receipt", "--execution-id", b.executionID, "--generation", b.generation, "--json"}
	result, err := s.command(ctx, s.root, providerGZGit, args...)
	diagnostics = append(diagnostics, diagnostic(result))
	var native nativeRequestReceipt
	if err != nil || result.ExitCode != 0 || json.Unmarshal([]byte(result.Stdout), &native) != nil {
		return unknown("native receipt unavailable; integration outcome remains unknown", result.ExitCode)
	}
	identityMatches := func(n nativeRequestReceipt) bool {
		return n.Schema == 2 && n.ExecutionID == b.executionID && n.Generation == b.generation && n.Owner == e.Owner && n.Common == r.Spec.CommonDir && n.Branch == e.Branch && n.Worktree == e.Worktree && n.TaskSHA == r.Spec.ExpectedTaskSHA && n.Target == "refs/heads/"+e.Source && n.TargetBefore == r.Spec.ExpectedSourceSHA
	}
	if !identityMatches(native) {
		return unknown("native receipt identity, generation or pushed outcome mismatch", 2)
	}
	if allowCleanup {
		result, err = s.runner.Run(context.WithoutCancel(ctx), CommandRequest{Command: providerGZGit, Args: []string{"integrate", "cleanup", "--execution-id", b.executionID, "--generation", b.generation, "--json"}, WorkDir: s.root, Env: requestOwnerEnv(r.Spec.Owner)})
		diagnostics = append(diagnostics, diagnostic(result))
		if err != nil || result.ExitCode != 0 {
			return unknown("native cleanup incomplete; integration was not rerun", result.ExitCode)
		}
		result, err = s.command(ctx, s.root, providerGZGit, args...)
		diagnostics = append(diagnostics, diagnostic(result))
		if err != nil || result.ExitCode != 0 || json.Unmarshal([]byte(result.Stdout), &native) != nil || !identityMatches(native) {
			return unknown("post-cleanup native receipt verification failed", 2)
		}
	}
	if !native.SourcePushed || native.Phase != "complete" || !native.WorktreeRemoved || !native.LocalRemoved || !native.RemoteRemoved {
		return unknown("native completion is not proven", 3)
	}
	// Independently verify the live source contains the captured task and all
	// residuals are absent; receipt booleans or success text alone cannot suffice.
	remote, err := s.command(ctx, s.root, "git", "ls-remote", "--exit-code", "--heads", "origin", "refs/heads/"+e.Source)
	diagnostics = append(diagnostics, diagnostic(remote))
	fields := strings.Fields(remote.Stdout)
	if err != nil || remote.ExitCode != 0 || len(fields) != 2 || !validOID(fields[0]) || fields[1] != "refs/heads/"+e.Source {
		return unknown("fresh remote source identity unavailable", 2)
	}
	freshSHA := fields[0]
	// Fetch the immutable advertised object, never a shared FETCH_HEAD or a
	// mutable tracking ref that another concurrent fetch can overwrite.
	fetched, err := s.command(ctx, s.root, "git", "fetch", "--no-tags", "--no-write-fetch-head", "origin", freshSHA)
	diagnostics = append(diagnostics, diagnostic(fetched))
	if err != nil || fetched.ExitCode != 0 {
		return unknown("fresh source object proof failed", 2)
	}
	contains, err := s.command(ctx, s.root, "git", "merge-base", "--is-ancestor", r.Spec.ExpectedTaskSHA, freshSHA)
	diagnostics = append(diagnostics, diagnostic(contains))
	if err != nil || contains.ExitCode != 0 {
		return unknown("fresh source does not prove captured task integration", 2)
	}
	verified, evidenceErr := s.receiptWithSourceProof(ctx, e, StatusDone, "request integration and cleanup verified", diagnostics, r.Spec.ExpectedSourceSHA, prior.BaseHead, r.Spec.ExpectedTaskSHA, false, freshSHA)
	bindRequestReceipt(&verified, &b)
	verified.SourceHead = freshSHA
	if evidenceErr != nil {
		return unknown(evidenceErr.Error(), 3)
	}
	if err = validateIntegratedFinish(e.Task, &e, &verified); err != nil {
		return unknown(err.Error(), 3)
	}
	unlock, err = store.lock()
	if err != nil {
		return unknown(err.Error(), 3)
	}
	locked = true
	current, err = store.load()
	if err != nil {
		return unknown(err.Error(), 3)
	}
	receipts, err = store.latestReceipts()
	if err != nil {
		return unknown(err.Error(), 3)
	}
	if !containsExecutionGeneration(current, e) || !sameFinishClaim(receipts[e.Task], claim) {
		return unknown("request reconciliation lost runtime generation/claim CAS; actual result retained in native receipt", 3)
	}
	if err = store.appendReceipt(verified); err != nil {
		return unknown(err.Error(), 3)
	}
	return integrationqueue.Outcome{State: integrationqueue.Succeeded, Reason: verified.Reason, ExitCode: 0}
}
