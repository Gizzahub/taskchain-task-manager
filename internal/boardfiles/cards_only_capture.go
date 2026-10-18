package boardfiles

import (
	"context"
	"fmt"
	"io/fs"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	cardsOnlyCaptureFileLimit  = 1 << 20
	cardsOnlyCaptureTotalLimit = 64 << 20
	cardsOnlyCaptureEntryLimit = 100000
	cardsOnlyCapturePathLimit  = 16 << 20
)

// CaptureCardsOnlyPhysical makes two matching observations of a real board
// directory. It never follows a link, including a link below the board root.
// tracked is Git metadata observed by the caller; a successful result is not
// an atomic filesystem snapshot or evidence that a later mutation is safe.
func CaptureCardsOnlyPhysical(ctx context.Context, repo, board string, tracked []SnapshotEntry) (CardsOnlySnapshot, error) {
	return captureCardsOnlyPhysicalPlatform(ctx, repo, board, tracked, nil)
}

// captureCardsOnlyPhysicalWithHook is a narrow race-test seam. Production
// uses CaptureCardsOnlyPhysical.
func captureCardsOnlyPhysicalWithHook(ctx context.Context, repo, board string, tracked []SnapshotEntry, between func() error) (CardsOnlySnapshot, error) {
	return captureCardsOnlyPhysicalPlatform(ctx, repo, board, tracked, between)
}

func validPhysicalBoard(board string) error {
	if len(board) > 64<<10 || !fs.ValidPath(board) || board == "." || strings.Contains(board, "\\") ||
		!utf8.ValidString(board) || strings.ContainsFunc(board, unicode.IsControl) {
		return fmt.Errorf("unsafe physical board path")
	}
	for _, component := range strings.Split(board, "/") {
		if strings.EqualFold(component, ".git") {
			return fmt.Errorf("Git metadata is not a board")
		}
	}
	return nil
}
