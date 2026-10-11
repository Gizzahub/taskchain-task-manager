package taskflow

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	// DoctorCapability is the gate-doctor contract id.
	DoctorCapability = "ce.task.gate-doctor/v2"
	doctorSchema     = 2
	doctorSource     = "ce-task-doctor"
	gateDeclSchema   = 1
)

// DoctorReport is the read-only gate-hook observation.
type DoctorReport struct {
	CapabilityID      string         `json:"capabilityId"`
	SchemaVersion     int            `json:"schemaVersion"`
	ObservationSource string         `json:"observationSource"`
	SymlinkSafe       bool           `json:"symlinkSafe"`
	RepositoryID      string         `json:"repositoryId"`
	CommonGitDir      string         `json:"commonGitDir"`
	Worktrees         []WorktreeHook `json:"worktrees"`
	Gates             []GateOutcome  `json:"gates"`
	Status            string         `json:"status"`
	Remediation       string         `json:"remediation"`
	BuildRevision     string         `json:"buildRevision"`
}

// WorktreeHook is one worktree's core.hooksPath.
type WorktreeHook struct {
	Path      string    `json:"path"`
	HooksPath HooksPath `json:"hooksPath"`
}

// HooksPath is the resolved core.hooksPath. Unset fields are omitted.
type HooksPath struct {
	Origin   string `json:"origin,omitempty"`
	Scope    string `json:"scope,omitempty"`
	Value    string `json:"value,omitempty"`
	Resolved string `json:"resolved,omitempty"`
	Set      bool   `json:"set"`
}

// GateOutcome is one declared gate observed in one worktree.
type GateOutcome struct {
	Worktree        string   `json:"worktree"`
	Declaration     GateSpec `json:"declaration"`
	Exists          bool     `json:"exists"`
	Symlink         bool     `json:"symlink"`
	RegularFile     bool     `json:"regularFile"`
	GitMode         string   `json:"gitMode,omitempty"`
	Dirty           bool     `json:"dirty"`
	SourceRevision  string   `json:"sourceRevision,omitempty"`
	ObservedBlob    string   `json:"observedBlob,omitempty"`
	ObservedDigest  string   `json:"observedDigest,omitempty"`
	ObservedVersion int      `json:"observedVersion,omitempty"`
	Executable      bool     `json:"executable"`
	Status          string   `json:"status"`
	Remediation     string   `json:"remediation"`
}

// GateSpec is one task-gate entry.
type GateSpec struct {
	Event          string `yaml:"event" json:"event"`
	ID             string `yaml:"id" json:"id"`
	HookPath       string `yaml:"hook-path" json:"hookPath"`
	PayloadVersion int    `yaml:"payload-version" json:"payloadVersion"`
	PayloadDigest  string `yaml:"payload-digest" json:"payloadDigest"`
}

type gateDeclaration struct {
	SchemaVersion int        `yaml:"schema-version"`
	RepositoryID  string     `yaml:"repository-id"`
	Gates         []GateSpec `yaml:"gates"`
}

// DoctorResult carries the report plus the two absence facts that exit 2
// after the report is printed.
type DoctorResult struct {
	Report            DoctorReport
	IdentityUnknown   bool
	DeclarationAbsent bool
}

// InspectDoctor observes hook adoption. It does not install or edit anything.
func InspectDoctor(ctx context.Context, root string) (DoctorResult, error) {
	common, err := gitOutput(ctx, root, "rev-parse", "--git-common-dir")
	if err != nil {
		return DoctorResult{}, err
	}
	if !filepath.IsAbs(common) {
		common = filepath.Join(root, common)
	}
	paths, err := doctorWorktrees(ctx, root)
	if err != nil {
		return DoctorResult{}, err
	}
	report := DoctorReport{
		CapabilityID: DoctorCapability, SchemaVersion: doctorSchema,
		ObservationSource: doctorSource, SymlinkSafe: true,
		RepositoryID: repositoryID(root), CommonGitDir: filepath.Clean(common),
		Worktrees: []WorktreeHook{}, Gates: []GateOutcome{},
		Status: "not-adopted", Remediation: "declare-task-gate",
		BuildRevision: ReferenceStampRevision,
	}
	for _, path := range paths {
		hooks, hooksErr := readHooksPath(ctx, path)
		if hooksErr != nil {
			return DoctorResult{}, hooksErr
		}
		report.Worktrees = append(report.Worktrees, WorktreeHook{Path: path, HooksPath: hooks})
	}
	declaration, err := loadTaskGateDeclaration(root)
	if err != nil {
		return DoctorResult{}, err
	}
	result := DoctorResult{Report: report}
	if report.RepositoryID == "unknown" {
		result.IdentityUnknown = true
	}
	if declaration == nil {
		result.DeclarationAbsent = true
	}
	if result.IdentityUnknown || result.DeclarationAbsent {
		return result, nil
	}
	if declaration.RepositoryID != report.RepositoryID {
		return DoctorResult{}, fmt.Errorf("declared repository-id does not match runtime repository identity")
	}
	for _, worktree := range report.Worktrees {
		for _, gate := range declaration.Gates {
			report.Gates = append(report.Gates, inspectOneGate(worktree, gate))
		}
	}
	report.Status, report.Remediation = gateReportStatus(report.Gates)
	result.Report = report
	return result, nil
}

func gateReportStatus(gates []GateOutcome) (string, string) {
	status, remediation := "adopted", "none"
	for _, gate := range gates {
		if gate.Status == "unsupported" {
			return gate.Status, gate.Remediation
		}
		if gate.Status == "drift" {
			status, remediation = gate.Status, gate.Remediation
		}
		if gate.Status == "not-adopted" && status == "adopted" {
			status, remediation = gate.Status, gate.Remediation
		}
	}
	return status, remediation
}

func repositoryID(root string) string {
	raw, err := os.ReadFile(filepath.Join(root, ".ce", "task-runtime.yaml"))
	if err != nil {
		return "unknown"
	}
	var doc struct {
		RepositoryID string `yaml:"repository-id"`
	}
	if yaml.Unmarshal(raw, &doc) != nil || strings.TrimSpace(doc.RepositoryID) == "" {
		return "unknown"
	}
	return strings.TrimSpace(doc.RepositoryID)
}

func loadTaskGateDeclaration(root string) (*gateDeclaration, error) {
	raw, err := os.ReadFile(filepath.Join(root, "ce-tasks.yaml"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var top map[string]yaml.Node
	if err := yaml.Unmarshal(raw, &top); err != nil {
		return nil, fmt.Errorf("decode task-gate: %w", err)
	}
	node, ok := top["task-gate"]
	if !ok {
		return nil, nil
	}
	encoded, err := yaml.Marshal(&node)
	if err != nil {
		return nil, err
	}
	var decl gateDeclaration
	decoder := yaml.NewDecoder(strings.NewReader(string(encoded)))
	decoder.KnownFields(true)
	if err := decoder.Decode(&decl); err != nil {
		return nil, fmt.Errorf("decode task-gate: %w", err)
	}
	if decl.SchemaVersion != gateDeclSchema || decl.RepositoryID == "" || len(decl.Gates) == 0 {
		return nil, fmt.Errorf("task-gate requires schema-version 1, repository-id and gates")
	}
	seen := map[string]bool{}
	for _, gate := range decl.Gates {
		if gate.ID == "" || gate.PayloadVersion < 1 || !validSHA256Digest(gate.PayloadDigest) || !validHookPath(gate.HookPath) || seen[gate.ID] {
			return nil, fmt.Errorf("invalid typed task-gate declaration")
		}
		seen[gate.ID] = true
	}
	return &decl, nil
}

func validSHA256Digest(value string) bool {
	if !strings.HasPrefix(value, "sha256:") || len(value) != len("sha256:")+64 {
		return false
	}
	hexPart := strings.TrimPrefix(value, "sha256:")
	if hexPart != strings.ToLower(hexPart) {
		return false
	}
	_, err := hex.DecodeString(hexPart)
	return err == nil
}

func validHookPath(value string) bool {
	clean := filepath.Clean(filepath.FromSlash(value))
	return value != "" && !filepath.IsAbs(clean) && clean != "." && !strings.HasPrefix(clean, ".."+string(filepath.Separator)) && clean != ".."
}

func doctorWorktrees(ctx context.Context, root string) ([]string, error) {
	raw, err := gitOutput(ctx, root, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, line := range strings.Split(raw, "\n") {
		if strings.HasPrefix(line, "worktree ") {
			paths = append(paths, strings.TrimPrefix(line, "worktree "))
		}
	}
	if paths == nil {
		paths = []string{}
	}
	return paths, nil
}
