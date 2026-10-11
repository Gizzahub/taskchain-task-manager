package taskruntime

import (
	"context"
	"sort"
	"strings"
)

// selectBareFinish chooses the one active execution the current actor and
// host own. Zero and several matches refuse without integrating. Finish
// readiness is not required. An explicit task never reaches this function.
func (s *Service) selectBareFinish(ctx context.Context) (string, Response, int) {
	listed := s.status(ctx, "", false)
	if listed.Status == StatusBlocked && len(listed.States) == 0 {
		return "", listed, 1
	}
	current, err := s.owner(ctx)
	if err != nil {
		res := response(StatusBlocked, "", "cannot resolve the current owner for bare run-finish: "+err.Error(), "set the actor and host, then retry")
		appendIdentityDiagnostic(&res, err)
		return "", res, 1
	}
	ids := make([]string, 0, 1)
	for _, state := range listed.States {
		if state.Status != StatusActive || !sameOwner(state.Execution.Owner, current) {
			continue
		}
		ids = append(ids, state.Execution.Task)
	}
	sort.Strings(ids)
	switch len(ids) {
	case 0:
		return "", response(StatusBlocked, "", "no active execution owned by "+current.String()+" is eligible for bare run-finish", "ce task run-list"), 1
	case 1:
		return ids[0], Response{}, 0
	default:
		commands := make([]string, len(ids))
		for i, id := range ids {
			commands[i] = "ce task run-finish " + id
		}
		reason := "bare run-finish is ambiguous for " + current.String() + ": " + strings.Join(ids, ", ")
		return "", response(StatusBlocked, "", reason, strings.Join(commands, "; ")), 1
	}
}
