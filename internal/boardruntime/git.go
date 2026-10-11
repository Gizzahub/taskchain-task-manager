package boardruntime

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

func gitEnv() error {
	for _, key := range []string{
		"GIT_DIR", "GIT_WORK_TREE", "GIT_COMMON_DIR", "GIT_INDEX_FILE",
		"GIT_OBJECT_DIRECTORY", "GIT_ALTERNATE_OBJECT_DIRECTORIES",
		"GIT_CONFIG_COUNT", "GIT_CONFIG_PARAMETERS",
	} {
		if os.Getenv(key) != "" {
			return fmt.Errorf("cleanup refuses Git repository override %s", key)
		}
	}
	return nil
}

func git(ctx context.Context, dir string, args ...string) ([]byte, error) {
	if err := gitEnv(); err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, "git", append([]string{"--no-replace-objects", "--no-lazy-fetch"}, args...)...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if err != nil {
		return stdout.Bytes(), fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

func gitOK(ctx context.Context, dir string, args ...string) bool {
	_, err := git(ctx, dir, args...)
	return err == nil
}

// SourceBranch resolves the cleanup source: .gz-git.yaml, then the two git
// config keys, then origin/HEAD. It never guesses a branch name.
func SourceBranch(ctx context.Context, root string) (string, error) {
	if err := gitEnv(); err != nil {
		return "", err
	}
	declared, err := declaredIntegration(root)
	if err != nil {
		return "", err
	}
	if declared != "" {
		return declared, nil
	}
	for _, key := range []string{"ce.integrationBranch", "workflow.integrationBranch"} {
		out, err := git(ctx, root, "config", "--get", key)
		if err == nil {
			if name := strings.TrimSpace(string(out)); name != "" {
				return name, nil
			}
		}
	}
	out, err := git(ctx, root, "symbolic-ref", "--short", "refs/remotes/origin/HEAD")
	if err == nil {
		name := strings.TrimPrefix(strings.TrimSpace(string(out)), "origin/")
		if name != "" {
			return name, nil
		}
	}
	return "", fmt.Errorf("cleanup source branch is not declared and origin/HEAD is unresolved")
}

type gitDeclaration struct {
	Branch struct {
		IntegrationBranch stringList `yaml:"integrationBranch"`
	} `yaml:"branch"`
}

type stringList []string

func (l *stringList) UnmarshalYAML(value *yaml.Node) error {
	switch value.Kind {
	case yaml.ScalarNode:
		var single string
		if err := value.Decode(&single); err != nil {
			return err
		}
		if single != "" {
			*l = stringList{single}
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

func declaredIntegration(root string) (string, error) {
	path := filepath.Join(root, ".gz-git.yaml")
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("%s exists but cannot be read: %w", path, err)
	}
	var doc gitDeclaration
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return "", fmt.Errorf("parse .gz-git.yaml: %w", err)
	}
	for _, branch := range doc.Branch.IntegrationBranch {
		if branch != "" {
			return branch, nil
		}
	}
	return "", nil
}

// SourceReady requires the source ref to exist locally. A configured upstream
// must match that local tip. Nothing is fetched.
func SourceReady(ctx context.Context, root, source string) error {
	if !gitOK(ctx, root, "rev-parse", "--verify", "--quiet", source+"^{commit}") {
		return fmt.Errorf("configured source tip %q is unavailable locally", source)
	}
	remote, ok := sourceUpstream(ctx, root, source)
	if !ok {
		return nil
	}
	local, localErr := git(ctx, root, "rev-parse", "--verify", "--quiet", source+"^{commit}")
	upstream, upstreamErr := git(ctx, root, "rev-parse", "--verify", "--quiet", remote+"^{commit}")
	if localErr != nil || upstreamErr != nil || strings.TrimSpace(string(local)) != strings.TrimSpace(string(upstream)) {
		return fmt.Errorf("configured source tip %q does not match locally recorded upstream %q", source, remote)
	}
	return nil
}

func sourceUpstream(ctx context.Context, root, source string) (string, bool) {
	remoteRaw, err := git(ctx, root, "config", "--get", "branch."+source+".remote")
	if err != nil || strings.TrimSpace(string(remoteRaw)) == "" {
		return "", false
	}
	mergeRaw, err := git(ctx, root, "config", "--get", "branch."+source+".merge")
	if err != nil || strings.TrimSpace(string(mergeRaw)) == "" {
		return "", false
	}
	return strings.TrimSpace(string(remoteRaw)) + "/" + strings.TrimPrefix(strings.TrimSpace(string(mergeRaw)), "refs/heads/"), true
}

func revParse(ctx context.Context, root, ref string) (string, error) {
	out, err := git(ctx, root, "rev-parse", "--verify", "--quiet", ref+"^{commit}")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// ApplySourceGuard is the cleanup --apply branch guard. Callers that are not
// cleanup add their own prefix.
func ApplySourceGuard(ctx context.Context, root, source string) error {
	out, err := git(ctx, root, "branch", "--show-current")
	if err != nil {
		return fmt.Errorf("read cleanup branch: %w", err)
	}
	current := strings.TrimSpace(string(out))
	if current == "" {
		return fmt.Errorf("cleanup --apply refuses detached HEAD")
	}
	if current == source {
		return fmt.Errorf("cleanup --apply refuses configured source branch %q", source)
	}
	cmd := exec.CommandContext(ctx, "git", "--no-replace-objects", "--no-lazy-fetch", "merge-base", "--is-ancestor", source, "HEAD")
	cmd.Dir = root
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("cleanup --apply requires current HEAD to contain configured source tip %q", source)
	}
	return nil
}
