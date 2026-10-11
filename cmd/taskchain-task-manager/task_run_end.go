package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/Gizzahub/taskchain-task-manager/internal/taskruntime"
)

const (
	runFinishHelp  = "Usage: ce task run-finish [task] [--json]\n\nVerify, integrate, and recover a pushed clean task through gz-git.\nWith no task, finish the single active execution owned by the current actor and host.\nNo match names ce task run-list. Several matches refuse and print ce task run-finish <task> for each id.\n"
	runRecoverHelp = "Usage: ce task run-recover <task> [--json]\n\nClose a proven exit-3 provider cleanup failure without repeating integration or removing residual paths or refs.\n"
	runAbortHelp   = "Usage: ce task run-abort <task> [--reason R] [--json]\n\nClose one execution as ABORTED by appending a receipt, without integrating it.\nThe task is named explicitly; no worktree, branch, or remote ref is removed.\n"
	runSettleHelp  = "Usage: ce task run-settle <task> [--json]\n\nClose a missing-worktree execution as ABORTED only after local Git proves its surviving task tip is contained in its named source branch. This command does not fetch or mutate Git state.\n"
	runCloseHelp   = "Usage: ce task run-close <task> --reason <reason> [--json]\n\nRepository-designated administrator closure of one missing-worktree execution as ABORTED.\nAppends an audited receipt without integration, Git proof or cleanup claims.\n"
	runDiscardHelp = "Usage: ce task run-discard <task> --reason R [--take-over-from actor/host] [--json]\n\nDiscard a clean, owner-bound task worktree and branch without integration.\nThe recorded owner runs it directly; cross-actor takeover requires the exact owner identity and a reason.\nIt refuses dirty worktrees, remote branches, path mismatches, and cross-host takeover.\n"
)

// runExitError carries the exit code a termination command computed.
// Flag and argument mistakes stay ordinary errors and exit 1.
type runExitError struct {
	code   int
	reason string
}

func (e *runExitError) Error() string { return e.reason }
func (e *runExitError) ExitCode() int { return e.code }

func finishRunCode(out io.Writer, result taskruntime.Response, asJSON bool, code int) error {
	if err := taskruntime.WriteResponse(out, result, asJSON, false); err != nil {
		return err
	}
	if code == 0 {
		return nil
	}
	return &runExitError{code: code, reason: result.Reason}
}

func taskRunFinish(args []string, out io.Writer) error {
	asJSON, task, help, err := parseRunFinish(args)
	if help {
		fmt.Fprint(out, runFinishHelp)
		return nil
	}
	if err != nil {
		return describeUnknownFlag(err, "(valid: --json, --help, -h)")
	}
	svc, err := newRunService()
	if err != nil {
		return err
	}
	result, code := svc.Finish(context.Background(), task)
	return finishRunCode(out, result, asJSON, code)
}

func taskRunRecover(args []string, out io.Writer) error {
	asJSON, task, help, err := parseRunRecover(args)
	if err != nil || help {
		if help {
			fmt.Fprint(out, runRecoverHelp)
		}
		return err
	}
	svc, err := newRunService()
	if err != nil {
		return err
	}
	result, code := svc.Recover(context.Background(), task)
	return finishRunCode(out, result, asJSON, code)
}

func taskRunAbort(args []string, out io.Writer) error {
	asJSON, task, reason, help, err := parseRunAbort(args)
	if err != nil || help {
		if help {
			fmt.Fprint(out, runAbortHelp)
		}
		return err
	}
	svc, err := newRunService()
	if err != nil {
		return err
	}
	result, code := svc.Abort(context.Background(), task, reason)
	return finishRunCode(out, result, asJSON, code)
}

func taskRunClose(args []string, out io.Writer) error {
	asJSON, task, reason, help, err := parseRunClose(args)
	if err != nil || help {
		if help {
			fmt.Fprint(out, runCloseHelp)
		}
		return err
	}
	svc, err := newRunService()
	if err != nil {
		return err
	}
	result, code := svc.Close(context.Background(), task, reason)
	return finishRunCode(out, result, asJSON, code)
}

func taskRunSettle(args []string, out io.Writer) error {
	asJSON, task, help, err := parseRunSettle(args)
	if err != nil || help {
		if help {
			fmt.Fprint(out, runSettleHelp)
		}
		return err
	}
	svc, err := newRunService()
	if err != nil {
		return err
	}
	result, code := svc.Settle(context.Background(), task)
	return finishRunCode(out, result, asJSON, code)
}

func taskRunDiscard(args []string, out io.Writer) error {
	asJSON, task, reason, takeOver, help, err := parseRunDiscard(args)
	if err != nil || help {
		if help {
			fmt.Fprint(out, runDiscardHelp)
		}
		return err
	}
	svc, err := newRunService()
	if err != nil {
		return err
	}
	result, code := svc.Discard(context.Background(), task, reason, takeOver)
	return finishRunCode(out, result, asJSON, code)
}

type unknownFlagError struct{ arg string }

func (e unknownFlagError) Error() string { return "unknown flag: " + e.arg }

func describeUnknownFlag(err error, valid string) error {
	var unknown unknownFlagError
	if errors.As(err, &unknown) {
		return fmt.Errorf("unknown flag: %s %s", unknown.arg, valid)
	}
	return err
}

func parseRunFinish(args []string) (bool, string, bool, error) {
	asJSON, task := false, ""
	for _, arg := range args {
		switch {
		case arg == "--help" || arg == "-h":
			return false, "", true, nil
		case arg == "--json":
			asJSON = true
		case strings.HasPrefix(arg, "-"):
			return false, "", false, unknownFlagError{arg: arg}
		case task != "":
			return false, "", false, fmt.Errorf("run-finish accepts at most one task")
		default:
			task = arg
		}
	}
	return asJSON, task, false, nil
}

func parseRunRecover(args []string) (bool, string, bool, error) {
	asJSON, task := false, ""
	for _, arg := range args {
		switch {
		case arg == "--help" || arg == "-h":
			return false, "", true, nil
		case arg == "--json":
			asJSON = true
		case strings.HasPrefix(arg, "-"):
			return false, "", false, fmt.Errorf("unknown flag %s (valid: --json, --help, -h)", arg)
		case task != "":
			return false, "", false, fmt.Errorf("run-recover accepts one task")
		default:
			task = arg
		}
	}
	if task == "" {
		return false, "", false, fmt.Errorf("usage: ce task run-recover <task> [--json]")
	}
	return asJSON, task, false, nil
}

func parseRunAbort(args []string) (bool, string, string, bool, error) {
	asJSON, task, reason := false, "", ""
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--help" || arg == "-h":
			return false, "", "", true, nil
		case arg == "--json":
			asJSON = true
		case arg == "--reason":
			i++
			if i >= len(args) {
				return false, "", "", false, fmt.Errorf("--reason requires a value")
			}
			reason = args[i]
		case strings.HasPrefix(arg, "-"):
			return false, "", "", false, fmt.Errorf("unknown flag %s (valid: --reason, --json, --help, -h)", arg)
		case task != "":
			return false, "", "", false, fmt.Errorf("run-abort accepts one task")
		default:
			task = arg
		}
	}
	if task == "" {
		return false, "", "", false, fmt.Errorf("usage: ce task run-abort <task> [--reason R] [--json]")
	}
	return asJSON, task, reason, false, nil
}

func parseRunClose(args []string) (bool, string, string, bool, error) {
	asJSON, task, reason, reasonSeen := false, "", "", false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--help" || arg == "-h":
			return false, "", "", true, nil
		case arg == "--json":
			asJSON = true
		case arg == "--reason":
			if reasonSeen || i+1 == len(args) || strings.HasPrefix(args[i+1], "--") {
				return false, "", "", false, fmt.Errorf("--reason requires exactly one nonblank value")
			}
			i++
			reason, reasonSeen = strings.TrimSpace(args[i]), true
		case strings.HasPrefix(arg, "-"):
			return false, "", "", false, fmt.Errorf("unknown flag %s (valid: --reason, --json, --help, -h)", arg)
		case task != "" || strings.TrimSpace(arg) == "":
			return false, "", "", false, fmt.Errorf("run-close accepts exactly one nonblank task")
		default:
			task = arg
		}
	}
	if task == "" || reason == "" {
		return false, "", "", false, fmt.Errorf("usage: ce task run-close <task> --reason <reason> [--json]")
	}
	return asJSON, task, reason, false, nil
}

func parseRunSettle(args []string) (bool, string, bool, error) {
	asJSON, task := false, ""
	for _, arg := range args {
		switch {
		case arg == "--help" || arg == "-h":
			return false, "", true, nil
		case arg == "--json":
			asJSON = true
		case strings.HasPrefix(arg, "-"):
			return false, "", false, fmt.Errorf("unknown flag %s (valid: --json, --help, -h)", arg)
		case task != "" || strings.TrimSpace(arg) == "":
			return false, "", false, fmt.Errorf("run-settle accepts exactly one nonblank task")
		default:
			task = strings.TrimSpace(arg)
		}
	}
	if task == "" {
		return false, "", false, fmt.Errorf("usage: ce task run-settle <task> [--json]")
	}
	return asJSON, task, false, nil
}

func parseRunDiscard(args []string) (bool, string, string, string, bool, error) {
	asJSON, task, reason, takeOver := false, "", "", ""
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--help" || arg == "-h":
			return false, "", "", "", true, nil
		case arg == "--json":
			asJSON = true
		case arg == "--reason" || arg == "--take-over-from":
			i++
			if i >= len(args) || strings.HasPrefix(args[i], "--") {
				return false, "", "", "", false, fmt.Errorf("%s requires a value", arg)
			}
			if arg == "--reason" {
				reason = args[i]
			} else {
				takeOver = args[i]
			}
		case strings.HasPrefix(arg, "-"):
			return false, "", "", "", false, fmt.Errorf("unknown flag %s (valid: --reason, --take-over-from, --json, --help, -h)", arg)
		case task != "":
			return false, "", "", "", false, fmt.Errorf("run-discard accepts one task")
		default:
			task = arg
		}
	}
	if task == "" || strings.TrimSpace(reason) == "" {
		return false, "", "", "", false, fmt.Errorf("usage: ce task run-discard <task> --reason R [--take-over-from actor/host] [--json]")
	}
	return asJSON, task, reason, takeOver, false, nil
}
