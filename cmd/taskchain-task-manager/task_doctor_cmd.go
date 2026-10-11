package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/Gizzahub/taskchain-task-manager/internal/taskflow"
)

const doctorUsage = "usage: taskchain-task-manager task doctor [--json] [--capability ce.task.gate-doctor/v2]"

func taskDoctor(ctx context.Context, args []string, out io.Writer) error {
	asJSON, capability, help, err := parseDoctorArgs(args, out)
	if err != nil || help {
		return err
	}
	if capability != "" && capability != taskflow.DoctorCapability {
		return usagef("capability mismatch: want %s have %s", capability, taskflow.DoctorCapability)
	}
	root, err := os.Getwd()
	if err != nil {
		return usagef("get working directory: %s", err.Error())
	}
	top, err := taskflow.ShowToplevel(ctx, root)
	if err != nil {
		return usagef("%s", err.Error())
	}
	result, err := taskflow.InspectDoctor(ctx, top)
	if err != nil {
		return usagef("%s", err.Error())
	}
	if asJSON {
		data, err := json.MarshalIndent(result.Report, "", "  ")
		if err != nil {
			return usagef("%s", err.Error())
		}
		fmt.Fprintln(out, string(data))
	} else {
		for _, gate := range result.Report.Gates {
			fmt.Fprintf(out, "%s %s: %s (%s)\n", gate.Worktree, gate.Declaration.ID, gate.Status, gate.Remediation)
		}
	}
	if result.IdentityUnknown || result.DeclarationAbsent {
		return usagef("repository identity is unknown")
	}
	if result.Report.Status != "adopted" {
		return fmt.Errorf("task gate is %s", result.Report.Status)
	}
	return nil
}

func parseDoctorArgs(args []string, out io.Writer) (asJSON bool, capability string, help bool, err error) {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--help" || arg == "-h":
			fmt.Fprintln(out, doctorUsage)
			return false, "", true, nil
		case arg == "--json":
			asJSON = true
		case arg == "--capability":
			value, valueErr := flagValue(args, i)
			if valueErr != nil {
				return false, "", false, usagef("flag --capability requires a value")
			}
			i++
			capability = value
		case strings.HasPrefix(arg, "--capability="):
			capability = strings.TrimPrefix(arg, "--capability=")
		default:
			return false, "", false, usagef("unknown flag %s (valid: --json, --capability, --help, -h)", arg)
		}
	}
	return asJSON, capability, false, nil
}
