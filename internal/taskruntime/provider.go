package taskruntime

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

var gzGitVersion = regexp.MustCompile(`^gz-git version [0-9][0-9A-Za-z.+-]*$`)

type gzGitProvider struct{ runner CommandRunner }

func (p gzGitProvider) diagnose(ctx context.Context, dir string) (ProviderReport, []CommandDiagnostic, error) {
	report, diagnostics, err := p.diagnoseRequired(ctx, dir)
	if err != nil {
		return report, diagnostics, err
	}
	// Optional capabilities do not fail doctor. A missing flag leaves the
	// capability false and the probe output stays in diagnostics.
	diagnostics, _ = p.probeNoFetch(ctx, dir, &report, diagnostics)
	diagnostics, _ = p.probeRelease(ctx, dir, &report, diagnostics)
	return report, diagnostics, nil
}

func (p gzGitProvider) diagnoseRequired(ctx context.Context, dir string) (ProviderReport, []CommandDiagnostic, error) {
	report := ProviderReport{
		Name: providerGZGit,
		Capabilities: map[string]bool{
			"integrate-check": false, "integrate-run": false,
			integrationNoFetchCapability: false, integrationReleaseCapability: false,
		},
	}
	version, err := p.runner.Run(ctx, CommandRequest{Command: providerGZGit, Args: []string{"--version"}, WorkDir: dir})
	diagnostics := []CommandDiagnostic{diagnostic(version)}
	report.Executable = version.Executable
	if err != nil {
		return report, diagnostics, fmt.Errorf("probe %s: %w", providerGZGit, err)
	}
	report.Present = true
	report.Version = strings.TrimSpace(strings.TrimSpace(version.Stdout) + "\n" + strings.TrimSpace(version.Stderr))
	if version.ExitCode != 0 {
		return report, diagnostics, fmt.Errorf("probe %s version: exit %d", providerGZGit, version.ExitCode)
	}
	if !filepath.IsAbs(report.Executable) {
		return report, diagnostics, fmt.Errorf("probe %s resolved executable path is missing or invalid", providerGZGit)
	}
	if !gzGitVersion.MatchString(report.Version) {
		return report, diagnostics, fmt.Errorf("probe %s version is missing or unrecognizable", providerGZGit)
	}
	help, err := p.runner.Run(ctx, CommandRequest{Command: providerGZGit, Args: []string{"integrate", "--help"}, WorkDir: dir})
	diagnostics = append(diagnostics, diagnostic(help))
	if err != nil {
		return report, diagnostics, fmt.Errorf("probe %s integrate help: %w", providerGZGit, err)
	}
	if help.ExitCode != 0 {
		return report, diagnostics, fmt.Errorf("probe %s integrate help: exit %d", providerGZGit, help.ExitCode)
	}
	capabilities, contractErr := integrateCapabilities(help.Stdout + "\n" + help.Stderr)
	if contractErr != nil {
		return report, diagnostics, contractErr
	}
	for capability := range capabilities {
		report.Capabilities[capability] = true
	}
	for _, capability := range []string{"integrate-check", "integrate-run"} {
		if !report.Capabilities[capability] {
			return report, diagnostics, fmt.Errorf("%s integrate help does not declare required capability %s", providerGZGit, capability)
		}
	}
	return report, diagnostics, nil
}

func (p gzGitProvider) probeNoFetch(ctx context.Context, dir string, report *ProviderReport, diagnostics []CommandDiagnostic) ([]CommandDiagnostic, error) {
	for _, operation := range []string{"check", "run"} {
		subhelp, err := p.runner.Run(ctx, CommandRequest{Command: providerGZGit, Args: []string{"integrate", operation, "--help"}, WorkDir: dir})
		diagnostics = append(diagnostics, diagnostic(subhelp))
		if err != nil {
			return diagnostics, fmt.Errorf("probe %s integrate %s help: %w", providerGZGit, operation, err)
		}
		if subhelp.ExitCode != 0 {
			return diagnostics, fmt.Errorf("probe %s integrate %s help: exit %d", providerGZGit, operation, subhelp.ExitCode)
		}
		if !declaresFlag(subhelp.Stdout+"\n"+subhelp.Stderr, operation, integrationNoFetchFlag) {
			return diagnostics, fmt.Errorf("%s integrate %s help does not declare %s in its Flags section", providerGZGit, operation, integrationNoFetchFlag)
		}
	}
	report.Capabilities[integrationNoFetchCapability] = true
	return diagnostics, nil
}

func (p gzGitProvider) probeRelease(ctx context.Context, dir string, report *ProviderReport, diagnostics []CommandDiagnostic) ([]CommandDiagnostic, error) {
	required := map[string][]string{
		"check": {integrationReleaseFlag, integrationTargetFlag, integrationExpectSourceFlag},
		"run":   {integrationReleaseFlag, integrationTargetFlag, integrationNoFetchFlag, integrationExpectSourceFlag},
	}
	for _, operation := range []string{"check", "run"} {
		subhelp, err := p.runner.Run(ctx, CommandRequest{Command: providerGZGit, Args: []string{"integrate", operation, "--help"}, WorkDir: dir})
		diagnostics = append(diagnostics, diagnostic(subhelp))
		if err != nil {
			return diagnostics, fmt.Errorf("probe %s integrate %s help: %w", providerGZGit, operation, err)
		}
		if subhelp.ExitCode != 0 {
			return diagnostics, fmt.Errorf("probe %s integrate %s help: exit %d", providerGZGit, operation, subhelp.ExitCode)
		}
		for _, flag := range required[operation] {
			if !declaresFlag(subhelp.Stdout+"\n"+subhelp.Stderr, operation, flag) {
				return diagnostics, fmt.Errorf("%s integrate %s help does not declare %s in its Flags section", providerGZGit, operation, flag)
			}
		}
	}
	report.Capabilities[integrationReleaseCapability] = true
	return diagnostics, nil
}

func integrateCapabilities(help string) (map[string]bool, error) {
	capabilities := map[string]bool{}
	usage, usageHeader, available := false, false, false
	inCommands := false
	for _, line := range strings.Split(help, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "Usage:" {
			usage = false
			usageHeader = true
			continue
		}
		if usageHeader && !usage && trimmed == "gz-git integrate [command]" {
			usage = true
			continue
		}
		if trimmed == "Available Commands:" {
			if !usage {
				return nil, fmt.Errorf("%s integrate help has no recognized integrate usage", providerGZGit)
			}
			inCommands = true
			available = true
			continue
		}
		if !inCommands {
			continue
		}
		if strings.HasSuffix(trimmed, ":") {
			break
		}
		fields := strings.Fields(line)
		if len(fields) < 2 || len(line) == len(strings.TrimLeft(line, " \t")) {
			continue
		}
		switch fields[0] {
		case "check", "run":
			capabilities["integrate-"+fields[0]] = true
		}
	}
	if !usage || !available {
		return nil, fmt.Errorf("%s integrate help is malformed or does not identify integrate", providerGZGit)
	}
	return capabilities, nil
}

// IntegrationProvider is the declared integration tool. Finish calls Check
// and then Run. Tests substitute a fake. gzGitProvider speaks only the
// documented gz-git command line and never reimplements integration.
type IntegrationProvider interface {
	Check(ctx context.Context, worktree, target string, noFetch bool) (CommandResult, error)
	Run(ctx context.Context, worktree, target string, noFetch bool) (CommandResult, error)
	DiagnoseNoFetch(ctx context.Context, dir string) (ProviderReport, []CommandDiagnostic, error)
}

func (s *Service) integrator() IntegrationProvider {
	if s.provider != nil {
		return s.provider
	}
	return gzGitProvider{runner: s.runner}
}

func (p gzGitProvider) Check(ctx context.Context, dir, target string, noFetch bool) (CommandResult, error) {
	// check only reads, so a moving source tip may stop the whole tree.
	// Run never sets ProcessGroup: an integrated branch must not be interrupted.
	return p.runner.Run(ctx, CommandRequest{
		Command: providerGZGit, Args: integrateArgs("check", target, noFetch), WorkDir: dir, ProcessGroup: true,
	})
}

func (p gzGitProvider) Run(ctx context.Context, dir, target string, noFetch bool) (CommandResult, error) {
	return p.runner.Run(ctx, CommandRequest{
		Command: providerGZGit, Args: integrateArgs("run", target, noFetch), WorkDir: dir,
	})
}

func integrateArgs(operation, target string, noFetch bool) []string {
	args := []string{"integrate", operation, integrationTargetFlag, target}
	if noFetch {
		args = append(args, integrationNoFetchFlag)
	}
	return args
}

func (p gzGitProvider) DiagnoseNoFetch(ctx context.Context, dir string) (ProviderReport, []CommandDiagnostic, error) {
	report, diagnostics, err := p.diagnoseRequired(ctx, dir)
	if err != nil {
		return report, diagnostics, err
	}
	diagnostics, err = p.probeNoFetch(ctx, dir, &report, diagnostics)
	return report, diagnostics, err
}

func declaresFlag(help, operation, flag string) bool {
	usageHeader, usage, inFlags := false, false, false
	for _, line := range strings.Split(help, "\n") {
		trimmed := strings.TrimSpace(line)
		indented := trimmed != "" && len(line) != len(strings.TrimLeft(line, " \t"))
		if !indented && strings.HasSuffix(trimmed, ":") {
			usageHeader = trimmed == "Usage:"
			inFlags = trimmed == "Flags:" && usage
			continue
		}
		fields := strings.Fields(line)
		if usageHeader && indented && len(fields) >= 3 && fields[0] == providerGZGit && fields[1] == "integrate" && fields[2] == operation {
			usage = true
			continue
		}
		if inFlags && indented && len(fields) > 0 && fields[0] == flag {
			return true
		}
	}
	return false
}
