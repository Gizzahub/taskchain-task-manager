package taskflow

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// gitOutput runs git with cmd.Dir set to root and returns stdout without the
// trailing newline. Diagnostics from git stay in the returned error.
func gitOutput(ctx context.Context, root string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = root
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
		}
		return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), msg)
	}
	return strings.TrimSuffix(string(out), "\n"), nil
}

// ShowToplevel returns the repository root that contains root.
func ShowToplevel(ctx context.Context, root string) (string, error) {
	top, err := gitOutput(ctx, root, "rev-parse", "--show-toplevel")
	if err != nil {
		msg := strings.TrimPrefix(err.Error(), "git rev-parse --show-toplevel: ")
		msg = strings.TrimSpace(msg)
		if msg == "" {
			msg = "not a git repository"
		}
		return "", errors.New(msg)
	}
	top = strings.TrimSpace(top)
	if top == "" {
		return "", errors.New("not a git repository")
	}
	return top, nil
}

// gitBytes returns git stdout unchanged, including trailing newlines.
func gitBytes(ctx context.Context, root string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = root
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			return nil, fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
		}
		return nil, fmt.Errorf("git %s: %s", strings.Join(args, " "), msg)
	}
	return out, nil
}
