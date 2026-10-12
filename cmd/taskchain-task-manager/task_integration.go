package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Gizzahub/taskchain-task-manager/internal/integrationqueue"
	"github.com/Gizzahub/taskchain-task-manager/internal/taskruntime"
)

const integrationHelp = `Usage: taskchain-task-manager task integration <request|status|cancel> --queue-root ABSOLUTE_PATH [options]

request options:
  --repo PATH --repo-id ID --task ID --idempotency-key KEY
  [--request-id ID] [--resource NAME] [--gate-profile NAME] [--json]
status options:
  [--request-id ID] [--json]
cancel options:
  --request-id ID --revision N [--json]
`

func taskIntegration(ctx context.Context, args []string, out io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: taskchain-task-manager task integration <request|status|cancel> --queue-root ABSOLUTE_PATH")
	}
	if args[0] == "--help" || args[0] == "-h" {
		fmt.Fprint(out, integrationHelp)
		return nil
	}
	command := args[0]
	flags, err := parseIntegrationFlags(args[1:])
	if err != nil {
		return err
	}
	if flags.help {
		fmt.Fprint(out, integrationHelp)
		return nil
	}
	if !filepath.IsAbs(flags.queueRoot) {
		return fmt.Errorf("--queue-root must be an absolute path")
	}
	switch command {
	case "request":
		spec, err := integrationRequest(ctx, flags)
		if err != nil {
			return err
		}
		store, err := integrationqueue.New(filepath.Clean(flags.queueRoot))
		if err != nil {
			return fmt.Errorf("open integration queue: %w", err)
		}
		request, err := store.Enqueue(spec)
		if err != nil {
			return err
		}
		return writeIntegrationResult(out, request, flags.asJSON)
	case "status":
		store, err := integrationqueue.Open(filepath.Clean(flags.queueRoot))
		if err != nil {
			return fmt.Errorf("open integration queue: %w", err)
		}
		if flags.requestID != "" {
			view, err := store.Get(flags.requestID)
			if err != nil {
				return err
			}
			return writeIntegrationResult(out, view, flags.asJSON)
		}
		views, err := store.List()
		if err != nil {
			return err
		}
		return writeIntegrationResult(out, views, flags.asJSON)
	case "cancel":
		if flags.requestID == "" || !flags.revisionSet {
			return fmt.Errorf("cancel requires --request-id and --revision")
		}
		store, err := integrationqueue.Open(filepath.Clean(flags.queueRoot))
		if err != nil {
			return fmt.Errorf("open integration queue: %w", err)
		}
		owner, err := currentIntegrationOwner(ctx)
		if err != nil {
			return err
		}
		request, err := store.Cancel(flags.requestID, owner, flags.revision)
		if err != nil {
			return err
		}
		return writeIntegrationResult(out, request, flags.asJSON)
	default:
		return fmt.Errorf("unknown integration command %s (valid: request, status, cancel)", command)
	}
}

type integrationFlags struct {
	queueRoot, repo, repoID, task, idempotencyKey, requestID, resource, gateProfile string
	revision                                                                        uint64
	revisionSet, asJSON, help                                                       bool
}

func parseIntegrationFlags(args []string) (integrationFlags, error) {
	var flags integrationFlags
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "--help", "-h":
			flags.help = true
		case "--json":
			flags.asJSON = true
		default:
			if !strings.HasPrefix(arg, "--") {
				return flags, fmt.Errorf("unexpected integration argument %s", arg)
			}
			if i+1 >= len(args) {
				return flags, fmt.Errorf("%s requires a value", arg)
			}
			i++
			value := args[i]
			switch arg {
			case "--queue-root":
				flags.queueRoot = value
			case "--repo":
				flags.repo = value
			case "--repo-id":
				flags.repoID = value
			case "--task":
				flags.task = value
			case "--idempotency-key":
				flags.idempotencyKey = value
			case "--request-id":
				flags.requestID = value
			case "--resource":
				flags.resource = value
			case "--gate-profile":
				flags.gateProfile = value
			case "--revision":
				parsed, err := strconv.ParseUint(value, 10, 64)
				if err != nil || parsed == 0 {
					return flags, fmt.Errorf("--revision must be a positive integer")
				}
				flags.revision, flags.revisionSet = parsed, true
			default:
				return flags, fmt.Errorf("unknown integration flag %s", arg)
			}
		}
	}
	if flags.queueRoot == "" && !flags.help {
		return flags, fmt.Errorf("--queue-root is required")
	}
	return flags, nil
}

func integrationRequest(ctx context.Context, flags integrationFlags) (integrationqueue.Spec, error) {
	for _, required := range []struct{ name, value string }{
		{"--repo", flags.repo}, {"--repo-id", flags.repoID}, {"--task", flags.task}, {"--idempotency-key", flags.idempotencyKey},
	} {
		if strings.TrimSpace(required.value) == "" {
			return integrationqueue.Spec{}, fmt.Errorf("request requires %s", required.name)
		}
	}
	if taskruntime.NormalizeIdentity(flags.repoID) != flags.repoID {
		return integrationqueue.Spec{}, fmt.Errorf("--repo-id must be a canonical lowercase kebab-case identifier")
	}
	repo, commonDir, err := canonicalIntegrationRepository(ctx, flags.repo)
	if err != nil {
		return integrationqueue.Spec{}, err
	}
	svc := taskruntime.NewService(repo, taskruntime.NewExecutor())
	owner, err := svc.ResolveOwner(ctx)
	if err != nil {
		return integrationqueue.Spec{}, fmt.Errorf("resolve current task owner: %w", err)
	}
	executions, receipts, err := svc.ReadLedger(ctx)
	if err != nil {
		return integrationqueue.Spec{}, fmt.Errorf("read task runtime ledger: %w", err)
	}
	execution, found := integrationExecution(executions, taskruntime.NormalizeIdentity(flags.task))
	if !found {
		return integrationqueue.Spec{}, fmt.Errorf("task execution was not found")
	}
	if execution.Owner != owner {
		return integrationqueue.Spec{}, fmt.Errorf("only the recorded execution owner may request integration")
	}
	if execution.Type == "" || execution.Source == "" || execution.Worktree == "" || execution.StartedAt.IsZero() || execution.Owner.Actor == "" || execution.Owner.Host == "" || execution.Owner.Kind == "" {
		return integrationqueue.Spec{}, fmt.Errorf("recorded execution generation is incomplete")
	}
	if taskruntime.EffectivePurpose(execution.Purpose) != taskruntime.PurposeDelivery || taskruntime.ValidatePurposeBranch(execution.Purpose, execution.Branch) != nil {
		return integrationqueue.Spec{}, fmt.Errorf("integration requests require a valid delivery execution")
	}
	if receipt, ok := receipts[execution.Task]; ok && taskruntime.ReceiptBindsExecution(execution, receipt) && (receipt.Status == taskruntime.StatusFinishing || taskruntime.TerminalStatus(receipt.Status)) {
		return integrationqueue.Spec{}, fmt.Errorf("task execution is terminal or finishing")
	}
	taskSHA, err := integrationGitRef(ctx, repo, "refs/heads/"+execution.Branch+"^{commit}")
	if err != nil {
		return integrationqueue.Spec{}, fmt.Errorf("resolve task branch: %w", err)
	}
	sourceSHA, err := integrationGitRef(ctx, repo, "refs/remotes/origin/"+execution.Source+"^{commit}")
	if err != nil {
		return integrationqueue.Spec{}, fmt.Errorf("resolve integration source: %w", err)
	}
	requestID := flags.requestID
	if requestID == "" {
		requestID, err = integrationRequestID()
		if err != nil {
			return integrationqueue.Spec{}, err
		}
	}
	queueOwner := integrationOwner(owner)
	resource := flags.resource
	if resource == "" {
		resource = "host/" + execution.Owner.Host + "/integration"
	}
	profile := flags.gateProfile
	if profile == "" {
		profile = "repository-default"
	}
	spec := integrationqueue.Spec{
		ID: requestID, IdempotencyKey: flags.idempotencyKey, RepositoryID: flags.repoID, RepositoryPath: repo, CommonDir: commonDir,
		ExpectedTaskSHA: taskSHA, ExpectedSourceSHA: sourceSHA, Source: execution.Source, Target: "origin/" + execution.Source,
		GateProfile: profile, Resource: resource, Owner: queueOwner,
		Generation: integrationqueue.Generation{Task: execution.Task, Type: execution.Type, Purpose: string(taskruntime.EffectivePurpose(execution.Purpose)), Source: execution.Source, Branch: execution.Branch, Worktree: execution.Worktree, Owner: queueOwner, StartedAt: execution.StartedAt},
	}
	return spec, nil
}

func currentIntegrationOwner(ctx context.Context) (integrationqueue.Owner, error) {
	wd, err := os.Getwd()
	if err != nil {
		return integrationqueue.Owner{}, err
	}
	svc := taskruntime.NewService(taskruntime.EnclosingRepository(wd), taskruntime.NewExecutor())
	owner, err := svc.ResolveOwner(ctx)
	if err != nil {
		return integrationqueue.Owner{}, fmt.Errorf("resolve current task owner: %w", err)
	}
	return integrationOwner(owner), nil
}

func integrationOwner(owner taskruntime.Owner) integrationqueue.Owner {
	return integrationqueue.Owner{Actor: owner.Actor, Host: owner.Host, Kind: owner.Kind}
}

func integrationExecution(executions []taskruntime.Execution, task string) (taskruntime.Execution, bool) {
	for _, execution := range executions {
		if execution.Task == task {
			return execution, true
		}
	}
	return taskruntime.Execution{}, false
}

func canonicalIntegrationRepository(ctx context.Context, path string) (string, string, error) {
	if !filepath.IsAbs(path) {
		return "", "", fmt.Errorf("--repo must be an absolute path")
	}
	repo, err := integrationGitRef(ctx, path, "--show-toplevel")
	if err != nil {
		return "", "", fmt.Errorf("resolve repository: %w", err)
	}
	repo, err = filepath.EvalSymlinks(repo)
	if err != nil {
		return "", "", fmt.Errorf("canonicalize repository: %w", err)
	}
	common, err := integrationGitRef(ctx, repo, "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return "", "", fmt.Errorf("resolve git common directory: %w", err)
	}
	common, err = filepath.EvalSymlinks(common)
	if err != nil {
		return "", "", fmt.Errorf("canonicalize git common directory: %w", err)
	}
	return repo, common, nil
}

func integrationGitRef(ctx context.Context, dir string, args ...string) (string, error) {
	command := exec.CommandContext(ctx, "git", append([]string{"-C", dir, "rev-parse", "--verify", "--quiet"}, args...)...)
	if len(args) == 1 && args[0] == "--show-toplevel" {
		command = exec.CommandContext(ctx, "git", "-C", dir, "rev-parse", "--show-toplevel")
	}
	if len(args) == 2 && args[1] == "--git-common-dir" {
		command = exec.CommandContext(ctx, "git", "-C", dir, "rev-parse", args[0], args[1])
	}
	output, err := command.Output()
	if err != nil {
		return "", err
	}
	value := strings.TrimSpace(string(output))
	if value == "" {
		return "", fmt.Errorf("Git returned an empty value")
	}
	return value, nil
}

func integrationRequestID() (string, error) {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("generate request id: %w", err)
	}
	return hex.EncodeToString(bytes), nil
}

func writeIntegrationResult(out io.Writer, value any, asJSON bool) error {
	if asJSON {
		return json.NewEncoder(out).Encode(value)
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(out, string(data))
	return err
}
