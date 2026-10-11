package taskflow

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// changeIDTrailerLine matches one trailer line. Placement is checked separately
// by ChangeIDFromCommitMessage. The match is case-sensitive and line-anchored;
// a prose mention of the key is not a trailer.
var changeIDTrailerLine = regexp.MustCompile(`(?m)^Change-Id:[ \t]*(.*)$`)

// changeIDPattern is the product Change-Id. The ce-gate prefix is not accepted.
var changeIDPattern = regexp.MustCompile(`^taskchain-gate/[0-9a-f]{32}$`)

// ChangeIDFromCommitMessage reads the one Change-Id a single commit declares.
//
// The last nonempty line must be `Model: <nonempty>`. The previous nonempty
// line must be exactly `Change-Id: <value>`. Blank lines between those two
// are skipped. Every matching trailer line in the message is counted, and
// any count other than one is refused before the value is accepted.
func ChangeIDFromCommitMessage(message string) (string, error) {
	lines := strings.Split(strings.TrimRight(message, "\n"), "\n")
	last := len(lines) - 1
	for last >= 0 && strings.TrimSpace(lines[last]) == "" {
		last--
	}
	if last < 1 || !strings.HasPrefix(lines[last], "Model: ") || strings.TrimSpace(strings.TrimPrefix(lines[last], "Model: ")) == "" {
		return "", errors.New("last nonempty trailer must be Model")
	}
	previous := last - 1
	for previous >= 0 && strings.TrimSpace(lines[previous]) == "" {
		previous--
	}
	if previous < 0 {
		return "", errors.New("Change-Id must immediately precede Model")
	}
	matches := changeIDTrailerLine.FindAllStringSubmatch(message, -1)
	if len(matches) != 1 {
		return "", fmt.Errorf("need exactly one Change-Id trailer, found %d", len(matches))
	}
	value := strings.TrimSpace(matches[0][1])
	if lines[previous] != "Change-Id: "+value {
		return "", errors.New("Change-Id must immediately precede Model")
	}
	if !changeIDPattern.MatchString(value) {
		return "", fmt.Errorf("malformed Change-Id %q", value)
	}
	return value, nil
}

// ChangeIDFromBranchRange returns the one Change-Id declared by messages in a
// branch range. Messages with no Change-Id line are skipped. A repeated value
// is still two declarations. Commit failures name the subject, or
// "(empty subject)" when the first line is blank.
func ChangeIDFromBranchRange(messages []string) (string, error) {
	var found []string
	for _, message := range messages {
		if len(changeIDTrailerLine.FindAllStringIndex(message, 1)) == 0 {
			continue
		}
		value, err := ChangeIDFromCommitMessage(message)
		if err != nil {
			return "", fmt.Errorf("commit %q: %w", commitSubject(message), err)
		}
		found = append(found, value)
	}
	switch len(found) {
	case 1:
		return found[0], nil
	case 0:
		return "", errors.New("no Change-Id trailer in the branch range: the branch has not declared a change to gate")
	default:
		return "", fmt.Errorf("branch range declares %d Change-Id trailers (%s): exactly one commit must declare the change",
			len(found), strings.Join(found, ", "))
	}
}

func commitSubject(message string) string {
	subject := strings.TrimSpace(strings.SplitN(message, "\n", 2)[0])
	if subject == "" {
		return "(empty subject)"
	}
	return subject
}

// BranchChangeID resolves the Change-Id added after the merge-base with the
// source branch. The source is branch.integrationBranch in .gz-git.yaml, then
// the last component of origin/HEAD. When both the origin and local refs of
// that name exist, the ref whose merge-base is nearer to HEAD is used. The
// range suffix on a declaration error names that ref, not the merge-base.
func BranchChangeID(root string) (string, error) {
	source, err := sourceBranch(root)
	if err != nil {
		return "", err
	}
	peer, err := sourceBranchRef(root, source)
	if err != nil {
		return "", err
	}
	base, err := mergeBase(root, peer)
	if err != nil {
		return "", err
	}
	messages, err := rangeMessages(root, base)
	if err != nil {
		return "", err
	}
	changeID, err := ChangeIDFromBranchRange(messages)
	if err != nil {
		return "", fmt.Errorf("%w (range %s..HEAD)", err, peer)
	}
	return changeID, nil
}

func sourceBranch(root string) (string, error) {
	declared, err := declaredIntegrationBranch(root)
	if err != nil {
		return "", err
	}
	if declared != "" {
		return declared, nil
	}
	return originHeadBranch(root)
}

// changeIDBranchList accepts a scalar or a sequence. A mapping or an empty
// scalar declares nothing. The first non-empty entry wins.
type changeIDBranchList []string

func (l *changeIDBranchList) UnmarshalYAML(value *yaml.Node) error {
	switch value.Kind {
	case yaml.ScalarNode:
		var single string
		if err := value.Decode(&single); err != nil {
			return err
		}
		if single != "" {
			*l = changeIDBranchList{single}
		}
		return nil
	case yaml.SequenceNode:
		var many []string
		if err := value.Decode(&many); err != nil {
			return err
		}
		*l = many
		return nil
	default:
		return nil
	}
}

func (l changeIDBranchList) first() string {
	for _, entry := range l {
		if entry != "" {
			return entry
		}
	}
	return ""
}

func declaredIntegrationBranch(root string) (string, error) {
	path := filepath.Join(root, ".gz-git.yaml")
	b, readErr := os.ReadFile(path)
	if os.IsNotExist(readErr) {
		return "", nil
	}
	if readErr != nil {
		return "", fmt.Errorf("%s exists but cannot be read: %w", path, readErr)
	}
	var doc struct {
		Branch struct {
			IntegrationBranch changeIDBranchList `yaml:"integrationBranch"`
		} `yaml:"branch"`
	}
	if err := yaml.Unmarshal(b, &doc); err != nil {
		return "", fmt.Errorf("%s cannot be parsed, and a declaration that cannot be read is not one that declares nothing: parse .gz-git.yaml: %w", path, err)
	}
	return doc.Branch.IntegrationBranch.first(), nil
}

func originHeadBranch(root string) (string, error) {
	out, err := gitText(root, "symbolic-ref", "--short", "refs/remotes/origin/HEAD")
	if err != nil || strings.TrimSpace(out) == "" {
		return "", errors.New("source branch is not declared and origin/HEAD is unresolved")
	}
	parts := strings.Split(strings.TrimSpace(out), "/")
	return parts[len(parts)-1], nil
}

func sourceBranchRef(root, source string) (string, error) {
	origin := "refs/remotes/origin/" + source
	local := "refs/heads/" + source
	resolvedOrigin := refExists(root, origin)
	resolvedLocal := refExists(root, local)
	switch {
	case !resolvedOrigin && !resolvedLocal:
		return "", fmt.Errorf("source branch %q resolves neither on origin nor locally: cannot find the branch point", source)
	case resolvedOrigin && !resolvedLocal:
		return origin, nil
	case !resolvedOrigin && resolvedLocal:
		return local, nil
	}
	return nearerRef(root, origin, local)
}

func refExists(root, candidate string) bool {
	out, _, err := gitRaw(root, "rev-parse", "--verify", "--quiet", candidate)
	return err == nil && strings.TrimSpace(string(out)) != ""
}

// nearerRef picks the ref whose merge-base is closer to HEAD. A ref that
// resolves but shares no history with HEAD is dropped. Equal bases use origin.
// Incomparable bases, which a merge in HEAD can produce, are an error.
func nearerRef(root, origin, local string) (string, error) {
	baseOrigin, errOrigin := mergeBase(root, origin)
	baseLocal, errLocal := mergeBase(root, local)
	switch {
	case errOrigin != nil && errLocal != nil:
		return "", errors.Join(errOrigin, errLocal)
	case errOrigin != nil:
		return local, nil
	case errLocal != nil:
		return origin, nil
	}
	if baseOrigin == baseLocal {
		return origin, nil
	}
	if isAncestor(root, baseOrigin, baseLocal) {
		return local, nil
	}
	if isAncestor(root, baseLocal, baseOrigin) {
		return origin, nil
	}
	return "", fmt.Errorf("HEAD reaches %q on origin and locally through separate lines of history, so their branch points are incomparable and neither is the nearer one", strings.TrimPrefix(local, "refs/heads/"))
}

func isAncestor(root, ancestor, descendant string) bool {
	_, _, err := gitRaw(root, "merge-base", "--is-ancestor", ancestor, descendant)
	return err == nil
}

func mergeBase(root, peer string) (string, error) {
	out, stderr, err := gitRaw(root, "merge-base", peer, "HEAD")
	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			return "", fmt.Errorf("merge-base %s HEAD: %w", peer, err)
		}
		if msg := strings.TrimSpace(stderr); msg != "" {
			return "", fmt.Errorf("merge-base %s HEAD: %s", peer, msg)
		}
		return "", fmt.Errorf("merge-base %s HEAD found no common ancestor: the branch shares no history with its source", peer)
	}
	base := strings.TrimSpace(string(out))
	if base == "" {
		return "", fmt.Errorf("merge-base %s HEAD exited zero but printed no commit", peer)
	}
	return base, nil
}

func rangeMessages(root, base string) ([]string, error) {
	out, stderr, err := gitRaw(root, "log", "-z", "--format=%B", base+"..HEAD")
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return nil, fmt.Errorf("read branch commit messages: %s", strings.TrimSpace(stderr))
		}
		return nil, fmt.Errorf("read branch commit messages: %w", err)
	}
	return splitGitLogMessages(string(out)), nil
}

// splitGitLogMessages splits a NUL-terminated `git log -z --format=%B` stream.
// The trailing separator is an empty record, and a second empty record is
// empty as well. Neither is a commit. Internal blank lines stay in the message.
func splitGitLogMessages(raw string) []string {
	var messages []string
	for _, message := range strings.Split(raw, "\x00") {
		if strings.TrimSpace(message) != "" {
			messages = append(messages, message)
		}
	}
	return messages
}

func gitRaw(root string, args ...string) ([]byte, string, error) {
	cmd := exec.Command("git", append([]string{"--no-replace-objects", "--no-lazy-fetch"}, args...)...)
	cmd.Dir = root
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	return out, stderr.String(), err
}

func gitText(root string, args ...string) (string, error) {
	out, stderr, err := gitRaw(root, args...)
	if err != nil {
		if msg := strings.TrimSpace(stderr); msg != "" {
			return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), msg)
		}
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(out)), nil
}
