package taskruntime

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

func (s *Service) config() (Config, error) {
	var cfg Config
	b, err := os.ReadFile(filepath.Join(s.root, ConfigFile))
	if err != nil {
		return cfg, fmt.Errorf("read %s: %w", ConfigFile, err)
	}
	return decodeRuntimeConfig(b)
}

// decodeRuntimeConfig preserves the legacy parser independently of where its
// bytes were read. Inactive canonical classification reuses it without reading
// a caller-local config or changing the live CLI's resolution rules.
func decodeRuntimeConfig(b []byte) (Config, error) {
	var cfg Config
	var keys map[string]yaml.Node
	if err := yaml.Unmarshal(b, &keys); err != nil {
		return cfg, fmt.Errorf("decode %s: %w", ConfigFile, err)
	}
	if _, legacy := keys["worktree-root"]; legacy {
		return cfg, fmt.Errorf("unsupported task runtime config key \"worktree-root\"; use host-keyed \"worktree-roots.<GIT_WORK_HOST>\"")
	}
	if err := yaml.Unmarshal(b, &cfg); err != nil {
		return cfg, fmt.Errorf("decode %s: %w", ConfigFile, err)
	}
	if cfg.SchemaVersion < 0 || cfg.SchemaVersion > 3 {
		return cfg, fmt.Errorf("unsupported task runtime schema-version %d", cfg.SchemaVersion)
	}
	if cfg.SchemaVersion < 2 {
		if _, declared := keys["integration-network-policy"]; declared {
			return cfg, fmt.Errorf("integration-network-policy requires task runtime schema-version: 2")
		}
		cfg.IntegrationNetworkPolicy = NetworkAllow
	} else {
		allowed := map[string]bool{
			"schema-version": true, "repository-id": true, "worktree-roots": true,
			"integration-provider": true, "integration-network-policy": true,
			"execution-administrators": cfg.SchemaVersion == 3,
		}
		unknown := make([]string, 0)
		for key := range keys {
			if !allowed[key] {
				unknown = append(unknown, key)
			}
		}
		if len(unknown) > 0 {
			sort.Strings(unknown)
			return cfg, fmt.Errorf("unsupported task runtime config key %q for schema-version %d", unknown[0], cfg.SchemaVersion)
		}
		policy, declared := keys["integration-network-policy"]
		if !declared || policy.Kind != yaml.ScalarNode || policy.Tag == "!!null" || strings.TrimSpace(policy.Value) == "" {
			return cfg, fmt.Errorf("schema-version %d requires integration-network-policy: allow or no-fetch", cfg.SchemaVersion)
		}
	}
	if err := validateExecutionAdministrators(cfg, keys); err != nil {
		return cfg, err
	}
	if cfg.RepositoryID == "" || len(cfg.WorktreeRoots) == 0 || cfg.IntegrationProvider == "" {
		return cfg, fmt.Errorf("repository-id, worktree-roots, and integration-provider are required")
	}
	if NormalizeIdentity(cfg.RepositoryID) != cfg.RepositoryID {
		return cfg, fmt.Errorf("repository-id %q must be one lowercase kebab-case path segment", cfg.RepositoryID)
	}
	if cfg.IntegrationProvider != providerGZGit {
		return cfg, fmt.Errorf("unsupported integration-provider %q", cfg.IntegrationProvider)
	}
	if cfg.IntegrationNetworkPolicy != NetworkAllow && cfg.IntegrationNetworkPolicy != NetworkNoFetch {
		return cfg, fmt.Errorf("unsupported integration-network-policy %q (valid: allow, no-fetch)", cfg.IntegrationNetworkPolicy)
	}
	return cfg, nil
}

// Administrators are an explicit local policy, not authentication.
// A declaration is never widened by normalizing it.
func validateExecutionAdministrators(cfg Config, keys map[string]yaml.Node) error {
	node, present := keys["execution-administrators"]
	if !present {
		return nil
	}
	if cfg.SchemaVersion != 3 {
		return fmt.Errorf("execution-administrators requires task runtime schema-version: 3")
	}
	if node.Kind != yaml.SequenceNode {
		return fmt.Errorf("execution-administrators must be a sequence of exact actor, host and kind identities")
	}
	seen := map[Owner]bool{}
	for i, item := range node.Content {
		if item.Kind != yaml.MappingNode {
			return fmt.Errorf("execution-administrators[%d] must be an identity mapping", i)
		}
		fields := map[string]bool{}
		for j := 0; j+1 < len(item.Content); j += 2 {
			key, value := item.Content[j], item.Content[j+1]
			if key.Value != "actor" && key.Value != "host" && key.Value != "kind" {
				return fmt.Errorf("execution-administrators[%d] has unknown field %q", i, key.Value)
			}
			if fields[key.Value] || value.Kind != yaml.ScalarNode || value.Tag != "!!str" {
				return fmt.Errorf("execution-administrators[%d] has invalid or duplicate field %q", i, key.Value)
			}
			fields[key.Value] = true
		}
		owner := cfg.ExecutionAdministrators[i]
		if !owner.Valid() || NormalizeIdentity(owner.Actor) != owner.Actor || NormalizeIdentity(owner.Host) != owner.Host || (owner.Kind != "human" && owner.Kind != "agent") || len(fields) != 3 {
			return fmt.Errorf("execution-administrators[%d] requires canonical actor, host and human or agent kind", i)
		}
		if seen[owner] {
			return fmt.Errorf("execution-administrators contains a duplicate identity")
		}
		seen[owner] = true
	}
	return nil
}

func (s *Service) worktreeRoot(cfg Config, host string) (string, error) {
	path := cfg.WorktreeRoots[host]
	if path == "" {
		return "", fmt.Errorf("worktree root is not declared for host %q", host)
	}
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("worktree root for host %q must be absolute", host)
	}
	root, err := canonicalizePotentialPath(s.root)
	if err != nil {
		return "", fmt.Errorf("resolve repository path for worktree safety: %w", err)
	}
	path, err = canonicalizePotentialPath(path)
	if err != nil {
		return "", fmt.Errorf("resolve worktree root for host %q: %w", host, err)
	}
	if filepath.Dir(path) == path {
		return "", fmt.Errorf("worktree root for host %q cannot be a filesystem root", host)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve user home for worktree safety: %w", err)
	}
	home, err = canonicalizePotentialPath(home)
	if err != nil {
		return "", fmt.Errorf("resolve user home for worktree safety: %w", err)
	}
	sameHome, err := sameExistingPath(path, home)
	if err != nil {
		return "", fmt.Errorf("compare worktree root with user home: %w", err)
	}
	if path == home || sameHome {
		return "", fmt.Errorf("worktree root for host %q cannot be the user home directory", host)
	}
	if filepath.Base(path) != cfg.RepositoryID {
		return "", fmt.Errorf("worktree root for host %q must end with repository-id %q", host, cfg.RepositoryID)
	}
	insideRepository, err := withinFilesystem(path, root)
	if err != nil {
		return "", fmt.Errorf("compare worktree root with repository: %w", err)
	}
	besideRepository, err := sameExistingPath(filepath.Dir(root), filepath.Dir(path))
	if err != nil {
		return "", fmt.Errorf("compare worktree and repository parents: %w", err)
	}
	if within(path, root) || insideRepository || filepath.Dir(root) == filepath.Dir(path) || besideRepository {
		return "", fmt.Errorf("worktree root must be outside and not beside the repository")
	}
	return path, nil
}

func canonicalizePotentialPath(path string) (string, error) {
	current, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	missing := make([]string, 0)
	for {
		if _, statErr := os.Lstat(current); statErr == nil {
			resolved, resolveErr := filepath.EvalSymlinks(current)
			if resolveErr != nil {
				return "", resolveErr
			}
			for i := len(missing) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, missing[i])
			}
			return filepath.Clean(resolved), nil
		} else if !errors.Is(statErr, os.ErrNotExist) {
			return "", statErr
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", fmt.Errorf("no existing ancestor for %q", path)
		}
		missing = append(missing, filepath.Base(current))
		current = parent
	}
}

func sameExistingPath(left, right string) (bool, error) {
	leftInfo, leftErr := os.Stat(left)
	if errors.Is(leftErr, os.ErrNotExist) {
		return false, nil
	}
	if leftErr != nil {
		return false, leftErr
	}
	rightInfo, rightErr := os.Stat(right)
	if errors.Is(rightErr, os.ErrNotExist) {
		return false, nil
	}
	if rightErr != nil {
		return false, rightErr
	}
	return os.SameFile(leftInfo, rightInfo), nil
}

func withinFilesystem(path, root string) (bool, error) {
	rootInfo, err := os.Stat(root)
	if err != nil {
		return false, err
	}
	current := path
	for {
		info, statErr := os.Stat(current)
		if statErr == nil {
			for {
				if os.SameFile(info, rootInfo) {
					return true, nil
				}
				parent := filepath.Dir(current)
				if parent == current {
					return false, nil
				}
				current = parent
				info, statErr = os.Stat(current)
				if statErr != nil {
					return false, statErr
				}
			}
		}
		if !errors.Is(statErr, os.ErrNotExist) {
			return false, statErr
		}
		parent := filepath.Dir(current)
		if parent == current {
			return false, nil
		}
		current = parent
	}
}

func within(path, root string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}

func expectedWorktreePath(root, branch string) string {
	return filepath.Join(root, strings.ReplaceAll(strings.TrimPrefix(branch, "dev/"), "/", "__"))
}

func sameOwner(existing, current Owner) bool {
	return existing.Actor == current.Actor && existing.Host == current.Host &&
		(existing.Kind == "" || existing.Kind == current.Kind)
}
