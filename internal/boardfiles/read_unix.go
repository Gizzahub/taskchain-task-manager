//go:build unix

package boardfiles

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

func openProjection(r *os.Root, name string) (*os.File, error) {
	if !declaredRootLeaf(name) {
		return nil, errors.New("projection path must be one root filename")
	}

	dir, err := r.Open(".")
	if err != nil {
		return nil, err
	}
	fd, openErr := unix.Openat(int(dir.Fd()), name,
		unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	closeErr := dir.Close()
	if openErr != nil {
		return nil, errors.Join(openErr, closeErr)
	}
	if closeErr != nil {
		return nil, errors.Join(closeErr, unix.Close(fd))
	}
	file := os.NewFile(uintptr(fd), name)
	if file == nil {
		return nil, errors.Join(errors.New("wrap projection descriptor"), unix.Close(fd))
	}
	return file, nil
}
