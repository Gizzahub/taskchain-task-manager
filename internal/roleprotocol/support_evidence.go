package roleprotocol

import (
	"fmt"
)

// validateEvidenceDocuments decodes the report, review and every route
// evidence exactly and binds them to this support's source identity, profiles
// and route decisions. A decision that differs per profile cannot pass: such
// an artifact needs separate support.
func (s Support) validateEvidenceDocuments() error {
	decisions := make(map[string]string, len(s.Routes))
	for _, r := range s.Routes {
		decisions[r.Entry] = r.Decision
	}
	command, err := s.validateRunReport(s.Evidence[s.Acceptance.ReportSHA256], "report", routeEntries, decisions)
	if err != nil {
		return err
	}
	if command != s.Acceptance.Command {
		return fmt.Errorf("report command differs from acceptance.command")
	}
	if err := s.validateReview(s.Evidence[s.Acceptance.ReviewSHA256]); err != nil {
		return err
	}
	for _, r := range s.Routes {
		if _, err := s.validateRunReport(s.Evidence[r.EvidenceSHA256], "route evidence "+r.Entry, []string{r.Entry}, decisions); err != nil {
			return err
		}
	}
	return nil
}

// validateRunReport checks one {version,sourceCommit,sourceTree,
// artifactSHA256,command,exitCode,entries} document whose entries list every
// profile × entry pair exactly once in profile, then entry, order.
func (s Support) validateRunReport(raw []byte, what string, entries []string, decisions map[string]string) (string, error) {
	n, err := parseJSON(raw, maxEvidenceEntryBytes, what)
	if err != nil {
		return "", err
	}
	f, err := fields(n, what, "version", "sourceCommit", "sourceTree", "artifactSHA256", "command", "exitCode", "entries")
	if err != nil {
		return "", err
	}
	if err := exactInt(f["version"], what+".version", 1); err != nil {
		return "", err
	}
	if err := s.matchSource(f, what); err != nil {
		return "", err
	}
	command, err := stringValue(f["command"], what+".command")
	if err != nil {
		return "", err
	}
	if command == "" || len(command) > maxCommandBytes {
		return "", fmt.Errorf("%s.command must be 1..%d bytes", what, maxCommandBytes)
	}
	if err := exactInt(f["exitCode"], what+".exitCode", 0); err != nil {
		return "", err
	}
	rows, err := arrayValue(f["entries"], what+".entries")
	if err != nil {
		return "", err
	}
	if len(rows) != len(s.Profiles)*len(entries) {
		return "", fmt.Errorf("%s must list each profile and entry exactly once", what)
	}
	i := 0
	for _, profile := range s.Profiles {
		for _, entry := range entries {
			row := what + ".entries[" + fmt.Sprint(i) + "]"
			rf, err := fields(rows[i], row, "entry", "profile", "decision", "passed")
			if err != nil {
				return "", err
			}
			i++
			if _, err := enumValue(rf["entry"], row+".entry", entry); err != nil {
				return "", err
			}
			if _, err := enumValue(rf["profile"], row+".profile", profile); err != nil {
				return "", err
			}
			if _, err := enumValue(rf["decision"], row+".decision", decisions[entry]); err != nil {
				return "", err
			}
			if err := trueValue(rf["passed"], row+".passed"); err != nil {
				return "", err
			}
		}
	}
	return command, nil
}

func (s Support) validateReview(raw []byte) error {
	const what = "review"
	n, err := parseJSON(raw, maxEvidenceEntryBytes, what)
	if err != nil {
		return err
	}
	f, err := fields(n, what, "version", "sourceCommit", "sourceTree", "artifactSHA256", "grade", "verdict", "reportSHA256")
	if err != nil {
		return err
	}
	if err := exactInt(f["version"], "review.version", 1); err != nil {
		return err
	}
	if err := s.matchSource(f, what); err != nil {
		return err
	}
	if _, err := enumValue(f["grade"], "review.grade", "strong", "frontier"); err != nil {
		return err
	}
	if _, err := enumValue(f["verdict"], "review.verdict", "PASS"); err != nil {
		return err
	}
	_, err = enumValue(f["reportSHA256"], "review.reportSHA256", s.Acceptance.ReportSHA256)
	return err
}

func (s Support) matchSource(f map[string]*node, what string) error {
	commit, tree, artifact, err := sourceIdentity(f, what)
	if err != nil {
		return err
	}
	if commit != s.SourceCommit || tree != s.SourceTree || artifact != s.ArtifactSHA256 {
		return fmt.Errorf("%s source or artifact differs from support", what)
	}
	return nil
}
