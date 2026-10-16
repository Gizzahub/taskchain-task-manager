//go:build unix

package taskruntime

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

var ErrFixtureBoundary = errors.New("unsafe or unavailable synthetic fixture boundary")

func fixtureOpenFlags(flags int) int { return flags | unix.O_NOFOLLOW | unix.O_NONBLOCK }

// OpenFixtureSandbox pins an explicitly selected owner-private synthetic root.
// It does not discover a live repository or create/repair any directory.
func OpenFixtureSandbox(path string) (*os.Root, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || path == "/" {
		return nil, ErrFixtureBoundary
	}
	fd, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, ErrFixtureBoundary
	}
	defer func() { _ = unix.Close(fd) }()
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	for i, part := range parts {
		next, e := unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if e != nil {
			return nil, ErrFixtureBoundary
		}
		_ = unix.Close(fd)
		fd = next
		var st unix.Stat_t
		if unix.Fstat(fd, &st) != nil {
			return nil, ErrFixtureBoundary
		}
		owner := st.Uid == uint32(os.Geteuid())
		if i == len(parts)-1 {
			if !owner || st.Mode&0o7777 != 0o700 {
				return nil, ErrFixtureBoundary
			}
		} else if (!owner && st.Uid != 0) || (st.Mode&0o022 != 0 && !(st.Uid == 0 && st.Mode&unix.S_ISVTX != 0)) {
			return nil, ErrFixtureBoundary
		}
	}
	// OpenRoot is portable descriptor-relative storage; compare against the
	// no-follow walk so a replacement between the two opens is refused.
	var before unix.Stat_t
	if unix.Fstat(fd, &before) != nil {
		return nil, ErrFixtureBoundary
	}
	r, err := os.OpenRoot(path)
	if err != nil {
		return nil, ErrFixtureBoundary
	}
	info, err := r.Stat(".")
	if err != nil {
		_ = r.Close()
		return nil, ErrFixtureBoundary
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || st.Dev != before.Dev || st.Ino != before.Ino {
		_ = r.Close()
		return nil, ErrFixtureBoundary
	}
	return r, nil
}

func fixtureSafeDirectory(info os.FileInfo) bool {
	s, ok := info.Sys().(*syscall.Stat_t)
	return ok && (s.Uid == uint32(os.Geteuid()) || s.Uid == 0) && info.IsDir() && info.Mode().Perm()&0o022 == 0 && info.Mode()&(os.ModeSymlink|os.ModeSetuid|os.ModeSetgid|os.ModeSticky) == 0
}
