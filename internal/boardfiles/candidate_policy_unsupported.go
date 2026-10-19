//go:build !darwin && !linux

package boardfiles

import (
	"context"
	"fmt"
)

func readCandidatePolicyPlatform(context.Context, string, func() error) ([]byte, error) {
	return nil, fmt.Errorf("candidate policy read is unsupported on this operating system")
}
