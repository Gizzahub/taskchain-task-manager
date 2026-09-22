package main

import (
	"errors"
	"flag"
	"fmt"
	"github.com/Gizzahub/taskchain-task-manager/internal/outputformat"
	"github.com/Gizzahub/taskchain-task-manager/internal/taskstore"
	"io"
	"os"
	"strconv"
	"strings"
)

const rejoinBoardUsage = "rejoin-board --dir <board> (--prepare | --apply | --status) --json [--source <board>] [--clone] [--rejoin-id <32lowerhex>] [--target-namespace <32lowerhex>] [--target-policy-authority <32lowerhex>] [--source-export <file>] [--source-fenced] [--floor PREFIX:N ...] [--reserve-id PREFIX-N ...] [--plan <file>] [--payload <file>] [--capacity <file>]"

// rejoinFloors collects repeated --floor PREFIX:N values in the order given.
// Reservation floors are an explicit operator assertion about what the source
// reserved, so they are never sorted or deduplicated silently here; the plan
// validator rejects anything that is not already sorted and unique.
type rejoinFloors []taskstore.ReservationFloor

func (f *rejoinFloors) String() string { return "" }
func (f *rejoinFloors) Set(v string) error {
	prefix, through, ok := strings.Cut(v, ":")
	if !ok {
		return errors.New("expected PREFIX:N")
	}
	n, err := strconv.ParseUint(through, 10, 64)
	if err != nil {
		return err
	}
	*f = append(*f, taskstore.ReservationFloor{Prefix: prefix, Through: n})
	return nil
}

type rejoinIDs []string

func (v *rejoinIDs) String() string     { return "" }
func (v *rejoinIDs) Set(s string) error { *v = append(*v, s); return nil }

func runRejoinBoard(args []string, out, errOut io.Writer) int {
	if len(args) == 2 && (args[1] == "--help" || args[1] == "-h") {
		fmt.Fprintln(out, "Usage:", rejoinBoardUsage)
		fmt.Fprintln(out, "Return a board's owner authority to this worktree. --prepare only reads and writes the plan and payload; --apply runs the durable transaction and is the same call as a resume; --status reports recorded evidence without touching it.")
		fmt.Fprintln(out, "--clone bootstraps a new clone-local namespace and policy authority and requires --target-namespace plus an explicit reservation floor, reserved IDs, or --source-export.")
		return 0
	}
	f := flag.NewFlagSet("rejoin-board", flag.ContinueOnError)
	f.SetOutput(errOut)
	dir := f.String("dir", "", "returning task board directory")
	source := f.String("source", "", "source owner board directory")
	prepare := f.Bool("prepare", false, "derive the rejoin plan and payload without publishing")
	apply := f.Bool("apply", false, "run or resume the durable rejoin transaction")
	status := f.Bool("status", false, "report recorded rejoin evidence")
	clone := f.Bool("clone", false, "bootstrap an independent clone authority")
	rejoinID := f.String("rejoin-id", "", "exact 32 lowercase hex rejoin identifier")
	targetNamespace := f.String("target-namespace", "", "new clone-local namespace, 32 lowercase hex")
	targetAuthority := f.String("target-policy-authority", "", "new clone-local policy authority, 32 lowercase hex")
	sourceExport := f.String("source-export", "", "file holding the exact source common state bytes")
	sourceFenced := f.Bool("source-fenced", false, "assert the source writer fence is in place and non-empty")
	planPath := f.String("plan", "", "rejoin plan file")
	payloadPath := f.String("payload", "", "rejoin payload file")
	capacityPath := f.String("capacity", "", "archive capacity payload file")
	asJSON := f.Bool("json", false, "write JSON")
	var floors rejoinFloors
	var reserved rejoinIDs
	f.Var(&floors, "floor", "reservation floor as PREFIX:N, repeatable")
	f.Var(&reserved, "reserve-id", "additional reserved ID, repeatable")
	if err := f.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	selected := 0
	for _, on := range []bool{*prepare, *apply, *status} {
		if on {
			selected++
		}
	}
	if f.NArg() != 0 || *dir == "" || !*asJSON || selected != 1 {
		fmt.Fprintln(errOut, "expected", rejoinBoardUsage)
		return 2
	}
	switch {
	case *prepare:
		if *source == "" || *rejoinID == "" || *planPath == "" || *payloadPath == "" {
			fmt.Fprintln(errOut, "expected", rejoinBoardUsage)
			return 2
		}
		return runRejoinBoardPrepare(rejoinPrepareArgs{
			dir: *dir, source: *source, clone: *clone, rejoinID: *rejoinID,
			targetNamespace: *targetNamespace, targetAuthority: *targetAuthority,
			sourceExport: *sourceExport, sourceFenced: *sourceFenced,
			floors: floors, reserved: reserved,
			planPath: *planPath, payloadPath: *payloadPath, capacityPath: *capacityPath,
		}, out, errOut)
	case *apply:
		if *planPath == "" || *payloadPath == "" {
			fmt.Fprintln(errOut, "expected", rejoinBoardUsage)
			return 2
		}
		planRaw, err := os.ReadFile(*planPath)
		if err != nil {
			fmt.Fprintln(errOut, "read rejoin plan:", err)
			return 1
		}
		payload, err := os.ReadFile(*payloadPath)
		if err != nil {
			fmt.Fprintln(errOut, "read rejoin payload:", err)
			return 1
		}
		result, err := taskstore.ApplyRejoinBoard(*dir, planRaw, payload)
		if err != nil {
			fmt.Fprintln(errOut, "apply board rejoin:", err)
			fmt.Fprintln(errOut, "Preserve the plan, the payload and any pending receipt. Retry --apply with the identical plan after restoring any reported exact state; never delete a pending receipt to make this pass.")
			return 1
		}
		return writeRejoinBoardResult(result, out, errOut)
	default:
		result, err := taskstore.RejoinBoardStatus(*dir)
		if err != nil {
			fmt.Fprintln(errOut, "read board rejoin status:", err)
			return 1
		}
		return writeRejoinBoardResult(result, out, errOut)
	}
}

type rejoinPrepareArgs struct {
	dir, source                         string
	clone                               bool
	rejoinID                            string
	targetNamespace, targetAuthority    string
	sourceExport                        string
	sourceFenced                        bool
	floors                              rejoinFloors
	reserved                            rejoinIDs
	planPath, payloadPath, capacityPath string
}

func runRejoinBoardPrepare(a rejoinPrepareArgs, out, errOut io.Writer) int {
	opts := taskstore.RejoinBoardOptions{
		Dir: a.dir, SourceOwner: a.source, Clone: a.clone, RejoinID: a.rejoinID,
		TargetNamespace: a.targetNamespace, TargetPolicyAuthority: a.targetAuthority,
		ReservationFloors: a.floors, AdditionalReservedIDs: a.reserved,
		SourceFencedNonempty: a.sourceFenced,
	}
	if a.sourceExport != "" {
		export, err := os.ReadFile(a.sourceExport)
		if err != nil {
			fmt.Fprintln(errOut, "read source export:", err)
			return 1
		}
		opts.SourceExport = export
	}
	result, planRaw, payload, capacity, err := taskstore.PrepareRejoinBoard(opts)
	if err != nil {
		fmt.Fprintln(errOut, "prepare board rejoin:", err)
		fmt.Fprintln(errOut, "Nothing was published. Correct the reported binding and run --prepare again.")
		return 1
	}
	// The plan and payload are written before anything is reported, so a
	// reported prepare is always one the operator can actually apply.
	for _, w := range []struct {
		path string
		raw  []byte
		mode os.FileMode
	}{{a.planPath, planRaw, 0o600}, {a.payloadPath, payload, 0o600}, {a.capacityPath, capacity, 0o600}} {
		if w.path == "" {
			continue
		}
		if err := os.WriteFile(w.path, w.raw, w.mode); err != nil {
			fmt.Fprintln(errOut, "write rejoin artifact:", err)
			return 1
		}
	}
	return writeRejoinBoardResult(result, out, errOut)
}

func writeRejoinBoardResult(result taskstore.RejoinBoardResult, out, errOut io.Writer) int {
	if err := outputformat.Encode(out, result); err != nil {
		fmt.Fprintln(errOut, "write result (the rejoin step may already have completed; re-read with --status):", err)
		return 1
	}
	return 0
}
