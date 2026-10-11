package taskruntime

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// stringList accepts the documented sequence and a hand-written scalar.
// A mapping or an empty scalar declares nothing.
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

func (l stringList) first() string {
	for _, entry := range l {
		if entry != "" {
			return entry
		}
	}
	return ""
}

type gitDeclaration struct {
	Branch struct {
		IntegrationBranch stringList `yaml:"integrationBranch"`
	} `yaml:"branch"`
}

// declaredSource is the repository-root integration branch, or "" when the
// declaration is absent. It does not fall back to origin/HEAD.
func (s *Service) declaredSource() (string, error) {
	declaration := filepath.Join(s.root, ".gz-git.yaml")
	b, readErr := os.ReadFile(declaration)
	if os.IsNotExist(readErr) {
		return "", nil
	}
	if readErr != nil {
		return "", fmt.Errorf("%s exists but cannot be read: %w", declaration, readErr)
	}
	var doc gitDeclaration
	if err := yaml.Unmarshal(b, &doc); err != nil {
		return "", fmt.Errorf("%s cannot be parsed, and a declaration that cannot be read is not one that declares nothing: parse .gz-git.yaml: %w", declaration, err)
	}
	return doc.Branch.IntegrationBranch.first(), nil
}

func (s *Service) sourceBranch(ctx context.Context) (string, error) {
	declared, err := s.declaredSource()
	if err != nil || declared != "" {
		return declared, err
	}
	r, runErr := s.command(ctx, s.root, "git", "symbolic-ref", "--short", "refs/remotes/origin/HEAD")
	if runErr == nil && r.ExitCode == 0 {
		parts := strings.Split(strings.TrimSpace(r.Stdout), "/")
		return parts[len(parts)-1], nil
	}
	return "", fmt.Errorf("source branch is not declared and origin/HEAD is unresolved")
}

func sourceTrackingRef(source string) string { return "refs/remotes/origin/" + source }

func integrationTarget(execution Execution) string { return "origin/" + execution.Source }

// sourceTip resolves origin's tip to a commit in this checkout. It reads the
// local remote-tracking ref and does not fetch.
func (s *Service) sourceTip(ctx context.Context, source string) (string, error) {
	ref := sourceTrackingRef(source) + "^{commit}"
	result, err := s.command(ctx, s.root, "git", "rev-parse", "--verify", "--quiet", ref)
	if err != nil {
		return "", fmt.Errorf("resolve source tip %s: %w", sourceTrackingRef(source), err)
	}
	tip := strings.TrimSpace(result.Stdout)
	if result.ExitCode != 0 || tip == "" {
		return "", fmt.Errorf("source tip %s does not resolve in the primary checkout", sourceTrackingRef(source))
	}
	return tip, nil
}
