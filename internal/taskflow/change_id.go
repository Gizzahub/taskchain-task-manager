package taskflow

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// changeIDValue is the only Change-Id form this product reads or writes.
var changeIDValue = regexp.MustCompile(`^taskchain-gate/[0-9a-f]{32}$`)

// ChangeIDFromCommitMessage checks one commit message that already contains a
// Change-Id line. The checks run in contract order: Model is the last
// nonempty line, Change-Id sits on the nonempty line before it, the message
// carries exactly one Change-Id, and that value is a taskchain-gate id.
func ChangeIDFromCommitMessage(message string) (string, error) {
	lines := nonemptyLines(message)
	if len(lines) == 0 || !modelTrailer(lines[len(lines)-1]) {
		return "", errors.New("last nonempty trailer must be Model")
	}
	if len(lines) < 2 || !strings.HasPrefix(strings.TrimSpace(lines[len(lines)-2]), "Change-Id:") {
		return "", errors.New("Change-Id must immediately precede Model")
	}
	count := 0
	value := ""
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "Change-Id:") {
			continue
		}
		count++
		value = strings.TrimSpace(strings.TrimPrefix(trimmed, "Change-Id:"))
	}
	if count != 1 {
		return "", fmt.Errorf("need exactly one Change-Id trailer, found %d", count)
	}
	if !changeIDValue.MatchString(value) {
		return "", fmt.Errorf("malformed Change-Id %q", value)
	}
	return value, nil
}

func nonemptyLines(message string) []string {
	var lines []string
	for _, line := range strings.Split(message, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		lines = append(lines, strings.TrimRight(line, "\r"))
	}
	return lines
}

func modelTrailer(line string) bool {
	rest, ok := strings.CutPrefix(strings.TrimSpace(line), "Model:")
	return ok && strings.TrimSpace(rest) != ""
}

func commitHasChangeID(message string) bool {
	for _, line := range strings.Split(message, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "Change-Id:") {
			return true
		}
	}
	return false
}

func commitSubject(message string) string {
	subject, _, _ := strings.Cut(message, "\n")
	subject = strings.TrimSpace(subject)
	if subject == "" {
		return "(empty subject)"
	}
	return subject
}

// ChangeIDFromBranchRange applies the commit rules to every commit in
// peer..HEAD that carries a Change-Id line, then requires the range to
// declare exactly one such trailer.
func ChangeIDFromBranchRange(ctx context.Context, root, peer string) (string, error) {
	base, err := gitOutput(ctx, root, "--no-replace-objects", "--no-lazy-fetch", "merge-base", peer, "HEAD")
	if err != nil {
		return "", err
	}
	listed, err := gitOutput(ctx, root, "--no-replace-objects", "--no-lazy-fetch", "rev-list", "--reverse", strings.TrimSpace(base)+"..HEAD")
	if err != nil {
		return "", err
	}
	label := peerRangeLabel(peer)
	var ids []string
	for _, sha := range strings.Fields(listed) {
		message, err := gitOutput(ctx, root, "--no-replace-objects", "--no-lazy-fetch", "log", "-1", "--format=%B", sha)
		if err != nil {
			return "", err
		}
		if !commitHasChangeID(message) {
			continue
		}
		id, err := ChangeIDFromCommitMessage(message)
		if err != nil {
			return "", fmt.Errorf("commit %q: %w", commitSubject(message), err)
		}
		ids = append(ids, id)
	}
	switch len(ids) {
	case 0:
		return "", fmt.Errorf("no Change-Id trailer in the branch range: %s..HEAD", label)
	case 1:
		return ids[0], nil
	default:
		return "", fmt.Errorf("branch range declares %d Change-Id trailers (%s)", len(ids), strings.Join(ids, ", "))
	}
}

// BranchChangeID resolves the source branch, chooses the peer tip, and reads
// the single Change-Id in merge-base(peer, HEAD)..HEAD. Range failures keep
// the peer span so a caller can see which history was judged.
func BranchChangeID(ctx context.Context, root string) (string, error) {
	branch, err := sourceBranchName(ctx, root)
	if err != nil {
		return "", err
	}
	peer, err := selectSourcePeer(ctx, root, branch)
	if err != nil {
		return "", err
	}
	id, err := ChangeIDFromBranchRange(ctx, root, peer)
	if err != nil {
		return "", fmt.Errorf("%s (range %s..HEAD)", err.Error(), peerRangeLabel(peer))
	}
	return id, nil
}

func peerRangeLabel(peer string) string {
	peer = strings.TrimPrefix(peer, "refs/remotes/")
	peer = strings.TrimPrefix(peer, "refs/heads/")
	return peer
}

func sourceBranchName(ctx context.Context, root string) (string, error) {
	declared, err := declaredIntegrationBranch(root)
	if err != nil {
		return "", err
	}
	if declared != "" {
		return declared, nil
	}
	out, err := gitOutput(ctx, root, "symbolic-ref", "--short", "refs/remotes/origin/HEAD")
	if err != nil {
		return "", errors.New("source branch is not declared and origin/HEAD is unresolved")
	}
	parts := strings.Split(strings.TrimSpace(out), "/")
	name := parts[len(parts)-1]
	if name == "" {
		return "", errors.New("source branch is not declared and origin/HEAD is unresolved")
	}
	return name, nil
}

func declaredIntegrationBranch(root string) (string, error) {
	path := filepath.Join(root, ".gz-git.yaml")
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("%s exists but cannot be read: %w", path, err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return "", fmt.Errorf("parse .gz-git.yaml: %w", err)
	}
	branch, _ := doc["branch"].(map[string]any)
	if branch == nil {
		return "", nil
	}
	switch value := branch["integrationBranch"].(type) {
	case string:
		return strings.TrimSpace(value), nil
	case []any:
		for _, item := range value {
			text := strings.TrimSpace(scalarString(item))
			if text != "" {
				return text, nil
			}
		}
	}
	return "", nil
}

func selectSourcePeer(ctx context.Context, root, branch string) (string, error) {
	originRef := "refs/remotes/origin/" + branch
	localRef := "refs/heads/" + branch
	originSHA, originOK := verifyCommit(ctx, root, originRef)
	localSHA, localOK := verifyCommit(ctx, root, localRef)
	switch {
	case !originOK && !localOK:
		return "", fmt.Errorf("source branch %q resolves neither on origin nor locally; look for %s or %s", branch, originRef, localRef)
	case originOK && !localOK:
		return originRef, nil
	case !originOK && localOK:
		return localRef, nil
	case originSHA == localSHA:
		return originRef, nil
	}
	closer, ordered := closerPeer(ctx, root, originRef, localRef)
	if !ordered {
		return "", fmt.Errorf("source branch %q resolves on both origin and locally but the tips are not ordered", branch)
	}
	return closer, nil
}

func verifyCommit(ctx context.Context, root, ref string) (string, bool) {
	out, err := gitOutput(ctx, root, "--no-replace-objects", "--no-lazy-fetch", "rev-parse", "--verify", "--quiet", ref+"^{commit}")
	out = strings.TrimSpace(out)
	if err != nil || out == "" {
		return "", false
	}
	return out, true
}

// closerPeer picks the tip with fewer commits ahead of HEAD. Equal distance
// falls back to the descendant, which is the tip HEAD has actually reached.
// Unrelated tips are not ordered.
func closerPeer(ctx context.Context, root, originRef, localRef string) (string, bool) {
	if !tipsOrdered(ctx, root, originRef, localRef) {
		return "", false
	}
	originDistance, originErr := commitsAhead(ctx, root, originRef)
	localDistance, localErr := commitsAhead(ctx, root, localRef)
	if originErr != nil || localErr != nil {
		return "", false
	}
	if originDistance != localDistance {
		if originDistance < localDistance {
			return originRef, true
		}
		return localRef, true
	}
	if ancestorCommit(ctx, root, originRef, localRef) {
		return localRef, true
	}
	if ancestorCommit(ctx, root, localRef, originRef) {
		return originRef, true
	}
	return "", false
}

func tipsOrdered(ctx context.Context, root, originRef, localRef string) bool {
	return ancestorCommit(ctx, root, originRef, localRef) || ancestorCommit(ctx, root, localRef, originRef)
}

func ancestorCommit(ctx context.Context, root, ancestor, descendant string) bool {
	_, err := gitOutput(ctx, root, "--no-replace-objects", "--no-lazy-fetch", "merge-base", "--is-ancestor", ancestor, descendant)
	return err == nil
}

func commitsAhead(ctx context.Context, root, tip string) (int, error) {
	out, err := gitOutput(ctx, root, "--no-replace-objects", "--no-lazy-fetch", "rev-list", "--count", tip+"..HEAD")
	if err != nil {
		return 0, err
	}
	n, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil {
		return 0, err
	}
	return n, nil
}
