//go:build darwin || linux

package boardfiles

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// readCandidatePolicyPlatform owns every descriptor it opens. between is a
// test-only seam placed after the leaf descriptor is bound, before post-read
// pathname validation.
func readCandidatePolicyPlatform(ctx context.Context, name string, between func() error) (data []byte, err error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	components, err := candidatePathComponents(name)
	if err != nil {
		return nil, fmt.Errorf("resolve candidate policy path: %w", err)
	}
	parents := components[:len(components)-1]
	leaf := components[len(components)-1]

	root, err := openCandidateDirectory(unix.AT_FDCWD, string(os.PathSeparator))
	if err != nil {
		return nil, fmt.Errorf("open candidate root: %w", err)
	}
	identities := make([]captureIdentity, 0, len(parents)+1)
	rootID, err := captureFDIdentity(root)
	if err != nil {
		return nil, errors.Join(err, root.Close())
	}
	identities = append(identities, rootID)
	current := root
	defer func() {
		err = errors.Join(err, current.Close())
		if err != nil {
			data = nil
		}
	}()
	for _, component := range parents {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		next, openErr := openCandidateDirectory(int(current.Fd()), component)
		if openErr != nil {
			return nil, fmt.Errorf("open candidate path component: %w", openErr)
		}
		id, statErr := captureFDIdentity(next)
		if closeErr := current.Close(); statErr != nil || closeErr != nil {
			return nil, errors.Join(statErr, closeErr, next.Close())
		}
		current = next
		identities = append(identities, id)
	}
	before, err := captureEntryIdentity(int(current.Fd()), leaf)
	if err != nil {
		return nil, fmt.Errorf("stat candidate policy: %w", err)
	}
	fd, err := unix.Openat(int(current.Fd()), leaf, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("open candidate policy: %w", err)
	}
	file := os.NewFile(uintptr(fd), leaf)
	if file == nil {
		return nil, errors.Join(fmt.Errorf("wrap candidate policy descriptor"), unix.Close(fd))
	}
	data, err = readOpenedCandidatePolicy(ctx, file, before, between)
	if err != nil {
		return nil, err
	}
	if err := verifyCandidatePath(ctx, components, identities, leaf, before); err != nil {
		return nil, err
	}
	return data, nil
}

// candidatePathComponents deliberately preserves dot components. Cleaning an
// input such as link/../policy before traversal would hide the link from
// O_NOFOLLOW and change the path the caller actually supplied.
func candidatePathComponents(name string) ([]string, error) {
	raw := name
	if !filepath.IsAbs(raw) {
		cwd, err := os.Getwd()
		if err != nil {
			return nil, err
		}
		raw = cwd + string(os.PathSeparator) + raw
	}
	parts := strings.Split(strings.TrimLeft(raw, string(os.PathSeparator)), string(os.PathSeparator))
	if len(parts) == 0 || parts[len(parts)-1] == "" {
		return nil, fmt.Errorf("candidate policy is not a file")
	}
	components := make([]string, 0, len(parts))
	for _, part := range parts {
		if part != "" {
			components = append(components, part)
		}
	}
	if len(components) == 0 {
		return nil, fmt.Errorf("candidate policy is not a file")
	}
	return components, nil
}

func openCandidateDirectory(parentFD int, name string) (*os.File, error) {
	fd, err := unix.Openat(parentFD, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), name)
	if file == nil {
		return nil, errors.Join(fmt.Errorf("wrap candidate directory descriptor"), unix.Close(fd))
	}
	if info, statErr := file.Stat(); statErr != nil || !info.IsDir() {
		return nil, errors.Join(statErr, fmt.Errorf("candidate path component is not a directory"), file.Close())
	}
	return file, nil
}

// readOpenedCandidatePolicy owns file and clears data on every error path.
func readOpenedCandidatePolicy(ctx context.Context, file *os.File, expected captureIdentity, between func() error) (data []byte, err error) {
	defer func() {
		err = errors.Join(err, file.Close())
		if err != nil {
			data = nil
		}
	}()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := verifyCaptureFD(file, expected); err != nil {
		return nil, fmt.Errorf("candidate path and opened file identity differ: %w", err)
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, errors.Join(err, fmt.Errorf("candidate policy is not a regular file"))
	}
	if info.Size() > candidatePolicyFileLimit {
		return nil, fmt.Errorf("candidate policy exceeds %d bytes", candidatePolicyFileLimit)
	}
	data, err = io.ReadAll(io.LimitReader(captureContextReader{ctx, file}, candidatePolicyFileLimit+1))
	if err != nil {
		return nil, err
	}
	if len(data) > candidatePolicyFileLimit {
		return nil, fmt.Errorf("candidate policy exceeds %d bytes", candidatePolicyFileLimit)
	}
	if err := verifyCaptureFD(file, expected); err != nil {
		return nil, fmt.Errorf("candidate policy changed while reading: %w", err)
	}
	if between != nil {
		if err := between(); err != nil {
			return nil, err
		}
	}
	return data, ctx.Err()
}

func verifyCandidatePath(ctx context.Context, components []string, identities []captureIdentity, leaf string, expected captureIdentity) (err error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	current, err := openCandidateDirectory(unix.AT_FDCWD, string(os.PathSeparator))
	if err != nil {
		return fmt.Errorf("reopen candidate root: %w", err)
	}
	defer func() { err = errors.Join(err, current.Close()) }()
	if err := verifyCaptureFD(current, identities[0]); err != nil {
		return fmt.Errorf("candidate path changed while reading: %w", err)
	}
	for i, component := range components[:len(components)-1] {
		if err := ctx.Err(); err != nil {
			return err
		}
		next, openErr := openCandidateDirectory(int(current.Fd()), component)
		if openErr != nil {
			return fmt.Errorf("candidate path changed while reading: %w", openErr)
		}
		if closeErr := current.Close(); closeErr != nil {
			return errors.Join(closeErr, next.Close())
		}
		current = next
		if err := verifyCaptureFD(current, identities[i+1]); err != nil {
			return fmt.Errorf("candidate path changed while reading: %w", err)
		}
	}
	after, err := captureEntryIdentity(int(current.Fd()), leaf)
	if err != nil || after != expected {
		return errors.Join(err, fmt.Errorf("candidate policy changed while reading"))
	}
	return nil
}
