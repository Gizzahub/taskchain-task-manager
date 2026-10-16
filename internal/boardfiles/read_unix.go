//go:build unix

package boardfiles

import (
	"os"
	"syscall"
)

func openProjection(r *os.Root, name string) (*os.File, error) {
	return r.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
}
