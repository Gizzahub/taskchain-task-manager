//go:build darwin || linux

package boardfiles

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func TestCardsOnlyPhysicalOpenedDescriptorIdentity(t *testing.T) {
	for _, replacement := range []bool{false, true} {
		name := "unchanged"
		if replacement {
			name = "replacement"
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			for _, leaf := range []string{"original", "replacement"} {
				if err := os.WriteFile(filepath.Join(root, leaf), []byte(leaf), 0600); err != nil {
					t.Fatal(err)
				}
			}
			dir, err := os.Open(root)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = dir.Close() })
			before, err := captureEntryIdentity(int(dir.Fd()), "original")
			if err != nil {
				t.Fatal(err)
			}
			leaf := "original"
			if replacement {
				leaf = "replacement"
			}
			openedFD, err := unix.Openat(int(dir.Fd()), leaf, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
			if err != nil {
				t.Fatal(err)
			}
			// Preserve the pathname samples while presenting another opened inode.
			// Verify kernel FD closure on both refusal and successful completion.
			data, _, err := readCaptureOpened(context.Background(), dir, "original", before, os.NewFile(uintptr(openedFD), leaf))
			if replacement {
				if err == nil || !strings.Contains(err.Error(), "path and opened file identity differ") || data != nil {
					t.Fatalf("accepted replacement descriptor: data=%q err=%v", data, err)
				}
			} else if err != nil || string(data) != "original" {
				t.Fatalf("unchanged file rejected: data=%q err=%v", data, err)
			}
			var stat unix.Stat_t
			if err := unix.Fstat(openedFD, &stat); !errors.Is(err, unix.EBADF) {
				t.Fatalf("descriptor was not closed: %v", err)
			}
		})
	}
}
