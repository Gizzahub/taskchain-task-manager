//go:build darwin || linux

package boardfiles

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"
)

func candidateFixture(t *testing.T) (string, string) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	policy := filepath.Join(root, "dir", "candidate.yaml")
	if err := os.Mkdir(filepath.Dir(policy), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(policy, []byte("schema: 5\n"), 0600); err != nil {
		t.Fatal(err)
	}
	return root, policy
}

func TestReadCandidatePolicyLimitAndLinks(t *testing.T) {
	root, policy := candidateFixture(t)
	if err := os.WriteFile(policy, make([]byte, candidatePolicyFileLimit), 0600); err != nil {
		t.Fatal(err)
	}
	if got, err := ReadCandidatePolicy(context.Background(), policy); err != nil || len(got) != candidatePolicyFileLimit {
		t.Fatalf("boundary read: len=%d err=%v", len(got), err)
	}
	if err := os.WriteFile(policy, make([]byte, candidatePolicyFileLimit+1), 0600); err != nil {
		t.Fatal(err)
	}
	if got, err := ReadCandidatePolicy(context.Background(), policy); err == nil || got != nil {
		t.Fatalf("oversize accepted: len=%d err=%v", len(got), err)
	}
	if err := os.Symlink("candidate.yaml", filepath.Join(root, "dir", "leaf-link")); err != nil {
		t.Fatal(err)
	}
	if got, err := ReadCandidatePolicy(context.Background(), filepath.Join(root, "dir", "leaf-link")); err == nil || got != nil {
		t.Fatalf("leaf link accepted: %q %v", got, err)
	}
	if err := os.Symlink("dir", filepath.Join(root, "component-link")); err != nil {
		t.Fatal(err)
	}
	if got, err := ReadCandidatePolicy(context.Background(), filepath.Join(root, "component-link", "candidate.yaml")); err == nil || got != nil {
		t.Fatalf("component link accepted: %q %v", got, err)
	}
	if err := os.Symlink("dir", filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	if got, err := ReadCandidatePolicy(context.Background(), filepath.Join(root, "link", "..", "candidate.yaml")); err == nil || got != nil {
		t.Fatalf("link followed before parent traversal: %q %v", got, err)
	}
	control := filepath.Join(root, "dir", "..", "candidate.yaml")
	if err := os.WriteFile(filepath.Join(root, "candidate.yaml"), []byte("control"), 0600); err != nil {
		t.Fatal(err)
	}
	if got, err := ReadCandidatePolicy(context.Background(), control); err != nil || string(got) != "control" {
		t.Fatalf("real parent traversal rejected: %q %v", got, err)
	}
}

func TestReadCandidatePolicyRejectsFIFOAndCancellation(t *testing.T) {
	root, policy := candidateFixture(t)
	pipe := filepath.Join(root, "dir", "pipe")
	if err := syscall.Mkfifo(pipe, 0600); err != nil {
		t.Fatal(err)
	}
	if got, err := ReadCandidatePolicy(context.Background(), pipe); err == nil || got != nil {
		t.Fatalf("fifo accepted: %q %v", got, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got, err := ReadCandidatePolicy(ctx, policy); !errors.Is(err, context.Canceled) || got != nil {
		t.Fatalf("cancellation lost: %q %v", got, err)
	}
	ctx, cancel = context.WithCancel(context.Background())
	if got, err := readCandidatePolicyPlatform(ctx, policy, func() error { cancel(); return nil }); !errors.Is(err, context.Canceled) || got != nil {
		t.Fatalf("post-read cancellation returned bytes: %q %v", got, err)
	}
}

func TestReadCandidatePolicyDetectsLeafSwapAndParentDetach(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(t *testing.T, root, policy string) error
	}{
		{"leaf-swap", func(t *testing.T, root, policy string) error {
			return os.Rename(filepath.Join(root, "dir", "replacement.yaml"), policy)
		}},
		{"parent-detach", func(t *testing.T, root, policy string) error {
			old := filepath.Join(root, "old-dir")
			if err := os.Rename(filepath.Join(root, "dir"), old); err != nil {
				return err
			}
			if err := os.Mkdir(filepath.Join(root, "dir"), 0755); err != nil {
				return err
			}
			return os.WriteFile(filepath.Join(root, "dir", "candidate.yaml"), []byte("replacement"), 0600)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			root, policy := candidateFixture(t)
			if err := os.WriteFile(filepath.Join(root, "dir", "replacement.yaml"), []byte("replacement"), 0600); err != nil {
				t.Fatal(err)
			}
			got, err := readCandidatePolicyPlatform(context.Background(), policy, func() error { return test.mutate(t, root, policy) })
			if err == nil || got != nil || !strings.Contains(err.Error(), "changed while reading") {
				t.Fatalf("mutation accepted: %q %v", got, err)
			}
		})
	}
}

func TestReadOpenedCandidatePolicyClosesDescriptor(t *testing.T) {
	root, policy := candidateFixture(t)
	dir, err := os.Open(filepath.Dir(policy))
	if err != nil {
		t.Fatal(err)
	}
	defer dir.Close()
	before, err := captureEntryIdentity(int(dir.Fd()), filepath.Base(policy))
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name    string
		between func() error
		wantErr bool
	}{
		{name: "success"},
		{name: "refusal", between: func() error { return errors.New("stop") }, wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			fd, err := unix.Openat(int(dir.Fd()), filepath.Base(policy), unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
			if err != nil {
				t.Fatal(err)
			}
			got, err := readOpenedCandidatePolicy(context.Background(), os.NewFile(uintptr(fd), policy), before, test.between)
			if test.wantErr {
				if err == nil || got != nil {
					t.Fatalf("refusal returned bytes: %q %v", got, err)
				}
			} else if err != nil || string(got) != "schema: 5\n" {
				t.Fatalf("success read failed: %q %v", got, err)
			}
			assertCandidateRawFDClosed(t, fd)
		})
	}
	if err := os.WriteFile(filepath.Join(root, "dir", "other.yaml"), []byte("other"), 0600); err != nil {
		t.Fatal(err)
	}
	fd, err := unix.Openat(int(dir.Fd()), "other.yaml", unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	got, err := readOpenedCandidatePolicy(context.Background(), os.NewFile(uintptr(fd), "other.yaml"), before, nil)
	if err == nil || got != nil {
		t.Fatalf("wrong descriptor identity accepted: %q %v", got, err)
	}
	assertCandidateRawFDClosed(t, fd)
}

func assertCandidateRawFDClosed(t *testing.T, fd int) {
	t.Helper()
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); !errors.Is(err, unix.EBADF) {
		t.Fatalf("descriptor leaked: %v", err)
	}
}
