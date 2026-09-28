package taskstore

import (
	"errors"
	"fmt"
	"testing"
)

func TestContextNotFoundDoesNotHideCloseFailure(t *testing.T) {
	missing := fmt.Errorf("%w: exact revision", ErrContextNotFound)
	closeFailure := errors.New("unlock failed")
	if got := contextReadCloseError(missing, nil); !errors.Is(got, ErrContextNotFound) {
		t.Fatalf("successful close lost not-found: %v", got)
	}
	got := contextReadCloseError(missing, closeFailure)
	if !errors.Is(got, closeFailure) || errors.Is(got, ErrContextNotFound) {
		t.Fatalf("cleanup failure was treated as absence: %v", got)
	}
}
