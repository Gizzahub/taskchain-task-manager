package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Gizzahub/taskchain-task-manager/internal/card"
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, out, errOut io.Writer) int {
	if len(args) > 0 && args[0] == "rejoin-board" {
		return runRejoinBoard(args, out, errOut)
	}
	if len(args) > 0 && args[0] == "adopt-archive-capacity" {
		return runArchiveCapacity(args, out, errOut)
	}
	if len(args) > 0 && args[0] == "adopt-legacy-archive" {
		return runLegacyArchive(args, out, errOut)
	}
	if len(args) > 0 && args[0] == "archive" {
		return runArchive(args, out, errOut)
	}
	if len(args) > 0 && args[0] == "revise-policy" {
		return runPolicyRevision(args, out, errOut)
	}
	if len(args) > 0 && args[0] == "relocate" {
		return runRelocation(args, out, errOut)
	}
	if len(args) > 0 && args[0] == "repair-status" {
		return runRepairStatus(args, out, errOut)
	}
	if len(args) > 0 && args[0] == "activate-policy" {
		return runPolicyActivation(args, out, errOut)
	}
	if len(args) > 0 && args[0] == "validate-policy" {
		return runPolicyValidation(args, out, errOut)
	}
	if len(args) > 0 && args[0] == "validate-context" {
		return runContextValidation(args, out, errOut)
	}
	if len(args) > 0 && args[0] == "register-context" {
		return runRegisterContext(args, out, errOut)
	}
	if len(args) > 0 && args[0] == "show-context" {
		return runShowContext(args, out, errOut)
	}
	if len(args) > 0 && (args[0] == "validate" || args[0] == "validate-completion") {
		return runValidation(args, out, errOut)
	}
	if len(args) > 0 && args[0] == "enable-shared" {
		return runEnableShared(args, out, errOut)
	}
	if len(args) > 0 && args[0] == "inspect-worktrees" {
		return runInspectWorktrees(args, out, errOut)
	}
	if len(args) > 0 && args[0] == "import-ids" {
		return runImportIDs(args, out, errOut)
	}
	if len(args) > 0 && args[0] == "reserve-ids" {
		return runReservations(args, out, errOut)
	}
	if len(args) > 0 && args[0] == "create-bundle" {
		return runCreateBundle(args, out, errOut)
	}
	if len(args) > 0 && args[0] == "plan-progress" {
		return runPlanProgress(args, out, errOut)
	}
	if len(args) > 0 && (args[0] == "transition" || args[0] == "recover") {
		return runTransition(args, out, errOut)
	}
	if len(args) > 0 && (args[0] == "claim" || args[0] == "release") {
		return runClaim(args, out, errOut)
	}
	if len(args) > 0 && (args[0] == "init" || args[0] == "create" || args[0] == "list" || args[0] == "ready") {
		return runStore(args, out, errOut)
	}
	if len(args) == 1 && (args[0] == "--help" || args[0] == "help") {
		fmt.Fprintln(out, "Usage: taskchain-task-manager <show|validate> <file> --json")
		fmt.Fprintln(out, "       taskchain-task-manager validate <file> --config <validation.yaml> --json")
		fmt.Fprintln(out, "       taskchain-task-manager validate-completion <file> --config <validation.yaml> --json")
		fmt.Fprintln(out, "       taskchain-task-manager validate-policy <policy.yaml> --json")
		fmt.Fprintln(out, "       taskchain-task-manager activate-policy <policy.yaml> --dir <board> [--all-worktrees] [--resume] --json")
		fmt.Fprintln(out, "       taskchain-task-manager revise-policy <policy.yaml> --dir <board> --expected-authority <id> --expected-digest <sha256> [--all-worktrees] [--resume] --json")
		fmt.Fprintln(out, "       taskchain-task-manager validate-context <intent-batch-or-iteration.json> --json")
		fmt.Fprintln(out, "       taskchain-task-manager register-context <file> --dir <board> --json")
		fmt.Fprintln(out, "       taskchain-task-manager show-context --dir <board> --kind intent|batch|iteration --id ID --revision N --json")
		fmt.Fprintln(out, "       taskchain-task-manager enable-shared --dir <board> --all-worktrees [--resume] --json")
		fmt.Fprintln(out, "       taskchain-task-manager inspect-worktrees --repo <root> --board <path> --json")
		fmt.Fprintln(out, "       taskchain-task-manager <init|list|ready> --dir <board> --json")
		fmt.Fprintln(out, "       taskchain-task-manager import-ids --repo <root> --board <path> (--preview | --dir <target> [--adopt]) --json")
		fmt.Fprintln(out, "       taskchain-task-manager reserve-ids --dir <board> [--adopt] [--id TASK-N ...] --json")
		fmt.Fprintln(out, "       taskchain-task-manager create-bundle <file> --dir <board> [--adopt] [--resume] --json")
		fmt.Fprintln(out, "       taskchain-task-manager create --dir <board> --title <title> [--kind task|plan|issue|backlog] [--id PREFIX-N] [--depends-on PREFIX-N ...] --json")
		fmt.Fprintln(out, "       taskchain-task-manager create --dir <board> --title <title> --config <rules.yaml> --type <type> --priority <priority> --summary <text> --criterion <text> [--criterion <text> ...] --json")
		fmt.Fprintln(out, "       taskchain-task-manager <claim|release> --dir <board> --id TASK-N --owner <owner> --token <32 lowercase hex> --json")
		fmt.Fprintln(out, "       taskchain-task-manager claim --resume --dir <board> --id TASK-N --owner <owner> --token <32 lowercase hex> --json")
		fmt.Fprintln(out, "       taskchain-task-manager "+planProgressUsage)
		fmt.Fprintln(out, "       taskchain-task-manager <transition|recover> --dir <board> --id TASK-N --owner <owner> --token <claim token> --request-id <32 lowercase hex> --from <zone> --to <zone> --json")
		fmt.Fprintln(out, "       taskchain-task-manager repair-status --dir <board> --id TASK-N --path <card> --owner <owner> [--token <claim token>] --request-id <32 lowercase hex> --expected-sha256 <64 lowercase hex> [--adopt | --resume] --json")
		fmt.Fprintln(out, "       taskchain-task-manager", relocationUsage)
		fmt.Fprintln(out, "       taskchain-task-manager", archiveUsage)
		fmt.Fprintln(out, "       taskchain-task-manager", legacyArchiveUsage)
		fmt.Fprintln(out, "       taskchain-task-manager", archiveCapacityUsage)
		fmt.Fprintln(out, "       taskchain-task-manager", rejoinBoardUsage)
		return 0
	}
	if len(args) != 3 || args[2] != "--json" || args[0] != "show" {
		fmt.Fprintln(errOut, "usage: taskchain-task-manager <show|validate> <file> --json")
		return 2
	}
	data, err := os.ReadFile(args[1])
	if err != nil {
		fmt.Fprintln(errOut, "read card:", err)
		return 1
	}
	doc, err := card.Parse(data)
	if err != nil {
		fmt.Fprintln(errOut, "parse card:", err)
		return 1
	}
	var result any = doc.Snapshot(cardRelativePath(args[1]))
	if err := json.NewEncoder(out).Encode(result); err != nil {
		fmt.Fprintln(errOut, "write result:", err)
		return 1
	}
	return 0
}

// Only a tasks/ boundary makes directory names workflow metadata. An unrelated
// ancestor named done or review must not change a standalone document's view.
func cardRelativePath(path string) string {
	parts := strings.Split(filepath.ToSlash(filepath.Clean(path)), "/")
	for i := len(parts) - 2; i >= 0; i-- {
		if parts[i] == "tasks" {
			return strings.Join(parts[i+1:], "/")
		}
	}
	return filepath.Base(path)
}
