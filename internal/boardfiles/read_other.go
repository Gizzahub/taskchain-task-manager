//go:build !unix

package boardfiles

import (
	"errors"
	"os"
)

func openProjection(*os.Root, string) (*os.File, error) {
	return nil, errors.New("projection no-follow reads are unsupported on this platform")
}
