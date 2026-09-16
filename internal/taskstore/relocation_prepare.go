package taskstore

import (
	"errors"
	"path"
	"strings"

	"github.com/Gizzahub/taskchain-task-manager/internal/boardpolicy"
	"github.com/Gizzahub/taskchain-task-manager/internal/card"
	"github.com/Gizzahub/taskchain-task-manager/internal/cardpath"
)

// relocationZones validates a zone-only rewrite. It performs no discovery or
// filesystem access; the writer must separately validate identity and handles.
func relocationZones(source, target string, policy boardpolicy.Policy) (string, string, error) {
	valid := func(name string) bool {
		if name == "" || path.IsAbs(name) || path.Clean(name) != name || strings.ContainsAny(name, "\\\x00") || path.Ext(name) != ".md" || cardpath.IsDocumentation(path.Base(name)) {
			return false
		}
		for _, part := range strings.Split(name, "/") {
			if strings.HasPrefix(part, ".") || cardpath.IsExcludedDirectory(part) || part == "archive" || part == "_archive" {
				return false
			}
		}
		return true
	}
	if !valid(source) || !valid(target) {
		return "", "", errors.New("invalid relocation path")
	}
	a, b := strings.Split(source, "/"), strings.Split(target, "/")
	if len(a) != len(b) {
		return "", "", errors.New("relocation must preserve module and category path")
	}
	zone := -1
	for i, part := range a[:len(a)-1] {
		if policy.Workflow(part) || policy.IsKind(part) || policy.Parked(part) {
			if zone >= 0 {
				return "", "", errors.New("ambiguous relocation zone path")
			}
			zone = i
		} else if cardpath.WorkflowStatus(part) != "" {
			return "", "", errors.New("workflow alias cannot be a relocation module or category")
		}
	}
	if zone < 0 || !policy.AllowsRelocation(a[zone], b[zone]) {
		return "", "", errors.New("relocation edge is not explicitly allowed")
	}
	for i := range a {
		if i != zone && a[i] != b[i] {
			return "", "", errors.New("relocation must preserve module, category and filename")
		}
	}
	return a[zone], b[zone], nil
}

func prepareRelocationPatch(raw []byte, id, source, target string, policy boardpolicy.Policy) ([]byte, bool, error) {
	_, to, err := relocationZones(source, target, policy)
	if err != nil {
		return nil, false, err
	}
	if len(raw) == 0 || len(raw) > maxCardBytes || identityKey(id) == "" {
		return nil, false, errors.New("invalid relocation card size or identity")
	}
	doc, err := card.Parse(raw)
	if err != nil {
		return nil, false, err
	}
	if !sameIdentity(doc.View().ID, id) {
		return nil, false, errors.New("relocation source identity mismatch")
	}
	status, mapped := policy.KindStatus(to)
	if policy.Workflow(to) {
		status, mapped = policy.Status(to)
	}
	if !mapped {
		return append([]byte(nil), raw...), false, nil
	}
	return doc.SetStatusCell(status)
}
