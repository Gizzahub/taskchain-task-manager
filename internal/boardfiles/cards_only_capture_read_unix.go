//go:build darwin || linux

package boardfiles

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"

	"golang.org/x/sys/unix"
)

func captureFDIdentity(file *os.File) (captureIdentity, error) {
	var stat unix.Stat_t
	if err := unix.Fstat(int(file.Fd()), &stat); err != nil {
		return captureIdentity{}, err
	}
	return captureStatIdentity(stat), nil
}

func captureStatIdentity(stat unix.Stat_t) captureIdentity {
	return captureIdentity{
		dev: uint64(stat.Dev), ino: uint64(stat.Ino), mode: uint32(stat.Mode), size: stat.Size,
		msec: stat.Mtim.Sec, mnsec: stat.Mtim.Nsec, csec: stat.Ctim.Sec, cnsec: stat.Ctim.Nsec,
	}
}

func verifyCaptureFD(file *os.File, expected captureIdentity) error {
	actual, err := captureFDIdentity(file)
	if err != nil {
		return err
	}
	if actual != expected {
		return fmt.Errorf("path and opened file identity differ")
	}
	return nil
}

func readCaptureFile(ctx context.Context, dir *os.File, name string, before captureIdentity) ([]byte, fs.FileInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	fd, err := unix.Openat(int(dir.Fd()), name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, nil, err
	}
	return readCaptureOpened(ctx, dir, name, before, os.NewFile(uintptr(fd), name))
}

// readCaptureOpened owns file, including every refusal path.
func readCaptureOpened(ctx context.Context, dir *os.File, name string, before captureIdentity, file *os.File) (data []byte, info fs.FileInfo, err error) {
	defer func() {
		err = errors.Join(err, file.Close())
		if err != nil {
			data, info = nil, nil
		}
	}()
	if err = ctx.Err(); err != nil {
		return nil, nil, err
	}
	if err = verifyCaptureFD(file, before); err != nil {
		return nil, nil, err
	}
	info, err = file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, nil, errors.Join(err, fmt.Errorf("file is not regular"))
	}
	if info.Size() > cardsOnlyCaptureFileLimit {
		return nil, nil, fmt.Errorf("file exceeds %d bytes", cardsOnlyCaptureFileLimit)
	}
	data, err = io.ReadAll(io.LimitReader(captureContextReader{ctx, file}, cardsOnlyCaptureFileLimit+1))
	if err != nil {
		return nil, nil, err
	}
	if err = verifyCaptureFD(file, before); err != nil {
		return nil, nil, err
	}
	after, err := captureEntryIdentity(int(dir.Fd()), name)
	if err != nil || after != before {
		return nil, nil, errors.Join(err, fmt.Errorf("file changed while reading"))
	}
	if len(data) > cardsOnlyCaptureFileLimit {
		return nil, nil, fmt.Errorf("file exceeds %d bytes", cardsOnlyCaptureFileLimit)
	}
	return data, info, ctx.Err()
}

// Cancellation is checked around each read; a blocked OS read is not preempted.
type captureContextReader struct {
	ctx context.Context
	io.Reader
}

func (r captureContextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := r.Reader.Read(p)
	if canceled := r.ctx.Err(); canceled != nil {
		return n, canceled
	}
	return n, err
}
