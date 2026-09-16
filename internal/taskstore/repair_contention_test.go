package taskstore

import (
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func TestRepairConcurrentSessionsCannotWriteThroughHeldLock(t *testing.T) {
	for _, shared := range []bool{false, true} {
		t.Run(fmt.Sprintf("shared=%v", shared), func(t *testing.T) {
			var board, other string
			var req RepairRequest
			if shared {
				_, board, other = sharedFixture(t)
				if _, err := EnableShared(board, false); err != nil {
					t.Fatal(err)
				}
				req = storageRepairRequest(t, board, 'a')
			} else {
				board, req, _, _ = statusRepairFixture(t)
				other = board
			}
			reached := false
			result, err := repairStatusWithStep(board, req, true, false, func(phase string) error {
				if phase != "after-stage" {
					return nil
				}
				reached = true
				before := boardBytes(t, board)
				otherBefore := boardBytes(t, other)
				operations := []func() error{
					func() error { _, e := RecoverStatusRepair(board, req); return e },
					func() error { _, e := Create(board, CreateRequest{Title: "contender"}); return e },
					func() error { _, e := Create(other, CreateRequest{Title: "other contender"}); return e },
				}
				errs := make([]error, len(operations))
				var wg sync.WaitGroup
				for i, operation := range operations {
					wg.Add(1)
					go func() { defer wg.Done(); errs[i] = operation() }()
				}
				wg.Wait()
				for i, e := range errs {
					if e == nil || !strings.Contains(e.Error(), "lock") {
						return fmt.Errorf("contender %d did not hit held lock: %v", i, e)
					}
				}
				if !reflect.DeepEqual(before, boardBytes(t, board)) || !reflect.DeepEqual(otherBefore, boardBytes(t, other)) {
					return fmt.Errorf("contender changed board while repair held lock")
				}
				return nil
			})
			if err != nil || !reached || result.Status != "completed" {
				t.Fatalf("owner did not complete: reached=%v result=%+v err=%v", reached, result, err)
			}
		})
	}
}
