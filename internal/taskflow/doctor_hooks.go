package taskflow

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

const gatePayloadPrefix = "# ce-task-gate: v"

var errSymlinkOrSpecial = errors.New("path is a symlink or non-regular file")

func inspectOneGate(worktree WorktreeHook, gate GateSpec) GateOutcome {
	outcome := GateOutcome{Worktree: worktree.Path, Declaration: gate, Status: "not-adopted", Remediation: "configure-project-hook"}
	if gate.Event != "pre-commit" {
		outcome.Status, outcome.Remediation = "unsupported", "use-supported-event"
		return outcome
	}
	if gate.PayloadVersion != 1 {
		outcome.Status, outcome.Remediation = "unsupported", "use-supported-payload-version"
		return outcome
	}
	path := filepath.Join(worktree.Path, filepath.FromSlash(gate.HookPath))
	rel, err := filepath.Rel(worktree.Path, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		outcome.Status, outcome.Remediation = "unsupported", "use-contained-hook-path"
		return outcome
	}
	if filepath.Base(path) != gate.Event {
		outcome.Status, outcome.Remediation = "unsupported", "match-event-hook-basename"
		return outcome
	}
	if !worktree.HooksPath.Set {
		return outcome
	}
	if filepath.Clean(worktree.HooksPath.Resolved) != filepath.Dir(path) {
		outcome.Status, outcome.Remediation = "drift", "align-hooks-path"
		return outcome
	}
	info, payload, err := readHookPayload(path)
	if err != nil {
		if info != nil {
			outcome.Exists = true
			outcome.Symlink = info.Mode()&os.ModeSymlink != 0
			outcome.RegularFile = info.Mode().IsRegular()
		}
		if errors.Is(err, errSymlinkOrSpecial) {
			outcome.Status, outcome.Remediation = "drift", "refuse-symlink-or-special"
			return outcome
		}
		outcome.Status, outcome.Remediation = "drift", "restore-hook-payload"
		return outcome
	}
	outcome.Exists, outcome.RegularFile = true, true
	outcome.Executable = info.Mode()&0o111 != 0
	outcome.GitMode = gitFileMode(info)
	outcome.SourceRevision, outcome.ObservedBlob, outcome.Dirty = hookGitIdentity(worktree.Path, path)
	sum := sha256.Sum256(payload)
	outcome.ObservedDigest = "sha256:" + hex.EncodeToString(sum[:])
	outcome.ObservedVersion, _ = payloadVersion(payload)
	if !outcome.Executable {
		outcome.Status, outcome.Remediation = "drift", "restore-hook-executable"
		return outcome
	}
	if outcome.ObservedVersion != gate.PayloadVersion {
		outcome.Status, outcome.Remediation = "unsupported", "use-supported-payload-version"
		return outcome
	}
	if outcome.ObservedDigest != gate.PayloadDigest {
		outcome.Status, outcome.Remediation = "drift", "restore-declared-payload"
		return outcome
	}
	outcome.Status, outcome.Remediation = "adopted", "none"
	return outcome
}

func payloadVersion(payload []byte) (int, bool) {
	lines := strings.Split(string(payload), "\n")
	for index, line := range lines {
		line = strings.TrimSuffix(line, "\r")
		if !strings.HasPrefix(line, gatePayloadPrefix) {
			continue
		}
		if (index == 0 && containsPayloadShebang(lines[1:])) ||
			(index != 0 && (index != 1 || !validPayloadShebang(lines[0]))) ||
			markerCount(lines) != 1 {
			return 0, false
		}
		return parsePayloadVersion(line)
	}
	return 0, false
}

func containsPayloadShebang(lines []string) bool {
	for _, line := range lines {
		if validPayloadShebang(line) {
			return true
		}
	}
	return false
}

func markerCount(lines []string) int {
	count := 0
	for _, line := range lines {
		if strings.HasPrefix(strings.TrimSuffix(line, "\r"), gatePayloadPrefix) {
			count++
		}
	}
	return count
}

func validPayloadShebang(line string) bool {
	if !strings.HasPrefix(line, "#!") {
		return false
	}
	interpreter := strings.TrimPrefix(line, "#!")
	for index := 0; index < len(interpreter); index++ {
		character := interpreter[index]
		if (character < 0x20 && character != '\t') || character == 0x7f {
			return false
		}
	}
	first := interpreter
	if separator := strings.IndexAny(first, " \t"); separator >= 0 {
		first = first[:separator]
	}
	return strings.HasPrefix(first, "/") && first != "/"
}

func parsePayloadVersion(line string) (int, bool) {
	raw := strings.TrimPrefix(line, gatePayloadPrefix)
	if raw == "" || (len(raw) > 1 && raw[0] == '0') {
		return 0, false
	}
	version := 0
	for _, character := range raw {
		if character < '0' || character > '9' {
			return 0, false
		}
		version = version*10 + int(character-'0')
	}
	return version, true
}

func readHooksPath(ctx context.Context, root string) (HooksPath, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", root, "config", "--show-origin", "--show-scope", "--get", "core.hooksPath")
	out, err := cmd.CombinedOutput()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 1 && len(strings.TrimSpace(string(out))) == 0 {
			return HooksPath{}, nil
		}
		return HooksPath{}, fmt.Errorf("read core.hooksPath for %s: %s", root, strings.TrimSpace(string(out)))
	}
	parts := strings.Split(strings.TrimSpace(string(out)), "\t")
	if len(parts) != 3 {
		return HooksPath{}, fmt.Errorf("parse core.hooksPath for %s", root)
	}
	value := parts[2]
	resolved := value
	if !filepath.IsAbs(resolved) {
		resolved = filepath.Join(root, resolved)
	}
	return HooksPath{Scope: parts[0], Origin: parts[1], Value: value, Resolved: filepath.Clean(resolved), Set: true}, nil
}

func gitFileMode(info os.FileInfo) string {
	mode := os.FileMode(0o100644)
	if info.Mode()&0o111 != 0 {
		mode = 0o100755
	}
	return fmt.Sprintf("%o", mode)
}

func hookGitIdentity(root, path string) (revision, blob string, dirty bool) {
	ctx := context.Background()
	revision, _ = gitOutput(ctx, root, "rev-parse", "HEAD")
	blob, _ = gitOutput(ctx, root, "hash-object", path)
	index, err := gitOutput(ctx, root, "ls-files", "-s", "--", path)
	if err != nil || index == "" {
		return revision, blob, blob != ""
	}
	fields := strings.Fields(index)
	if len(fields) < 2 {
		return revision, blob, true
	}
	return revision, blob, fields[1] != blob
}

func readHookPayload(path string) (os.FileInfo, []byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return info, nil, errSymlinkOrSpecial
	}
	file, err := openHookNoFollow(path)
	if err != nil {
		return info, nil, err
	}
	opened, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return info, nil, err
	}
	if !opened.Mode().IsRegular() {
		_ = file.Close()
		return opened, nil, errSymlinkOrSpecial
	}
	payload, err := io.ReadAll(file)
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return opened, nil, err
	}
	return opened, payload, nil
}

func openHookNoFollow(path string) (*os.File, error) {
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), path), nil
}
