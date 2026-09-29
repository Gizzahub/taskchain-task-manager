package taskflow

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// probeTimeout bounds each criterion probe.
const probeTimeout = 5 * time.Second

// probeAllowlist is the set of binaries a criterion binding may run. Probes
// run in the caller's working tree with no shell, so anything a shell would
// interpret must be refused rather than escaped.
var probeAllowlist = map[string]bool{
	"test": true, "[": true, "ls": true, "grep": true, "rg": true, "find": true,
}

// findActionFlags are the find(1) primaries that act on the filesystem, not
// just print: a bound that deletes or executes while "proving" a criterion is
// not a read.
var findActionFlags = map[string]bool{
	"-delete": true, "-exec": true, "-execdir": true, "-ok": true, "-okdir": true, "-fprint": true,
}

// rgExecFlags are the rg flags that run a command of their own: --pre filters
// every searched file through a command and --pager pipes the output through
// one. The pinned reference boundary at bb970b24 filters find's action
// primaries but passes these; this product honors the boundary's declared
// rule instead — read-only file predicates and nothing else — because
// inverting rg's exit code with a ! does not uninvent the command it ran, so
// unlike a negated find primary the negation carve-out does not transfer.
var rgExecFlags = map[string]bool{
	"--pre": true, "--pager": true,
}

// shellMetacharacters end a probe's read-only guarantee the moment one
// appears outside quotes.
const shellMetacharacters = ";&|<>$`()"

// splitProbeArgs splits a command line into words the way a shell would,
// honoring quotes and refusing everything this boundary does not run:
// metacharacters, globs, and backslashes.
func splitProbeArgs(cmd string) ([]string, error) {
	var args []string
	var current strings.Builder
	var quoted rune
	for i := 0; i < len(cmd); i++ {
		c := cmd[i]
		switch {
		case quoted != 0:
			if c == '\\' {
				return nil, fmt.Errorf("backslash escapes are not supported in probes")
			}
			if rune(c) == quoted {
				quoted = 0
				continue
			}
			current.WriteByte(c)
		case c == '"' || c == '\'':
			quoted = rune(c)
		case c == '\\':
			return nil, fmt.Errorf("backslash escapes are not supported in probes")
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			if current.Len() > 0 {
				args = append(args, current.String())
				current.Reset()
			}
		case strings.ContainsRune(shellMetacharacters, rune(c)):
			return nil, fmt.Errorf("probe contains shell metacharacter %q", string(c))
		case c == '*' || c == '?':
			return nil, fmt.Errorf("probe contains glob metacharacter %q", string(c))
		default:
			current.WriteByte(c)
		}
	}
	if quoted != 0 {
		return nil, fmt.Errorf("probe has an unterminated quote")
	}
	if current.Len() > 0 {
		args = append(args, current.String())
	}
	return args, nil
}

// classifyProbe reports whether a binding command is one this boundary runs:
// an allowed binary, no acting find primaries, and an explicit negation only
// when it is spelled with a following space.
func classifyProbe(cmd string) error {
	trimmed := strings.TrimSpace(cmd)
	negated := false
	if rest, ok := strings.CutPrefix(trimmed, "!"); ok {
		if !strings.HasPrefix(rest, " ") {
			return fmt.Errorf("a leading ! must be followed by a space")
		}
		negated = true
		trimmed = strings.TrimSpace(rest)
	}
	if trimmed == "" {
		return fmt.Errorf("probe is empty")
	}
	args, err := splitProbeArgs(trimmed)
	if err != nil {
		return err
	}
	if len(args) == 0 {
		return fmt.Errorf("probe is empty")
	}
	if !probeAllowlist[args[0]] {
		return fmt.Errorf("probe binary %q is not in the probe allowlist (test, [, ls, grep, rg, find)", args[0])
	}
	if args[0] == "find" && !negated {
		for _, arg := range args[1:] {
			if findActionFlags[arg] {
				return fmt.Errorf("find action %q may not run as a probe", arg)
			}
		}
	}
	if args[0] == "rg" {
		for _, arg := range args[1:] {
			if rgExecFlags[arg] {
				return fmt.Errorf("rg flag %q may not run as a probe: it executes a command", arg)
			}
		}
	}
	return nil
}

// probeRoot resolves the directory probes run in and requires it to be a git
// work tree: probes answer questions about a tracked tree, and a directory
// with no .git has no tree to answer about.
func probeRoot(root string) (string, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(abs + string(os.PathSeparator) + ".git"); err != nil {
		return "", errors.New("criterion probes need a git repository root")
	}
	return abs, nil
}

// execProbe runs one allowed command and reports what it showed: true when it
// exited zero, false when it exited nonzero, and an error when it could not
// start at all (a start failure means the probe said nothing either way).
func execProbe(ctx context.Context, root string, cmd string) (passed bool, executable bool, err error) {
	if err := classifyProbe(cmd); err != nil {
		return false, false, err
	}
	args, err := splitProbeArgs(strings.TrimSpace(cmd))
	if err != nil {
		return false, false, err
	}
	probeCtx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	probe := exec.CommandContext(probeCtx, args[0], args[1:]...)
	probe.Dir = root
	probe.Stdout = nil
	probe.Stderr = nil
	if err := probe.Run(); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return false, true, nil
		}
		return false, false, err
	}
	return true, true, nil
}

// vacuousCriteria reports the criteria whose command bindings already pass on
// the current tree. Only todo-zone cards are probed: a criterion is a promise
// about work not yet done, and only an unstarted card can still promise
// something the tree has not made true.
func vacuousCriteria(ctx context.Context, root, zone string, criteria []Criterion) ([]Criterion, error) {
	if zone != StatusPending.Dir() {
		return nil, nil
	}
	probeDir, err := probeRoot(root)
	if err != nil {
		return nil, nil // probes need a repository; without one nothing is probed
	}
	var vacuous []Criterion
	for _, criterion := range criteria {
		if criterion.Checked || criterion.Command == "" {
			continue
		}
		if err := classifyProbe(criterion.Command); err != nil {
			continue
		}
		passed, executable, err := execProbe(ctx, probeDir, criterion.Command)
		if err != nil || !executable {
			continue
		}
		if passed {
			vacuous = append(vacuous, criterion)
		}
	}
	return vacuous, nil
}
