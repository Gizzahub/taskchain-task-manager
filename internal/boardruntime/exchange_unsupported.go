//go:build !darwin && !linux

package boardruntime

import "fmt"

func exchangeDirectories(int, string, string) error {
	return fmt.Errorf("%w", ErrUnsupported)
}

func probeExchange(string) error {
	return fmt.Errorf("%w", ErrUnsupported)
}
