package taskruntime

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

type localIdentity struct {
	SchemaVersion int    `yaml:"schema-version"`
	Profile       string `yaml:"profile"`
	Defaults      struct {
		Actor     string `yaml:"actor"`
		ActorKind string `yaml:"actor-kind"`
		Host      string `yaml:"host"`
		ShellMode string `yaml:"shell-mode"`
		// Email is accepted and unused. The identity file is strict, so an
		// unread field would fail every run command on a machine that records
		// an address beside the actor.
		Email string `yaml:"email"`
	} `yaml:"defaults"`
}

type hostResolutionError struct {
	reason     string
	nextAction string
}

func (e *hostResolutionError) Error() string { return e.reason }

type localIdentityError struct {
	path string
	err  error
}

func (e *localIdentityError) Error() string { return e.err.Error() }
func (e *localIdentityError) Unwrap() error { return e.err }

type identityError struct {
	result CommandResult
	err    error
}

func (e *identityError) Error() string { return e.err.Error() }
func (e *identityError) Unwrap() error { return e.err }

func localIdentityPath() (string, error) {
	base := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME"))
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve home directory: %w", err)
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "ce", "identity.yaml"), nil
}

func loadLocalIdentity() (localIdentity, error) {
	var identity localIdentity
	path, err := localIdentityPath()
	if err != nil {
		return identity, err
	}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return identity, nil
	}
	if err != nil {
		return identity, &localIdentityError{path: path, err: fmt.Errorf("read local identity %s: %w", path, err)}
	}
	decoder := yaml.NewDecoder(bytes.NewReader(b))
	decoder.KnownFields(true)
	if err := decoder.Decode(&identity); err != nil {
		return identity, &localIdentityError{path: path, err: fmt.Errorf("decode local identity %s: %w", path, err)}
	}
	if identity.SchemaVersion != 1 {
		return identity, &localIdentityError{path: path, err: fmt.Errorf("unsupported local identity schema-version %d in %s", identity.SchemaVersion, path)}
	}
	rawActor, rawHost := strings.TrimSpace(identity.Defaults.Actor), strings.TrimSpace(identity.Defaults.Host)
	identity.Defaults.Actor = NormalizeIdentity(rawActor)
	identity.Defaults.ActorKind = strings.ToLower(strings.TrimSpace(identity.Defaults.ActorKind))
	identity.Defaults.Host = NormalizeIdentity(rawHost)
	identity.Defaults.ShellMode = strings.ToLower(strings.TrimSpace(identity.Defaults.ShellMode))
	if rawActor != "" && identity.Defaults.Actor == "" {
		return identity, &localIdentityError{path: path, err: fmt.Errorf("local identity actor is invalid")}
	}
	if rawHost != "" && identity.Defaults.Host == "" {
		return identity, &localIdentityError{path: path, err: fmt.Errorf("local identity host is invalid")}
	}
	if identity.Defaults.Actor == "" && identity.Defaults.ActorKind != "" {
		return identity, &localIdentityError{path: path, err: fmt.Errorf("local identity actor-kind requires actor")}
	}
	if identity.Defaults.Actor != "" && identity.Defaults.ActorKind != "human" && identity.Defaults.ActorKind != "agent" {
		return identity, &localIdentityError{path: path, err: fmt.Errorf("local identity actor-kind must be human or agent")}
	}
	if identity.Defaults.ShellMode != "" && identity.Defaults.ShellMode != "shellforge" && identity.Defaults.ShellMode != "config-shellrc" {
		return identity, &localIdentityError{path: path, err: fmt.Errorf("local identity shell-mode must be shellforge or config-shellrc")}
	}
	return identity, nil
}

func (s *Service) gitIdentity(ctx context.Context, gitKey string) (string, error) {
	result, err := s.command(ctx, s.root, "git", "config", "--global", "--includes", "--get-all", gitKey)
	if err != nil {
		return "", &identityError{result: result, err: fmt.Errorf("read Git work identity %s: %w", gitKey, err)}
	}
	if result.ExitCode == 1 {
		return "", nil
	}
	if result.ExitCode != 0 {
		return "", &identityError{result: result, err: fmt.Errorf("read Git work identity %s: %s", gitKey, strings.TrimSpace(result.Stderr))}
	}
	// Git ends each value with one newline. Trim only that byte: trimming
	// every whitespace would hide a second empty value.
	lines := strings.Split(strings.TrimSuffix(result.Stdout, "\n"), "\n")
	if len(lines) != 1 {
		return "", &identityError{result: result, err: fmt.Errorf("global Git %s must have exactly one value; found %d", gitKey, len(lines))}
	}
	value := NormalizeIdentity(lines[0])
	if value == "" {
		return "", &identityError{result: result, err: fmt.Errorf("global Git %s has an invalid value", gitKey)}
	}
	return value, nil
}

func (s *Service) resolveHost(ctx context.Context, local localIdentity) (string, string, error) {
	if value := NormalizeIdentity(os.Getenv("GIT_WORK_HOST")); value != "" {
		return value, "", nil
	}
	gitHost, err := s.gitIdentity(ctx, "ce.workHost")
	if err != nil {
		return "", "", err
	}
	if local.Defaults.Host != "" && gitHost != "" && local.Defaults.Host != gitHost {
		path, pathErr := localIdentityPath()
		if pathErr != nil {
			return "", "", pathErr
		}
		return "", "", &hostResolutionError{
			reason:     fmt.Sprintf("device identity source conflict: %s defaults.host is %q but global Git ce.workHost is %q", path, local.Defaults.Host, gitHost),
			nextAction: "make defaults.host and global Git ce.workHost agree, or set GIT_WORK_HOST for an explicit one-run override",
		}
	}
	if local.Defaults.Host != "" {
		return local.Defaults.Host, "", nil
	}
	if gitHost != "" {
		return gitHost, "", nil
	}
	path, pathErr := localIdentityPath()
	if pathErr != nil {
		return "", "", pathErr
	}
	return "", "", &hostResolutionError{
		reason:     "device identity is not configured: neither " + path + " defaults.host nor global Git ce.workHost is declared",
		nextAction: "declare defaults.host in " + path + " or set global Git ce.workHost (including its include files)",
	}
}

func (s *Service) resolveOwner(ctx context.Context) (Owner, []string, error) {
	local, err := loadLocalIdentity()
	if err != nil {
		return Owner{}, nil, err
	}
	actor, kind := NormalizeIdentity(os.Getenv("CE_TASK_ACTOR")), "agent"
	if actor == "" {
		actor, kind = NormalizeIdentity(os.Getenv("GIT_WORK_ACTOR")), "human"
	}
	if actor == "" && local.Defaults.Actor != "" {
		actor, kind = local.Defaults.Actor, local.Defaults.ActorKind
	}
	if actor == "" {
		actor, err = s.gitIdentity(ctx, "ce.workActor")
		if err != nil {
			return Owner{}, nil, err
		}
		kind = "human"
	}
	host, warning, err := s.resolveHost(ctx, local)
	if err != nil {
		return Owner{}, nil, err
	}
	owner := Owner{Actor: actor, Host: host, Kind: kind}
	if !owner.Valid() {
		return owner, nil, fmt.Errorf("task actor is not configured; set runtime context or defaults.actor in ~/.config/ce/identity.yaml")
	}
	var warnings []string
	if warning != "" {
		warnings = append(warnings, warning)
	}
	return owner, warnings, nil
}

func appendIdentityDiagnostic(res *Response, err error) {
	var identityErr *identityError
	if errors.As(err, &identityErr) {
		res.Diagnostics = append(res.Diagnostics, diagnostic(identityErr.result))
	}
}

func identityNextAction(err error) string {
	var resolution *hostResolutionError
	if errors.As(err, &resolution) {
		return resolution.nextAction
	}
	var local *localIdentityError
	if errors.As(err, &local) {
		return "fix YAML syntax, schema-version, and defaults values in " + local.path
	}
	return "repair the CE local identity configuration"
}
