package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/Gizzahub/taskchain-task-manager/internal/taskruntime"
)

const (
	runDoctorHelp = "Usage: ce task run-doctor [--json]\n\nCheck task runtime configuration, integration provider presence/version/path/capabilities, lock, receipt evidence readability and parseability, Worktrunk 0.80 or newer stable, and schema 2 output.\n"
	runStartHelp  = "Usage: taskchain-task-manager task run-start <task> --type <feat|fix|refactor|docs|test|chore|perf> [--purpose delivery|experiment|review] [--resume] [--json]\n\nCreate one owner-bound task worktree through Worktrunk.\n\nAn unfinished execution of the same task is never returned implicitly: the\ncommand is BLOCKED and names its branch, worktree, start time and owner, because\nanother session of the same owner cannot be told apart. --resume returns your own\nexisting execution; another owner stays blocked.\n"
	runStatusHelp = "Usage: ce task run-status [task] [--json]\n\nDerive one task's runtime state without acquiring the mutation lock.\n"
	runListHelp   = "Usage: ce task run-list [--json]\n\n" +
		"List repository-shared task executions without acquiring the mutation lock.\n" +
		"Human output gives each live execution its own line (task, branch,\n" +
		"worktree); a BLOCKED record keeps its line and carries its reason. Only\n" +
		"terminal executions (DONE, ABORTED) fold into one count line.\n" +
		"--json returns two parallel arrays: executions is history with no status\n" +
		"field of its own, and states carries the derived status for those same\n" +
		"records in the same order.\n"
)

func newRunService() (*taskruntime.Service, error) {
	wd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	return taskruntime.NewService(taskruntime.EnclosingRepository(wd), taskruntime.NewExecutor()), nil
}

func taskRunDoctor(args []string, out io.Writer) error {
	asJSON, help, err := parseRunJSONOnly(args)
	if err != nil || help {
		if help {
			fmt.Fprint(out, runDoctorHelp)
		}
		return err
	}
	svc, err := newRunService()
	if err != nil {
		return err
	}
	return finishRun(out, svc.Doctor(context.Background()), asJSON, false, false)
}

func taskRunStart(args []string, out io.Writer) error {
	asJSON, task, kind, purpose, purposeSet, resume, help, err := parseRunStartOptions(args)
	if err != nil || help {
		if help {
			fmt.Fprint(out, runStartHelp)
		}
		return err
	}
	svc, err := newRunService()
	if err != nil {
		return err
	}
	if purposeSet {
		return finishRun(out, svc.Start(context.Background(), task, kind, resume, purpose), asJSON, false, false)
	}
	return finishRun(out, svc.Start(context.Background(), task, kind, resume), asJSON, false, false)
}

func taskRunStatus(args []string, out io.Writer) error {
	return taskRunRead(args, out, false)
}

func taskRunList(args []string, out io.Writer) error {
	return taskRunRead(args, out, true)
}

func taskRunRead(args []string, out io.Writer, list bool) error {
	asJSON, task, help, err := parseRunRead(args, list)
	if err != nil || help {
		if help {
			if list {
				fmt.Fprint(out, runListHelp)
			} else {
				fmt.Fprint(out, runStatusHelp)
			}
		}
		return err
	}
	svc, err := newRunService()
	if err != nil {
		return err
	}
	var result taskruntime.Response
	if list {
		result = svc.List(context.Background())
	} else {
		result = svc.Status(context.Background(), task)
	}
	return finishRun(out, result, asJSON, list, true)
}

func finishRun(out io.Writer, result taskruntime.Response, asJSON, listMode, failOnUnknown bool) error {
	if err := taskruntime.WriteResponse(out, result, asJSON, listMode); err != nil {
		return err
	}
	failed := result.Status == taskruntime.StatusBlocked || (failOnUnknown && result.Status == taskruntime.StatusUnknown)
	if failed {
		return errors.New(result.Reason)
	}
	return nil
}

func parseRunJSONOnly(args []string) (bool, bool, error) {
	asJSON := false
	for _, arg := range args {
		switch arg {
		case "--help", "-h":
			return false, true, nil
		case "--json":
			asJSON = true
		default:
			return false, false, fmt.Errorf("unknown flag %s (valid: --json, --help, -h)", arg)
		}
	}
	return asJSON, false, nil
}

func parseRunStart(args []string) (bool, string, string, bool, bool, error) {
	asJSON, task, kind, _, _, resume, help, err := parseRunStartOptions(args)
	return asJSON, task, kind, resume, help, err
}

func parseRunStartOptions(args []string) (bool, string, string, string, bool, bool, bool, error) {
	asJSON, resume, task, kind, purpose := false, false, "", "", ""
	purposeSet := false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--help", "-h":
			return false, "", "", "", false, false, true, nil
		case "--json":
			asJSON = true
		case "--resume":
			resume = true
		case "--type":
			i++
			if i >= len(args) {
				return false, "", "", "", false, false, false, fmt.Errorf("--type requires a value")
			}
			kind = args[i]
		case "--purpose":
			i++
			if i >= len(args) {
				return false, "", "", "", false, false, false, fmt.Errorf("--purpose requires a value")
			}
			purpose = args[i]
			purposeSet = true
			if purpose != "delivery" && purpose != "experiment" && purpose != "review" {
				return false, "", "", "", false, false, false, fmt.Errorf("--purpose must be delivery, experiment, or review")
			}
		default:
			if strings.HasPrefix(args[i], "-") {
				return false, "", "", "", false, false, false, fmt.Errorf("unknown flag %s (valid: --type, --purpose, --resume, --json, --help, -h)", args[i])
			}
			if task != "" {
				return false, "", "", "", false, false, false, fmt.Errorf("run-start accepts one task")
			}
			task = args[i]
		}
	}
	if task == "" || kind == "" {
		return false, "", "", "", false, false, false, fmt.Errorf("usage: taskchain-task-manager task run-start <task> --type <type> [--purpose delivery|experiment|review]")
	}
	return asJSON, task, kind, purpose, purposeSet, resume, false, nil
}

func parseRunRead(args []string, list bool) (bool, string, bool, error) {
	asJSON, task := false, ""
	for _, arg := range args {
		switch {
		case arg == "--help" || arg == "-h":
			return false, "", true, nil
		case arg == "--json":
			asJSON = true
		case strings.HasPrefix(arg, "-"):
			return false, "", false, fmt.Errorf("unknown flag %s (valid: --json, --help, -h)", arg)
		case list || task != "":
			return false, "", false, fmt.Errorf("unexpected argument %s", arg)
		default:
			task = arg
		}
	}
	return asJSON, task, false, nil
}
