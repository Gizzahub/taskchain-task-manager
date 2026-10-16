//go:build unix

package taskruntime

import (
	"os"
	"syscall"
	"testing"
)

type fixtureInfo struct {
	os.FileInfo
	stat syscall.Stat_t
}

func (i fixtureInfo) Sys() any { return &i.stat }

func TestFixtureDirectoryOwnerBoundary(t *testing.T) {
	info, err := os.Stat(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		t.Fatal("missing owner metadata")
	}
	fake := fixtureInfo{info, *stat}
	fake.stat.Uid = uint32(os.Geteuid()) + 10000
	if fixtureSafeDirectory(fake) {
		t.Fatal("foreign owner accepted")
	}
}
