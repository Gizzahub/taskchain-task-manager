package taskflow

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
)

// compoundProbe reports shell syntax outside quotes. Those bindings are not
// executed: a direct exec would answer a different command than the shell
// the author wrote. A metacharacter inside quotes is literal text.
func compoundProbe(command string) bool {
	var quote byte
	for i := 0; i < len(command); i++ {
		c := command[i]
		if quote != 0 {
			if c == quote {
				quote = 0
			}
			continue
		}
		switch c {
		case '\'', '"':
			quote = c
		case '\\', ';', '&', '|', '<', '>', '$', '`', '(', ')', '\n', '\r', '*', '?':
			return true
		}
	}
	return false
}

// parseAdmittedProbe splits one allowlisted command. A leading "!" is negation
// only when it is its own word; the returned argv does not contain it.
func parseAdmittedProbe(command string) ([]string, bool, error) {
	command = strings.TrimSpace(command)
	negated := false
	if rest, found := strings.CutPrefix(command, "!"); found {
		if rest == "" || (rest[0] != ' ' && rest[0] != '\t') {
			return nil, false, errors.New("unsupported")
		}
		negated = true
		command = strings.TrimSpace(rest)
	}
	argv, err := splitProbeArgs(command)
	if err != nil || len(argv) == 0 || !probeAllowlist[argv[0]] {
		return nil, false, errors.New("unsupported")
	}
	if argv[0] == "find" {
		for _, arg := range argv[1:] {
			if findActionFlags[arg] || arg == "-fls" || strings.HasPrefix(arg, "-fprint") {
				return nil, false, errors.New("unsupported")
			}
		}
	}
	if argv[0] == "rg" {
		for _, arg := range argv[1:] {
			if rgExecFlags[arg] {
				return nil, false, errors.New("unsupported")
			}
		}
	}
	return argv, negated, nil
}

// runAdmittedProbe executes one classified command. Exit 0 and 1 are answers.
// Any other exit, a timeout, or a failure to start is unavailable.
func runAdmittedProbe(ctx context.Context, dir string, argv []string, selected bool) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = dir
	if selected {
		cmd.Env = snapshotProbeEnv(os.Environ())
	}
	err := cmd.Run()
	if ctx.Err() != nil {
		return false, ctx.Err()
	}
	if err == nil {
		return true, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return false, nil
	}
	return false, err
}

func snapshotProbeEnv(env []string) []string {
	filtered := make([]string, 0, len(env))
	for _, entry := range env {
		if strings.HasPrefix(entry, "RIPGREP_CONFIG_PATH=") || strings.HasPrefix(entry, "GREP_OPTIONS=") {
			continue
		}
		filtered = append(filtered, entry)
	}
	return filtered
}

// snapshotProbeAllowed is the operand grammar for a --base snapshot.
// Anything else could read the host through an absolute path, "..", or a symlink.
func snapshotProbeAllowed(root string, argv []string) bool {
	if len(argv) == 0 || root == "" {
		return false
	}
	switch argv[0] {
	case "test", "[":
		return snapshotTestAllowed(root, argv)
	case "ls":
		return len(argv) == 2 && snapshotPathAllowed(root, argv[1])
	case "grep", "rg":
		return snapshotSearchAllowed(root, argv)
	case "find":
		return len(argv) == 4 && argv[2] == "-name" && snapshotPathAllowed(root, argv[1]) && simpleProbePattern(argv[3])
	default:
		return false
	}
}

func snapshotTestAllowed(root string, argv []string) bool {
	if argv[0] == "[" {
		if len(argv) < 2 || argv[len(argv)-1] != "]" {
			return false
		}
		argv = argv[:len(argv)-1]
	}
	if len(argv) == 2 {
		return argv[1] != "" && !strings.HasPrefix(argv[1], "-")
	}
	return len(argv) == 3 && unaryFileTest(argv[1]) && snapshotPathAllowed(root, argv[2])
}

func unaryFileTest(flag string) bool {
	switch flag {
	case "-b", "-c", "-d", "-e", "-f", "-g", "-h", "-L", "-p", "-r", "-s", "-u", "-w", "-x":
		return true
	default:
		return false
	}
}

func snapshotSearchAllowed(root string, argv []string) bool {
	if len(argv) < 3 || argv[1] != "-q" {
		return false
	}
	i := 2
	if argv[i] == "--" {
		i++
	}
	return len(argv) == i+2 && simpleProbePattern(argv[i]) && snapshotPathAllowed(root, argv[i+1])
}

func simpleProbePattern(pattern string) bool {
	return pattern != "" && !strings.HasPrefix(pattern, "-")
}

func snapshotLexical(operand string) bool {
	if operand == "" || strings.HasPrefix(operand, "-") || filepath.IsAbs(operand) {
		return false
	}
	for _, component := range strings.FieldsFunc(operand, func(r rune) bool { return r == '/' || r == '\\' }) {
		if component == ".." {
			return false
		}
	}
	clean := filepath.Clean(operand)
	return clean != ".." && !strings.HasPrefix(clean, ".."+string(filepath.Separator))
}

// snapshotPathAllowed accepts a relative operand whose symlink walk stays
// inside the snapshot. A missing final component is contained. An absolute
// link target, or a ".." that leaves the root, is not. A ".." that stays
// inside — introduced by a symlink target, never by the operand itself — is.
func snapshotPathAllowed(root, operand string) bool {
	if !snapshotLexical(operand) {
		return false
	}
	current := filepath.Clean(root)
	parts := strings.Split(filepath.Clean(operand), string(filepath.Separator))
	depth := 0
	for i := 0; i < len(parts); i++ {
		part := parts[i]
		if part == "" || part == "." {
			continue
		}
		if part == ".." {
			parent := filepath.Dir(current)
			if !pathInside(root, parent) {
				return false
			}
			current = parent
			continue
		}
		next := filepath.Join(current, part)
		info, err := os.Lstat(next)
		if err != nil {
			if !os.IsNotExist(err) {
				return false
			}
			return snapshotLexicalRest(root, current, parts[i:])
		}
		if info.Mode()&os.ModeSymlink == 0 {
			if !info.IsDir() && i != len(parts)-1 {
				return false
			}
			current = next
			continue
		}
		depth++
		if depth > criterionSymlinkDepthLimit {
			return false
		}
		target, err := os.Readlink(next)
		if err != nil || target == "" || filepath.IsAbs(target) || path.IsAbs(target) || strings.Contains(target, "\\") {
			return false
		}
		// Keep current at the link's parent. The target is relative to that
		// directory. Do not Clean it: Clean would hide a ".." that escapes.
		targetParts := strings.Split(target, "/")
		tail := append([]string{}, parts[i+1:]...)
		rest := make([]string, 0, len(targetParts)+len(tail))
		rest = append(rest, targetParts...)
		rest = append(rest, tail...)
		parts = rest
		i = -1
	}
	return pathInside(root, current)
}

// snapshotLexicalRest finishes a walk after a missing component. Later ".."
// segments still have to stay inside root; the missing name is not created.
func snapshotLexicalRest(root, current string, parts []string) bool {
	for _, part := range parts {
		switch part {
		case "", ".":
			continue
		case "..":
			parent := filepath.Dir(current)
			if !pathInside(root, parent) {
				return false
			}
			current = parent
		default:
			current = filepath.Join(current, part)
		}
	}
	return pathInside(root, current)
}
