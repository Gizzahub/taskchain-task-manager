package taskflow

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	gateEvidenceMarker = "taskchain-gate-evidence"
	maxCILogLine       = 4 << 20
)

// noCILogError is the default-log miss. A ready gate ignores it; the command
// surfaces it.
type noCILogError struct {
	Pattern string
	Name    string
}

func (e *noCILogError) Error() string {
	return fmt.Sprintf("no CI log under %s: run `%s` first, or name one with --log", e.Pattern, e.Name)
}

type ciReceipt struct {
	StartLine   int
	FinishLine  int
	ResultLine  int
	RunID       string
	Head        string
	Tree        string
	ChangeID    string
	Worktree    string
	Result      string
	ExitCode    int
	FailLines   int
	OKLines     int
	SkipLines   int
	LogPath     string
	LogFinished time.Time
	resultRunID string
}

var (
	ciTimePrefix = regexp.MustCompile(`^\[[0-9]{2}:[0-9]{2}:[0-9]{2}\] `)
	ciEdge       = regexp.MustCompile(`^CI git (start|finish): run-id=(\S+) head=([0-9a-fA-F]{40}|[0-9a-fA-F]{64}) tree=([0-9a-fA-F]{40}|[0-9a-fA-F]{64})(?: change-id=(taskchain-gate/[0-9a-f]{32}|none))? worktree=(clean|dirty)$`)
	ciResult     = regexp.MustCompile(`^CI gate result: run-id=(\S+) result=(PASS|FAIL) exit=([0-9]+)$`)
	commitHashRe = regexp.MustCompile(`^[0-9a-fA-F]{7,40}$`)
	lineCountRe  = regexp.MustCompile(`^[0-9]+$`)
)

// ScanCILog reads one CI log. It strips a leading [HH:MM:SS] stamp, requires
// one start, one finish, and one result in that order, and counts test lines.
// A line longer than 4MiB is refused rather than buffered without a bound.
func ScanCILog(path string) (*ciReceipt, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	reader := bufio.NewReaderSize(f, 64*1024)
	receipt := &ciReceipt{LogPath: path, LogFinished: info.ModTime()}
	var starts, finishes, results, malformed int
	var startEdge, finishEdge ciEdgeFields
	lineNo := 0
	for {
		line, err := readBoundedLine(reader)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		lineNo++
		line = ciTimePrefix.ReplaceAllString(line, "")
		line = strings.TrimRight(line, "\r")
		switch {
		case strings.HasPrefix(line, "FAIL") || strings.HasPrefix(line, "--- FAIL"):
			receipt.FailLines++
		case strings.HasPrefix(line, "--- SKIP"):
			receipt.SkipLines++
		case strings.HasPrefix(line, "ok "):
			receipt.OKLines++
		}
		if strings.HasPrefix(line, "CI git start:") || strings.HasPrefix(line, "CI git finish:") {
			match := ciEdge.FindStringSubmatch(line)
			if match == nil {
				malformed++
				continue
			}
			edge := ciEdgeFields{line: lineNo, runID: match[2], head: strings.ToLower(match[3]), tree: strings.ToLower(match[4]), changeID: match[5], worktree: match[6]}
			if match[1] == "start" {
				starts++
				startEdge = edge
			} else {
				finishes++
				finishEdge = edge
			}
			continue
		}
		if strings.HasPrefix(line, "CI gate result:") {
			match := ciResult.FindStringSubmatch(line)
			if match == nil {
				malformed++
				continue
			}
			results++
			receipt.ResultLine = lineNo
			receipt.resultRunID = match[1]
			receipt.Result = match[2]
			receipt.ExitCode, _ = strconv.Atoi(match[3])
		}
	}
	if malformed > 0 {
		return nil, errors.New("CI log must contain exactly one valid start and finish git receipt: malformed")
	}
	if results != 1 {
		return nil, errors.New("no valid outer `CI gate result:` line")
	}
	if starts != 1 || finishes != 1 {
		return nil, errors.New("CI log must contain exactly one valid start and finish git receipt")
	}
	receipt.StartLine = startEdge.line
	receipt.FinishLine = finishEdge.line
	receipt.RunID = startEdge.runID
	receipt.Head = startEdge.head
	receipt.Tree = startEdge.tree
	receipt.ChangeID = startEdge.changeID
	receipt.Worktree = startEdge.worktree
	if !(receipt.StartLine < receipt.FinishLine && receipt.FinishLine < receipt.ResultLine) {
		return nil, errors.New("CI log must contain exactly one valid start and finish git receipt: out of order")
	}
	if startEdge.head != finishEdge.head || startEdge.tree != finishEdge.tree || startEdge.changeID != finishEdge.changeID || startEdge.worktree != finishEdge.worktree {
		return nil, errors.New("CI log must contain exactly one valid start and finish git receipt: start and finish disagree")
	}
	if startEdge.runID != finishEdge.runID || startEdge.runID != receipt.resultRunID {
		return nil, errors.New("CI log must contain exactly one valid start and finish git receipt: run-id disagree")
	}
	return receipt, nil
}

type ciEdgeFields struct {
	line     int
	runID    string
	head     string
	tree     string
	changeID string
	worktree string
}

func readBoundedLine(r *bufio.Reader) (string, error) {
	var buf bytes.Buffer
	for {
		chunk, err := r.ReadSlice('\n')
		if len(chunk) > 0 {
			if buf.Len()+len(chunk) > maxCILogLine {
				return "", errors.New("CI log line exceeds 4MiB")
			}
			buf.Write(chunk)
		}
		if err == bufio.ErrBufferFull {
			continue
		}
		if err != nil {
			if err == io.EOF && buf.Len() > 0 {
				return strings.TrimRight(buf.String(), "\n"), nil
			}
			return "", err
		}
		return strings.TrimRight(buf.String(), "\n"), nil
	}
}

// PrepareGateEvidence binds the declared log to HEAD and stores one receipt.
// block is the catalogue text, including its trailing newline. dest is the
// absolute durable copy.
func PrepareGateEvidence(ctx context.Context, cwd, logFlag string) (string, string, error) {
	root := evidenceRoot(ctx, cwd)
	command, err := loadGateCommand(root)
	if err != nil {
		return "", "", err
	}
	logPath, err := selectCILog(root, command, logFlag)
	if err != nil {
		return "", "", err
	}
	receipt, err := ScanCILog(logPath)
	if err != nil {
		return "", "", err
	}
	if _, ok := GitCommonDir(root); !ok {
		return "", "", fmt.Errorf("no git repository at %s: the durable copy has nowhere outside a working tree to live", root)
	}
	block, err := bindGateEvidence(ctx, root, command.Name, receipt)
	if err != nil {
		return "", "", err
	}
	if findings, _ := checkGateEvidenceText(block); len(findings) > 0 {
		return "", "", fmt.Errorf("rendered gate evidence failed validation: %s", findings[0].Message)
	}
	dest, err := publishGateBlock(root, block)
	if err != nil {
		return "", "", err
	}
	return block, dest, nil
}

// WriteReadyGateReceipt archives the same receipt a successful gate-evidence
// command would. A missing declaration or a missing log is not a verdict.
// Every other refusal is returned so the caller can print it without changing
// the gate status.
func WriteReadyGateReceipt(ctx context.Context, cwd string) error {
	_, _, err := PrepareGateEvidence(ctx, cwd, "")
	if err == nil || errors.Is(err, errGateUndeclared) {
		return nil
	}
	var missing *noCILogError
	if errors.As(err, &missing) {
		return nil
	}
	return err
}

func evidenceRoot(ctx context.Context, cwd string) string {
	top, err := ShowToplevel(ctx, cwd)
	if err != nil || top == "" {
		return cwd
	}
	return top
}

func selectCILog(root string, command gateCommand, logFlag string) (string, error) {
	if logFlag != "" {
		path := logFlag
		if !filepath.IsAbs(path) {
			path = filepath.Join(root, path)
		}
		info, err := os.Stat(path)
		if err != nil {
			return "", fmt.Errorf("%s: %w", logFlag, err)
		}
		if info.IsDir() {
			return "", fmt.Errorf("%s: is a directory", logFlag)
		}
		return path, nil
	}
	matches, err := filepath.Glob(filepath.Join(root, filepath.FromSlash(command.Log)))
	if err != nil {
		return "", fmt.Errorf("expand gate log %s: %w", command.Log, err)
	}
	var newest string
	var newestTime time.Time
	for _, match := range matches {
		info, err := os.Stat(match)
		if err != nil || info.IsDir() {
			continue
		}
		if newest == "" || info.ModTime().After(newestTime) || (info.ModTime().Equal(newestTime) && match > newest) {
			newest = match
			newestTime = info.ModTime()
		}
	}
	if newest == "" {
		return "", &noCILogError{Pattern: command.Log, Name: command.Name}
	}
	return newest, nil
}

func bindGateEvidence(ctx context.Context, root, gateName string, receipt *ciReceipt) (string, error) {
	head, err := gitOutput(ctx, root, "--no-replace-objects", "--no-lazy-fetch", "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return "", err
	}
	tree, err := gitOutput(ctx, root, "--no-replace-objects", "--no-lazy-fetch", "rev-parse", "--verify", "HEAD^{tree}")
	if err != nil {
		return "", err
	}
	head = strings.ToLower(strings.TrimSpace(head))
	tree = strings.ToLower(strings.TrimSpace(tree))
	if head != receipt.Head || tree != receipt.Tree {
		return "", fmt.Errorf("%s: CI receipt does not match current HEAD and tree", displayLog(root, receipt.LogPath))
	}
	subject, err := gitOutput(ctx, root, "--no-replace-objects", "--no-lazy-fetch", "log", "-1", "--format=%s", "HEAD")
	if err != nil {
		return "", err
	}
	subject = strings.TrimSpace(subject)
	if subject == "" {
		return "", errors.New("HEAD commit subject is empty")
	}
	changeID, err := BranchChangeID(root)
	if err != nil {
		return "", fmt.Errorf("branch Change-Id: %s", err.Error())
	}
	if receipt.ChangeID == "" || receipt.ChangeID == "none" || receipt.ChangeID != changeID {
		return "", fmt.Errorf("%s: CI receipt does not match branch Change-Id", displayLog(root, receipt.LogPath))
	}
	if receipt.Result != "PASS" {
		return "", fmt.Errorf("CI log result is FAIL (exit %d); evidence requires a clean PASS after the commit", receipt.ExitCode)
	}
	if receipt.ExitCode != 0 {
		return "", fmt.Errorf("CI log result is PASS (exit %d); evidence requires exit 0", receipt.ExitCode)
	}
	dated, err := gitOutput(ctx, root, "--no-replace-objects", "--no-lazy-fetch", "log", "-1", "--format=%cI", "HEAD")
	if err != nil {
		return "", err
	}
	commitDated, err := time.Parse(time.RFC3339, strings.TrimSpace(dated))
	if err != nil {
		return "", fmt.Errorf("commit date %q is not RFC3339", strings.TrimSpace(dated))
	}
	verdict := deriveGatesTheCommit(receipt.LogFinished, commitDated, receipt.Worktree, "yes")
	if !strings.HasPrefix(verdict, "yes") {
		return "", errors.New(verdict)
	}
	rel := displayLog(root, receipt.LogPath)
	block := renderGateBlock(gateBlockInput{
		Gate: commandOr(gateName), Name: gateName, Subject: subject, ChangeID: changeID,
		Result: receipt.Result, ExitCode: receipt.ExitCode,
		Fail: receipt.FailLines, OK: receipt.OKLines, Skip: receipt.SkipLines,
		LogFinished: receipt.LogFinished, CommitDated: commitDated, Verdict: verdict,
		Worktree: receipt.Worktree, Source: fmt.Sprintf("%s:%d — machine-local, reclaimed with its worktree", rel, receipt.ResultLine),
	})
	return block, nil
}

func commandOr(name string) string {
	if strings.TrimSpace(name) == "" {
		return "gate"
	}
	return name
}

func displayLog(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil || strings.HasPrefix(rel, "..") {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(rel)
}

type gateBlockInput struct {
	Gate, Name, Subject, ChangeID, Result, Verdict, Worktree, Source string
	ExitCode, Fail, OK, Skip                                         int
	LogFinished, CommitDated                                         time.Time
}

func renderGateBlock(in gateBlockInput) string {
	var buf strings.Builder
	write := func(key, value string) {
		fmt.Fprintf(&buf, "%s: %s\n", key, value)
	}
	write("gate-evidence", gateEvidenceMarker)
	write("schema-version", "2")
	write("gate", in.Name)
	write("commit", in.Subject)
	write("change-id", in.ChangeID)
	write("result", fmt.Sprintf("%s (exit %d)", in.Result, in.ExitCode))
	write("fail-lines", strconv.Itoa(in.Fail))
	write("ok-lines", strconv.Itoa(in.OK))
	write("skip-lines", strconv.Itoa(in.Skip))
	write("log-finished", in.LogFinished.Format(time.RFC3339))
	write("commit-dated", in.CommitDated.Format(time.RFC3339))
	write("gates-the-commit", in.Verdict)
	write("worktree", in.Worktree)
	write("tested-tree", "yes")
	write("source", in.Source)
	return buf.String()
}

func deriveGatesTheCommit(logFinished, commitDated time.Time, worktree, tested string) string {
	if logFinished.Unix() == commitDated.Unix() {
		return "no — log finished within the commit's second"
	}
	if logFinished.Before(commitDated) {
		return "no — log finished before the commit; this gates the content, not the tip"
	}
	if worktree == "dirty" {
		return "no — the worktree was dirty, so the run gated uncommitted content, not the commit's tree"
	}
	if tested != "yes" {
		return "no — the ordered CI receipts do not bind this HEAD, tree, and Change-Id"
	}
	return "yes — log finished after the commit, over a clean worktree"
}

func publishGateBlock(root, block string) (string, error) {
	dir, err := gateStoreDir(root)
	if err != nil {
		return "", err
	}
	name, err := gateReceiptName(block)
	if err != nil {
		return "", fmt.Errorf("cannot name the gate receipt: %w", err)
	}
	return publishGateFile(dir, name, []byte(block))
}

func gateStoreDir(root string) (string, error) {
	if strings.TrimSpace(root) == "" {
		return "", errors.New("no git repository: the durable copy has nowhere outside a working tree to live")
	}
	common, ok := GitCommonDir(root)
	if !ok {
		return "", fmt.Errorf("no git repository at %s: the durable copy has nowhere outside a working tree to live", root)
	}
	return filepath.Join(common, "taskchain", "gates"), nil
}

func gateReceiptName(block string) (string, error) {
	fields := map[string]string{}
	for _, line := range strings.Split(strings.TrimRight(block, "\n"), "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		fields[strings.TrimSpace(key)] = strings.TrimSpace(value)
	}
	changeID := fields["change-id"]
	if !changeIDPattern.MatchString(changeID) {
		return "", fmt.Errorf("change-id %q", changeID)
	}
	logFinished, err := time.Parse(time.RFC3339, fields["log-finished"])
	if err != nil {
		return "", err
	}
	commitDated, err := time.Parse(time.RFC3339, fields["commit-dated"])
	if err != nil {
		return "", err
	}
	slug := strings.NewReplacer("/", "-", ".", "-", "_", "-").Replace(strings.ToLower(changeID))
	sum := sha256.Sum256([]byte(block))
	return fmt.Sprintf("%s__%s__%s__%s.txt", slug, logFinished.UTC().Format("20060102T150405Z"), commitDated.UTC().Format("20060102T150405Z"), hex.EncodeToString(sum[:])[:12]), nil
}

func publishGateFile(dir, name string, block []byte) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	dest := filepath.Join(dir, name)
	if existing, err := os.ReadFile(dest); err == nil {
		if bytes.Equal(existing, block) {
			return dest, nil
		}
		return "", fmt.Errorf("refuse to overwrite different gate evidence at %s", dest)
	} else if !os.IsNotExist(err) {
		return "", err
	}
	tmp, err := os.CreateTemp(dir, ".gate-evidence-*")
	if err != nil {
		return "", err
	}
	tmpName := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := tmp.Write(block); err != nil {
		_ = tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := os.Link(tmpName, dest); err != nil {
		if !os.IsExist(err) {
			return "", err
		}
		existing, readErr := os.ReadFile(dest)
		if readErr == nil && bytes.Equal(existing, block) {
			return dest, nil
		}
		return "", fmt.Errorf("refuse to overwrite different gate evidence at %s", dest)
	}
	cleanup = false
	_ = os.Remove(tmpName)
	return dest, nil
}
