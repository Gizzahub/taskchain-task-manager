//go:build !darwin && !linux

package boardfiles

import (
	"context"
	"testing"
)

func TestCardsOnlyPhysicalUnsupportedPlatformFailsClosed(t *testing.T) {
	if _, err := CaptureCardsOnlyPhysical(context.Background(), ".", "tasks", nil); err == nil {
		t.Fatal("physical capture must fail closed on an unsupported platform")
	}
}
