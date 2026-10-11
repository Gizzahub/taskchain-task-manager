//go:build darwin

package boardruntime

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

func exchangeDirectories(parentFD int, left, right string) error {
	return unix.RenameatxNp(parentFD, left, parentFD, right, unix.RENAME_SWAP)
}

func probeExchange(parent string) (resultErr error) {
	probe, err := os.MkdirTemp(parent, ".ce-board-probe-")
	if err != nil {
		return fmt.Errorf("create exchange probe: %w", err)
	}
	defer func() { resultErr = errors.Join(resultErr, os.RemoveAll(probe)) }()
	left, right := filepath.Join(probe, "left"), filepath.Join(probe, "right")
	if err := os.Mkdir(left, 0o700); err != nil {
		return err
	}
	if err := os.Mkdir(right, 0o700); err != nil {
		return err
	}
	if err := exchangeDirectories(unix.AT_FDCWD, left, right); err != nil {
		return fmt.Errorf("%w: %v", ErrUnsupported, err)
	}
	if err := exchangeDirectories(unix.AT_FDCWD, left, right); err != nil {
		return fmt.Errorf("%w: probe restoration: %v", ErrUnsupported, err)
	}
	return nil
}
