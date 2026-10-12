package taskruntime

import "context"

// ResolveOwner resolves the caller using the same identity policy as lifecycle
// commands. It does not load a runtime declaration or mutate execution state.
func (s *Service) ResolveOwner(ctx context.Context) (Owner, error) {
	return s.owner(ctx)
}

func (s *Service) owner(ctx context.Context) (Owner, error) {
	current, _, err := s.resolveOwner(ctx)
	return current, err
}

func findExecution(executions []Execution, task string) (Execution, bool) {
	for i := range executions {
		if executions[i].Task == task {
			return executions[i], true
		}
	}
	return Execution{}, false
}

// executionAdministrator compares the caller to the declaration exactly.
// An absent or empty list denies. Normalization is not applied here.
func executionAdministrator(cfg Config, owner Owner) bool {
	for _, administrator := range cfg.ExecutionAdministrators {
		if administrator == owner {
			return true
		}
	}
	return false
}
