//go:build !darwin && !linux

package boardfiles

import (
	"context"
	"fmt"
)

func captureCardsOnlyPhysicalPlatform(context.Context, string, string, []SnapshotEntry, func() error) (CardsOnlySnapshot, error) {
	return CardsOnlySnapshot{}, fmt.Errorf("physical board capture is unsupported on this operating system")
}
