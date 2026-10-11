package taskflow

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// requiredGateFields is the catalogue F set. skip-lines may be absent.
var requiredGateFields = []string{
	"gate-evidence", "schema-version", "gate", "commit", "change-id", "result",
	"fail-lines", "ok-lines", "log-finished", "commit-dated", "gates-the-commit",
	"worktree", "tested-tree", "source",
}

type evidenceLine struct {
	Number int
	Key    string
	Value  string
	Raw    string
}

type evidenceBlock struct {
	lines []evidenceLine
}

// CheckGateEvidence applies catalogue F to every evidence block in content.
// FAIL and dirty are not valid forms. The receipt store is only the
// taskchain/gates directory of the git common dir; older names are not read.
func CheckGateEvidence(ctx context.Context, root, content string) (errs, warnings []Finding) {
	for _, block := range findEvidenceBlocks(content) {
		form := checkEvidenceBlock(block)
		if len(form) > 0 {
			errs = append(errs, form...)
			continue
		}
		finding, warning := verifyEvidenceReceipt(ctx, root, block)
		if finding.Message == "" {
			continue
		}
		if warning {
			warnings = append(warnings, finding)
		} else {
			errs = append(errs, finding)
		}
	}
	return errs, warnings
}

func checkGateEvidenceText(block string) ([]Finding, []Finding) {
	return CheckGateEvidence(context.Background(), "", block)
}

func findEvidenceBlocks(content string) []evidenceBlock {
	rawLines := strings.Split(content, "\n")
	var blocks []evidenceBlock
	for i := 0; i < len(rawLines); i++ {
		key, _, ok := parseEvidenceField(rawLines[i])
		if !ok || (key != "gate-evidence" && key != "gate") {
			continue
		}
		var lines []evidenceLine
		for j := i; j < len(rawLines); j++ {
			field, value, fieldOK := parseEvidenceField(rawLines[j])
			if !fieldOK {
				break
			}
			lines = append(lines, evidenceLine{Number: j + 1, Key: field, Value: value, Raw: strings.TrimRight(rawLines[j], "\r")})
		}
		if len(lines) == 0 {
			continue
		}
		if evidenceRunIsBlock(lines) {
			blocks = append(blocks, evidenceBlock{lines: lines})
		}
		i += len(lines) - 1
	}
	return blocks
}

func evidenceRunIsBlock(lines []evidenceLine) bool {
	if len(lines) >= 3 {
		return true
	}
	for _, line := range lines {
		if line.Key == "gate-evidence" || line.Value == gateEvidenceMarker {
			return true
		}
	}
	return false
}

func parseEvidenceField(line string) (string, string, bool) {
	trimmed := strings.TrimSpace(line)
	trimmed = strings.TrimPrefix(trimmed, "- ")
	trimmed = strings.TrimPrefix(trimmed, "* ")
	if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "```") {
		return "", "", false
	}
	key, value, ok := strings.Cut(trimmed, ":")
	if !ok || key == "" || strings.ContainsAny(key, " \t") {
		return "", "", false
	}
	return key, strings.TrimSpace(value), true
}

func checkEvidenceBlock(block evidenceBlock) []Finding {
	seen := map[string]int{}
	var findings []Finding
	values := map[string]string{}
	numbers := map[string]int{}
	for _, line := range block.lines {
		if previous, ok := seen[line.Key]; ok {
			findings = append(findings, evidenceFinding(line.Number, line.Key, fmt.Sprintf("is written more than once; first seen on line %d", previous)))
			continue
		}
		seen[line.Key] = line.Number
		if !knownEvidenceField(line.Key) {
			findings = append(findings, evidenceFinding(line.Number, line.Key, "is unknown; gate evidence schemas fail closed"))
			continue
		}
		values[line.Key] = line.Value
		numbers[line.Key] = line.Number
		if line.Value == "" {
			findings = append(findings, evidenceFinding(line.Number, line.Key, "missing or empty"))
		}
	}
	first := block.lines[0].Number
	for _, field := range requiredGateFields {
		if _, ok := seen[field]; ok {
			continue
		}
		findings = append(findings, evidenceFinding(first, field, "missing or empty"))
	}
	if value, ok := values["gate-evidence"]; ok && value != "" && value != gateEvidenceMarker {
		findings = append(findings, evidenceFinding(numbers["gate-evidence"], "gate-evidence", "must be taskchain-gate-evidence"))
	}
	if value, ok := values["schema-version"]; ok && value != "" && value != "2" {
		findings = append(findings, evidenceFinding(numbers["schema-version"], "schema-version", "must be supported schema version 2: "+value))
	}
	if value, ok := values["commit"]; ok && commitHashRe.MatchString(value) {
		findings = append(findings, evidenceFinding(numbers["commit"], "commit", "is a commit hash (7-40 hex); a rebase deletes it, so this field carries the commit subject"))
	}
	if value, ok := values["change-id"]; ok && value != "" && !changeIDPattern.MatchString(value) {
		findings = append(findings, evidenceFinding(numbers["change-id"], "change-id", "is not a safe Change-Id"))
	}
	for _, field := range []string{"fail-lines", "ok-lines", "skip-lines"} {
		value, ok := values[field]
		if !ok || value == "" {
			continue
		}
		if !lineCountRe.MatchString(value) {
			findings = append(findings, evidenceFinding(numbers[field], field, "is not a line count"))
		}
	}
	if value, ok := values["result"]; ok && value != "" {
		findings = append(findings, checkEvidenceResult(numbers["result"], value, values["fail-lines"])...)
	}
	for _, field := range []string{"log-finished", "commit-dated"} {
		value, ok := values[field]
		if !ok || value == "" {
			continue
		}
		if _, err := time.Parse(time.RFC3339, value); err != nil {
			findings = append(findings, evidenceFinding(numbers[field], field, "is not an RFC3339 timestamp"))
		}
	}
	if value, ok := values["worktree"]; ok && value != "" && value != "clean" {
		findings = append(findings, evidenceFinding(numbers["worktree"], "worktree", "is not clean"))
	}
	if value, ok := values["tested-tree"]; ok && value != "" && value != "yes" && value != "no" {
		findings = append(findings, evidenceFinding(numbers["tested-tree"], "tested-tree", "is neither yes nor no"))
	}
	if value, ok := values["gates-the-commit"]; ok && value != "" {
		if !strings.HasPrefix(value, "yes") && !strings.HasPrefix(value, "no") {
			findings = append(findings, evidenceFinding(numbers["gates-the-commit"], "gates-the-commit", "does not start with yes or no"))
		} else if derived, ok := derivedFromBlock(values); ok && value != derived {
			findings = append(findings, evidenceFinding(numbers["gates-the-commit"], "gates-the-commit", "claims "+value+", but the block derives: "+derived))
		}
	}
	return findings
}

func knownEvidenceField(key string) bool {
	switch key {
	case "gate-evidence", "schema-version", "gate", "commit", "change-id", "result",
		"fail-lines", "ok-lines", "skip-lines", "log-finished", "commit-dated",
		"gates-the-commit", "worktree", "tested-tree", "source":
		return true
	default:
		return false
	}
}

func checkEvidenceResult(line int, value, fails string) []Finding {
	word, exitCode, ok := parseResultField(value)
	if !ok {
		return []Finding{evidenceFinding(line, "result", "is not `PASS (exit N)`")}
	}
	if word != "PASS" {
		if word == "FAIL" {
			return []Finding{evidenceFinding(line, "result", "is not `PASS (exit N)`")}
		}
		return []Finding{evidenceFinding(line, "result", "says "+word+" beside exit "+exitCode)}
	}
	if n, err := strconv.Atoi(fails); err == nil && n > 0 {
		return []Finding{evidenceFinding(line, "result", fmt.Sprintf("says PASS beside %d counted failures", n))}
	}
	return nil
}

func parseResultField(value string) (string, string, bool) {
	word, rest, ok := strings.Cut(value, " ")
	if !ok || !strings.HasPrefix(rest, "(exit ") || !strings.HasSuffix(rest, ")") {
		return "", "", false
	}
	exitCode := strings.TrimSuffix(strings.TrimPrefix(rest, "(exit "), ")")
	if !lineCountRe.MatchString(exitCode) {
		return "", "", false
	}
	return word, exitCode, true
}

func derivedFromBlock(values map[string]string) (string, bool) {
	logFinished, err := time.Parse(time.RFC3339, values["log-finished"])
	if err != nil {
		return "", false
	}
	commitDated, err := time.Parse(time.RFC3339, values["commit-dated"])
	if err != nil {
		return "", false
	}
	return deriveGatesTheCommit(logFinished, commitDated, values["worktree"], values["tested-tree"]), true
}

func evidenceFinding(line int, field, message string) Finding {
	return Finding{Field: "gate-evidence", Message: fmt.Sprintf("line %d: `%s` %s", line, field, message)}
}

func verifyEvidenceReceipt(ctx context.Context, root string, block evidenceBlock) (Finding, bool) {
	raw := evidenceBytes(block)
	name, err := gateReceiptName(string(raw))
	if err != nil {
		return Finding{"gate-evidence", "cannot name the gate receipt: " + err.Error()}, false
	}
	dir, err := gateStoreDir(root)
	if err != nil {
		return Finding{"gate-evidence", "not verified — this machine keeps no gate receipts, so the block is taken at its word"}, true
	}
	path := dir + string(os.PathSeparator) + name
	body, err := os.ReadFile(path)
	if err == nil {
		if string(body) == string(raw) {
			return Finding{}, false
		}
		return Finding{"gate-evidence", "no gate receipt " + name + " — the stored bytes differ"}, false
	}
	if !os.IsNotExist(err) {
		return Finding{"gate-evidence", "cannot read gate receipt " + name + ": " + err.Error()}, false
	}
	changeID := ""
	for _, line := range block.lines {
		if line.Key == "change-id" {
			changeID = line.Value
		}
	}
	source, integrated, lookErr := changeIDIntegrated(ctx, root, changeID)
	if lookErr != nil {
		return Finding{"gate-evidence", fmt.Sprintf("no gate receipt %s — publish it from the gating worktree (whether the change is already integrated could not be determined: %s)", name, lookErr.Error())}, false
	}
	if integrated {
		return Finding{"gate-evidence", "not verified — the change was integrated into " + source + " and its receipt lives on the device that gated it"}, true
	}
	return Finding{"gate-evidence", "no gate receipt " + name + " — publish it from the gating worktree"}, false
}

func evidenceBytes(block evidenceBlock) []byte {
	var buf strings.Builder
	for _, line := range block.lines {
		buf.WriteString(line.Raw)
		buf.WriteByte('\n')
	}
	return []byte(buf.String())
}

func changeIDIntegrated(ctx context.Context, root, changeID string) (string, bool, error) {
	if !changeIDPattern.MatchString(changeID) {
		return "", false, fmt.Errorf("change-id %q", changeID)
	}
	source, err := sourceBranch(root)
	if err != nil {
		return "", false, err
	}
	peer, err := sourceBranchRef(root, source)
	if err != nil {
		return source, false, err
	}
	out, err := gitOutput(ctx, root, "--no-replace-objects", "--no-lazy-fetch", "log", peer, "--format=%B%x1e")
	if err != nil {
		return source, false, err
	}
	return source, strings.Contains(out, "Change-Id: "+changeID), nil
}
