// Package candidatediagnostic implements prospective, read-only board diagnostics.
// Its results are not authority checks, adoption previews, or writer admission.
package candidatediagnostic

import (
	"errors"
	"strings"

	"github.com/Gizzahub/taskchain-task-manager/internal/boardfiles"
)

type Input struct {
	Repo, Board, CandidatePolicy, Source, Ref string
}

type Candidate struct {
	Source        string `json:"source"`
	InputSHA256   string `json:"inputSHA256"`
	PolicySHA256  string `json:"policySHA256"`
	SchemaVersion int    `json:"schemaVersion"`
}

type Snapshot struct {
	Source      string  `json:"source"`
	Board       string  `json:"board"`
	TreeOID     *string `json:"treeOID"`
	IndexDigest *string `json:"indexDigest"`
}

type Finding struct {
	Path     string `json:"path"`
	Severity string `json:"severity"`
	Detail   string `json:"detail"`
	Contract string `json:"contract"`
	Action   string `json:"action"`
}

type DiagnosticError struct {
	Code   string `json:"code"`
	Detail string `json:"detail"`
}

type Result struct {
	Operation        string           `json:"operation"`
	Mode             string           `json:"mode"`
	AuthorityChecked bool             `json:"authorityChecked"`
	Status           string           `json:"status"`
	Candidate        *Candidate       `json:"candidate"`
	Snapshot         *Snapshot        `json:"snapshot"`
	Findings         []Finding        `json:"findings"`
	Error            *DiagnosticError `json:"error"`
}

func (r Result) ExitCode() int {
	switch r.Status {
	case "clean":
		return 0
	case "findings":
		return 1
	default:
		return 2
	}
}

// ValidateInput checks grammar without touching candidate files, Git or authority.
// Details deliberately do not reflect untrusted arguments.
func ValidateInput(in Input) error {
	if in.Repo == "" || in.CandidatePolicy == "" {
		return errors.New("repository and candidate policy are required")
	}
	if err := boardfiles.ValidateBoardPath(in.Board); err != nil {
		return errors.New("board must be a safe non-root repository-relative path")
	}
	switch in.Source {
	case "worktree", "index":
		if in.Ref != "" {
			return errors.New("ref is permitted only for ref source")
		}
	case "ref":
		if strings.TrimSpace(in.Ref) == "" {
			return errors.New("ref source requires a nonempty ref")
		}
	default:
		return errors.New("source must be worktree, index or ref")
	}
	return nil
}
