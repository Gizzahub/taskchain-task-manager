package taskruntime

import (
	"context"
	"fmt"
)

type authorityQuiescence struct {
	callersKnown   bool
	callersDrained bool
	boardClear     bool
	cleanupClear   bool
}

// No waiting or forced reclamation under the mutation lock. The controller
// owns launch freeze/drain outside it and must preserve freeze on any failure.
func checkAuthorityQuiescence(ctx context.Context, lock *ownedMutationLock, controller *authorityTransitionController) error {
	if controller == nil || !controller.frozen || !controller.drained || controller.quiescence == nil {
		return errAuthorityPublicationRefused
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	q, err := controller.quiescence(ctx)
	if err != nil {
		return err
	}
	if !controller.frozen || !controller.drained || !q.callersKnown || !q.callersDrained || !q.boardClear || !q.cleanupClear {
		return errAuthorityPublicationRefused
	}
	if err := lock.validate(); err != nil {
		return err
	}
	executions, err := lock.store.load()
	if err != nil {
		return err
	}
	receipts, err := lock.store.latestReceipts()
	if err != nil {
		return err
	}
	for _, r := range receipts {
		if r.Status == StatusFinishing || !TerminalStatus(r.Status) {
			return fmt.Errorf("%w: unresolved receipt", errAuthorityPublicationRefused)
		}
	}
	for _, e := range executions {
		r, ok := receipts[e.Task]
		if !ok || !validTerminalReceipt(e, r) {
			return fmt.Errorf("%w: nonterminal execution", errAuthorityPublicationRefused)
		}
	}
	return nil
}
