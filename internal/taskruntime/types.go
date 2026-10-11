// Package taskruntime is the run ledger shared with the CE task runtime:
// the machine-local declaration, the executions and receipts under the git
// common directory, and the derived state the run commands report.
package taskruntime

import (
	"regexp"
	"strings"
	"time"
)

const (
	ConfigFile      = ".ce/task-runtime.yaml"
	RuntimeStateDir = "ce"
	StateVersionDir = "task-runtime/v1"

	StatusActive  = "ACTIVE"
	StatusReady   = "READY"
	StatusAborted = "ABORTED"
	StatusBlocked = "BLOCKED"
	StatusDone    = "DONE"
	StatusUnknown = "UNKNOWN"

	worktrunkVersion  = "0.80"
	worktrunkMinMajor = 0
	worktrunkMinMinor = 80

	providerGZGit                = "gz-git"
	integrationNoFetchFlag       = "--no-fetch"
	integrationNoFetchCapability = "integrate-no-fetch"
	integrationTargetFlag        = "--target"
	integrationReleaseFlag       = "--release"
	integrationExpectSourceFlag  = "--expect-source"
	integrationReleaseCapability = "integrate-release"
	partialCleanupReason         = "integration succeeded but task recovery cleanup failed"
)

// Execution types accepted by run-start --type.
const (
	TypeFeat     = "feat"
	TypeFix      = "fix"
	TypeRefactor = "refactor"
	TypeDocs     = "docs"
	TypeTest     = "test"
	TypeChore    = "chore"
	TypePerf     = "perf"
)

func ValidType(kind string) bool {
	switch kind {
	case TypeFeat, TypeFix, TypeRefactor, TypeDocs, TypeTest, TypeChore, TypePerf:
		return true
	}
	return false
}

// TerminalStatus reports a record no further verb can move.
// READY is a list-level answer, not an execution status.
func TerminalStatus(status string) bool {
	return status == StatusDone || status == StatusAborted
}

var nonIdentitySlug = regexp.MustCompile(`[^a-z0-9]+`)

// NormalizeIdentity reduces an actor, host, or task id to the slug the
// runtime records. Punctuation-only input normalizes to empty, which is unset.
func NormalizeIdentity(value string) string {
	return strings.Trim(nonIdentitySlug.ReplaceAllString(strings.ToLower(strings.TrimSpace(value)), "-"), "-")
}

// Owner is who controls one execution. Comparison is exact; normalization
// happens before a value is stored, never when it is compared.
type Owner struct {
	Actor string `yaml:"actor" json:"actor"`
	Host  string `yaml:"host" json:"host"`
	Kind  string `yaml:"kind" json:"kind"`
}

func (o Owner) Valid() bool { return o.Actor != "" && o.Host != "" }

func (o Owner) String() string { return o.Actor + "/" + o.Host }

// Execution is one row of executions.json.
type Execution struct {
	Task      string    `json:"task"`
	Type      string    `json:"type"`
	Source    string    `json:"source"`
	Owner     Owner     `json:"owner"`
	Branch    string    `json:"branch"`
	Worktree  string    `json:"worktree"`
	StartedAt time.Time `json:"startedAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// CommandDiagnostic is one external command the runtime ran or failed to start.
type CommandDiagnostic struct {
	Command    string   `json:"command,omitempty"`
	Executable string   `json:"executable,omitempty"`
	Args       []string `json:"args,omitempty"`
	Cwd        string   `json:"cwd,omitempty"`
	ExitCode   int      `json:"exitCode"`
	Stdout     string   `json:"stdout,omitempty"`
	Stderr     string   `json:"stderr,omitempty"`
	Error      string   `json:"error,omitempty"`
	NotStarted bool     `json:"notStarted,omitempty"`
}

// ProviderReport is the read-only gz-git probe. Capability values come from
// the provider's own help text.
type ProviderReport struct {
	Name         string          `json:"name"`
	Present      bool            `json:"present"`
	Version      string          `json:"version,omitempty"`
	Executable   string          `json:"executable,omitempty"`
	Capabilities map[string]bool `json:"capabilities"`
}

// Receipt is one line of receipts.jsonl. The four booleans and the identity
// fields are always serialized; the rest use omitempty.
type Receipt struct {
	ExecutionStartedAt  *time.Time          `json:"executionStartedAt,omitempty"`
	ExecutionType       string              `json:"executionType,omitempty"`
	Source              string              `json:"source,omitempty"`
	Task                string              `json:"task"`
	Operation           string              `json:"operation"`
	Status              string              `json:"status"`
	Owner               Owner               `json:"owner"`
	PerformedBy         *Owner              `json:"performedBy,omitempty"`
	Branch              string              `json:"branch"`
	Worktree            string              `json:"worktree"`
	Reason              string              `json:"reason,omitempty"`
	CreatedAt           time.Time           `json:"createdAt"`
	SourceHead          string              `json:"sourceHead,omitempty"`
	SourceHeadBefore    string              `json:"sourceHeadBefore,omitempty"`
	BaseHead            string              `json:"baseHead,omitempty"`
	TaskHead            string              `json:"taskHead,omitempty"`
	SourcePushed        bool                `json:"sourcePushed"`
	WorktreeRemoved     bool                `json:"worktreeRemoved"`
	LocalBranchRemoved  bool                `json:"localBranchRemoved"`
	RemoteBranchRemoved bool                `json:"remoteBranchRemoved"`
	ToolVersion         string              `json:"tool_version,omitempty"`
	ToolRevision        string              `json:"tool_revision,omitempty"`
	Diagnostics         []CommandDiagnostic `json:"diagnostics,omitempty"`
}

// Response is the JSON envelope every run command prints.
// A nil AllowedActions slice serializes as null, which is the response
// shape of a refusal that names no actions.
type Response struct {
	SchemaVersion  int                 `json:"schemaVersion"`
	Status         string              `json:"status"`
	ActiveCount    int                 `json:"activeCount"`
	Task           string              `json:"task"`
	AllowedActions []string            `json:"allowedActions"`
	Reason         string              `json:"reason"`
	NextAction     string              `json:"nextAction"`
	FinishReady    bool                `json:"finishReady"`
	Warnings       []string            `json:"warnings,omitempty"`
	Lock           *LockInfo           `json:"lock,omitempty"`
	Execution      *Execution          `json:"execution,omitempty"`
	Executions     []Execution         `json:"executions,omitempty"`
	States         []DerivedState      `json:"states,omitempty"`
	Receipt        *Receipt            `json:"receipt,omitempty"`
	Diagnostics    []CommandDiagnostic `json:"diagnostics,omitempty"`
	Provider       *ProviderReport     `json:"provider,omitempty"`
}

// DerivedState is one execution after the read-only derivation.
type DerivedState struct {
	Execution      Execution           `json:"execution"`
	Status         string              `json:"status"`
	AllowedActions []string            `json:"allowedActions"`
	Reason         string              `json:"reason"`
	NextAction     string              `json:"nextAction"`
	FinishReady    bool                `json:"finishReady"`
	Diagnostics    []CommandDiagnostic `json:"diagnostics,omitempty"`
}

// LockInfo describes mutation.lock. Reclaimable is true only for a dead pid
// recorded on this host. A pid is meaningless on another machine.
type LockInfo struct {
	Path        string    `json:"path"`
	PID         int       `json:"pid,omitempty"`
	Host        string    `json:"host,omitempty"`
	CreatedAt   time.Time `json:"createdAt,omitempty"`
	AgeSeconds  int64     `json:"ageSeconds,omitempty"`
	PIDRunning  *bool     `json:"pidRunning,omitempty"`
	Reclaimable bool      `json:"reclaimable,omitempty"`
}

// Config is .ce/task-runtime.yaml.
type Config struct {
	ExecutionAdministrators  []Owner                  `yaml:"execution-administrators,omitempty" json:"executionAdministrators,omitempty"`
	SchemaVersion            int                      `yaml:"schema-version,omitempty" json:"schemaVersion"`
	RepositoryID             string                   `yaml:"repository-id" json:"repositoryId"`
	WorktreeRoots            map[string]string        `yaml:"worktree-roots" json:"worktreeRoots"`
	IntegrationProvider      string                   `yaml:"integration-provider" json:"integrationProvider"`
	IntegrationNetworkPolicy IntegrationNetworkPolicy `yaml:"integration-network-policy,omitempty" json:"integrationNetworkPolicy"`
}

// IntegrationNetworkPolicy limits network use by the declared provider.
// An omitted value on schema 0 or 1 means allow.
type IntegrationNetworkPolicy string

const (
	NetworkAllow   IntegrationNetworkPolicy = "allow"
	NetworkNoFetch IntegrationNetworkPolicy = "no-fetch"
)
