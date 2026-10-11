package taskruntime

import (
	"context"
	"fmt"
	"strings"
)

// Doctor checks the declaration, host, provider, ledger, and Worktrunk.
// It does not take the mutation lock and it does not delete one.
func (s *Service) Doctor(ctx context.Context) Response {
	res := response(StatusActive, "", "task runtime dependencies are ready", "ce task run-start <task> --type <type>", "run-start", "run-status", "run-list")
	cfg, err := s.config()
	if err != nil {
		res.Status, res.Reason, res.NextAction = StatusBlocked, err.Error(), "configure "+ConfigFile
		return res
	}
	local, hostErr := loadLocalIdentity()
	var host string
	if hostErr == nil {
		var warning string
		host, warning, hostErr = s.resolveHost(ctx, local)
		if warning != "" {
			res.Warnings = append(res.Warnings, warning)
		}
	}
	if hostErr != nil {
		res.Status, res.Reason, res.NextAction = StatusBlocked, hostErr.Error(), identityNextAction(hostErr)
		appendIdentityDiagnostic(&res, hostErr)
		return res
	}
	if _, err := s.worktreeRoot(cfg, host); err != nil {
		res.Status, res.Reason, res.NextAction = StatusBlocked, err.Error(), "declare this host in worktree-roots"
		return res
	}
	if cfg.SchemaVersion == 0 {
		res.Warnings = append(res.Warnings, "task runtime config has no schema-version; add schema-version: 1")
	}
	provider := gzGitProvider{runner: s.runner}
	providerReport, providerDiagnostics, providerErr := provider.diagnose(ctx, s.root)
	res.Provider = &providerReport
	res.Diagnostics = append(res.Diagnostics, providerDiagnostics...)
	if providerErr != nil {
		res.Status, res.Reason, res.NextAction = StatusBlocked, providerErr.Error(), "install or repair the configured integration provider and its integrate check/run capabilities"
		return res
	}
	store, err := s.store(ctx)
	if err != nil {
		res.Status, res.Reason = StatusBlocked, err.Error()
		return res
	}
	if lock, err := store.lockInfo(s.now()); err == nil && lock.Reclaimable {
		res.Lock = &lock
		res.Warnings = append(res.Warnings, fmt.Sprintf("mutation lock %s names dead pid %d on this host; the next mutating command reclaims it", store.lockPath(), lock.PID))
	} else if err == nil {
		res.Status, res.Reason, res.NextAction = StatusBlocked, "stale or active mutation lock: "+store.lockPath(), "inspect the lock owner; do not delete it automatically"
		res.Lock = &lock
		return res
	}
	if _, err := store.latestReceipts(); err != nil {
		res.Status, res.Reason, res.NextAction = StatusBlocked, "read task receipts: "+err.Error(), "inspect task runtime receipt evidence"
		return res
	}
	version, err := s.command(ctx, s.root, "wt", "--version")
	if err != nil {
		res.Status, res.Reason = StatusBlocked, err.Error()
		return res
	}
	res.Diagnostics = append(res.Diagnostics, diagnostic(version))
	if version.ExitCode != 0 || !worktrunkVersionSupported(strings.TrimSpace(version.Stdout)) {
		res.Status, res.Reason, res.NextAction = StatusBlocked, fmt.Sprintf("Worktrunk %s or newer stable is required; observed %q (exit %d)", worktrunkVersion, strings.TrimSpace(version.Stdout+version.Stderr), version.ExitCode), "align the owning source declaration and installed tool with the supported Worktrunk line; do not force an older binary"
		return res
	}
	listed, err := s.worktrunkList(ctx)
	if err != nil {
		res.Status, res.Reason = StatusBlocked, err.Error()
		res.Diagnostics = append(res.Diagnostics, diagnostic(listed.Diagnostic))
		return res
	}
	if listed.SkippedNoBranch > 0 {
		res.Warnings = append(res.Warnings, fmt.Sprintf("Worktrunk listed %d item(s) with no branch (a detached worktree lists this way); they were skipped and no task lookup is affected", listed.SkippedNoBranch))
	}
	if listed.SkippedNoPath > 0 {
		res.Warnings = append(res.Warnings, fmt.Sprintf("Worktrunk listed %d item(s) that name a branch but no worktree.path; they were skipped, so a task on those branches is reported missing", listed.SkippedNoPath))
	}
	res.Diagnostics = append(res.Diagnostics, diagnostic(listed.Diagnostic))
	return res
}
