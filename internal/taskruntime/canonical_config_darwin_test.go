//go:build darwin && cgo

package taskruntime

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func canonicalConfigFixture(t *testing.T) (string, string, string) {
	t.Helper()
	primary, linked := canonicalTestRepo(t, false)
	if err := os.Mkdir(filepath.Join(primary, ".ce"), 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(primary, ConfigFile)
	if err := os.WriteFile(path, []byte(canonicalTestProfile), 0600); err != nil {
		t.Fatal(err)
	}
	return primary, linked, path
}

func TestCanonicalAuthorityRead(t *testing.T) {
	primary, linked, path := canonicalConfigFixture(t)
	for _, root := range []string{primary, linked} {
		got, err := NewService(root, NewExecutor()).readCanonicalAuthority(context.Background())
		if err != nil || string(got.raw) != canonicalTestProfile || got.repository.primary.root != primary || got.declaration.Authority.Writer != "none" {
			t.Fatalf("read: %+v %v", got, err)
		}
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != canonicalTestProfile {
		t.Fatal("reader changed declaration", err)
	}
	if _, err := os.Stat(filepath.Join(primary, ".git", "ce")); !os.IsNotExist(err) {
		t.Fatal("reader created runtime state", err)
	}
}

func TestCanonicalAuthorityPaths(t *testing.T) {
	for _, kind := range []string{"file-mode", "ce-mode", "ancestor-mode", "hardlink", "file-symlink", "ce-symlink", "root-symlink", "directory", "fifo", "oversize"} {
		t.Run(kind, func(t *testing.T) {
			primary, _, path := canonicalConfigFixture(t)
			must := func(err error) {
				t.Helper()
				if err != nil {
					t.Fatal(err)
				}
			}
			switch kind {
			case "file-mode":
				must(os.Chmod(path, 0640))
			case "ce-mode":
				must(os.Chmod(filepath.Dir(path), 0750))
			case "ancestor-mode":
				must(os.Chmod(primary, 0777))
			case "hardlink":
				must(os.Link(path, path+".link"))
			case "file-symlink":
				must(os.Rename(path, path+".real"))
				must(os.Symlink(path+".real", path))
			case "ce-symlink":
				ce := filepath.Dir(path)
				must(os.Rename(ce, ce+".real"))
				must(os.Symlink(ce+".real", ce))
			case "root-symlink":
				alias := primary + ".alias"
				must(os.Symlink(primary, alias))
				primary = alias
			case "directory":
				must(os.Remove(path))
				must(os.Mkdir(path, 0600))
			case "fifo":
				must(os.Remove(path))
				must(unix.Mkfifo(path, 0600))
			case "oversize":
				must(os.WriteFile(path, []byte(strings.Repeat("x", canonicalConfigMaxBytes+1)), 0600))
			}
			got, err := NewService(primary, NewExecutor()).readCanonicalAuthority(context.Background())
			if err == nil || got.raw != nil {
				t.Fatalf("accepted %s: %v", kind, err)
			}
		})
	}
	// Owner rejection is a stat-policy test; no privileged chown is claimed.
	st := unix.Stat_t{Mode: unix.S_IFREG | 0600, Uid: 123, Nlink: 1}
	if canonicalSafeStat(st, 2, 124) {
		t.Fatal("accepted foreign owner")
	}
	st.Mode = unix.S_IFDIR | 01777
	st.Uid = 0
	if !canonicalSafeStat(st, 0, 124) {
		t.Fatal("root sticky temporary ancestor rejected")
	}
	st.Uid = 123
	if canonicalSafeStat(st, 0, 124) {
		t.Fatal("foreign sticky ancestor accepted")
	}
}

func TestCanonicalAuthorityReplacement(t *testing.T) {
	for _, kind := range []string{"file", "ce", "primary", "content", "grow", "grow-before-read"} {
		t.Run(kind, func(t *testing.T) {
			primary, _, path := canonicalConfigFixture(t)
			changed := false
			hook := func(point string) error {
				want := "after-read"
				if kind == "grow-before-read" {
					want = "opened"
				}
				if point != want || changed {
					return nil
				}
				changed = true
				switch kind {
				case "file":
					if err := os.Rename(path, path+".old"); err != nil {
						return err
					}
					return os.WriteFile(path, []byte(canonicalTestProfile), 0600)
				case "ce":
					ce := filepath.Dir(path)
					if err := os.Rename(ce, ce+".old"); err != nil {
						return err
					}
					if err := os.Mkdir(ce, 0700); err != nil {
						return err
					}
					return os.WriteFile(path, []byte(canonicalTestProfile), 0600)
				case "primary":
					return os.Rename(primary, primary+".old")
				case "content":
					return os.WriteFile(path, []byte(strings.Replace(canonicalTestProfile, "writer: none", "writer:   ce", 1)), 0600)
				case "grow", "grow-before-read":
					return os.WriteFile(path, []byte(strings.Repeat("x", canonicalConfigMaxBytes+1)), 0600)
				}
				return nil
			}
			got, err := NewService(primary, NewExecutor()).readCanonicalAuthorityWith(context.Background(), hook)
			if !changed || err == nil || got.raw != nil {
				t.Fatalf("replacement accepted: changed=%v err=%v", changed, err)
			}
		})
	}
}

func TestCanonicalAuthorityReadFaults(t *testing.T) {
	primary, _, _ := canonicalConfigFixture(t)
	for _, fault := range []string{"stat", "acl-query", "opened", "read", "after-read", "close", "before-git-revalidation"} {
		t.Run(fault, func(t *testing.T) {
			hit := false
			got, err := NewService(primary, NewExecutor()).readCanonicalAuthorityWith(context.Background(), func(point string) error {
				if point == fault {
					hit = true
					return errors.New("injected " + fault)
				}
				return nil
			})
			if !hit || err == nil || got.raw != nil {
				t.Fatalf("fault not refused: %s %v %v", fault, hit, err)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := NewService(primary, NewExecutor()).readCanonicalAuthority(ctx); err == nil {
		t.Fatal("cancel ignored")
	}
	if canonicalACLStatus(-1, uint32(os.Geteuid())) != 2 {
		t.Fatal("invalid descriptor ACL query accepted")
	}
}

func TestCanonicalAuthorityACL(t *testing.T) {
	owner, err := user.LookupId(strconv.Itoa(os.Geteuid()))
	if err != nil {
		t.Fatal(err)
	}
	for _, location := range []string{"config", "ce", "primary", "ancestor"} {
		for _, entry := range []string{"everyone allow read", "everyone deny delete", "user:" + owner.Username + " allow read"} {
			t.Run(location+"/"+entry, func(t *testing.T) {
				primary, _, path := canonicalConfigFixture(t)
				switch location {
				case "ce":
					path = filepath.Dir(path)
				case "primary":
					path = primary
				case "ancestor":
					path = filepath.Dir(primary)
				}
				t.Cleanup(func() {
					if out, err := exec.Command("/bin/chmod", "-N", path).CombinedOutput(); err != nil {
						t.Errorf("remove fixture ACL: %v %s", err, out)
					}
				})
				if out, err := exec.Command("/bin/chmod", "+a", entry, path).CombinedOutput(); err != nil {
					t.Fatalf("set fixture ACL: %v %s", err, out)
				}
				got, err := NewService(primary, NewExecutor()).readCanonicalAuthority(context.Background())
				reject := entry == "everyone allow read"
				if reject && (err == nil || got.raw != nil) {
					t.Fatal("nonowner ALLOW accepted")
				}
				if !reject && err != nil {
					t.Fatal("safe ACL rejected", err)
				}
			})
		}
	}
}
