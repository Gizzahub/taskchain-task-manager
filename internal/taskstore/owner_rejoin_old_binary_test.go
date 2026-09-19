package taskstore

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// protocol5EraCommit is the last revision whose shared-state validator admits
// storage protocols 1 through 5 and knows nothing of owner rejoin.  It is
// pinned rather than derived: the barrier is a claim about a specific released
// shape, and a moving reference would quietly stop testing that shape.
const protocol5EraCommit = "ca7a564"

// protocol5Probe drives the historical taskstore library the way the old CLI
// would have.  That checkout's CLI has only read-only verbs, so putting the
// barrier to the binary alone would prove nothing about writing; the library it
// would have called is the real subject.  It always exits zero and reports the
// two verdicts on stdout, because both "admitted" and "refused" are expected
// results here depending on which board it is pointed at.
const protocol5Probe = `package main

import (
	"fmt"
	"os"

	"github.com/Gizzahub/taskchain-task-manager/internal/taskstore"
)

func main() {
	dir := os.Args[1]
	if _, err := taskstore.Ready(dir); err != nil {
		fmt.Printf("read=refused %v\n", err)
	} else {
		fmt.Println("read=admitted")
	}
	if _, err := taskstore.Create(dir, taskstore.CreateRequest{ID: "TASK-99", Title: "old binary write"}); err != nil {
		fmt.Printf("write=refused %v\n", err)
	} else {
		fmt.Println("write=admitted")
	}
}
`

// TestProtocol5EraLibraryRefusesAnUpgradedBoard is the old-binary barrier, run
// rather than described.  A protocol-5-era build must refuse both to read and
// to write a board the owner rejoin upgraded to protocol 6 -- otherwise the
// upgrade is advisory and an older worktree could still corrupt the board.
//
// The control matters as much as the subject.  An earlier manual run of this
// barrier "passed" while proving nothing, because the only board offered
// besides the target shared the target's common directory and was therefore
// legitimately upgraded too: everything was refused, including things that
// should not have been.  The control here is a protocol-5 board on a common
// directory no rejoin has touched, so a refusal of the target is a statement
// about protocol 6 rather than about this fixture.
func TestProtocol5EraLibraryRefusesAnUpgradedBoard(t *testing.T) {
	probe := buildProtocol5EraProbe(t)
	fx := newOwnerRejoinApplyFixture(t, false, []ReservationFloor{})
	if err := applyOwnerRejoinSameCommon(fx.target, fx.plan, fx.payload, nil); err != nil {
		t.Fatal(err)
	}
	_, control, _ := protocol5SharedFixture(t)
	if _, err := EnableShared(control, false); err != nil {
		t.Fatal(err)
	}

	if got := runProtocol5EraProbe(t, probe, control); !strings.Contains(got, "read=admitted") || !strings.Contains(got, "write=admitted") {
		t.Fatalf("the protocol-5 control board was refused, so this fixture proves nothing about protocol 6: %s", got)
	}
	for _, board := range []string{fx.target, fx.source} {
		got := runProtocol5EraProbe(t, probe, board)
		if !strings.Contains(got, "read=refused") || !strings.Contains(got, "write=refused") {
			t.Fatalf("a protocol-5-era build was admitted to the upgraded board %s: %s", board, got)
		}
		if strings.Contains(got, "TASK-99") {
			t.Fatalf("the old build reported creating a card: %s", got)
		}
	}
}

// buildProtocol5EraProbe extracts the pinned revision and builds the probe
// against it.  It skips loudly rather than silently when the revision or the
// toolchain is out of reach -- a shallow clone, say -- because a barrier that
// no-ops without saying so is the defect this test replaces.
func buildProtocol5EraProbe(t *testing.T) string {
	t.Helper()
	if testing.Short() {
		t.Skip("protocol-5-era barrier builds a historical checkout; skipped under -short")
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("protocol-5-era barrier needs the go toolchain on PATH")
	}
	if out, err := exec.Command("git", "-C", root, "cat-file", "-e", protocol5EraCommit+"^{commit}").CombinedOutput(); err != nil {
		t.Skipf("protocol-5-era barrier needs revision %s in this checkout: %s %v", protocol5EraCommit, out, err)
	}

	src := t.TempDir()
	tarball := filepath.Join(t.TempDir(), "src.tar")
	if out, err := exec.Command("git", "-C", root, "archive", "-o", tarball, protocol5EraCommit).CombinedOutput(); err != nil {
		t.Fatalf("extracting %s: %s %v", protocol5EraCommit, out, err)
	}
	if out, err := exec.Command("tar", "-x", "-f", tarball, "-C", src).CombinedOutput(); err != nil {
		t.Fatalf("unpacking %s: %s %v", protocol5EraCommit, out, err)
	}

	probeDir := filepath.Join(src, "cmd", "protocol5probe")
	if err := os.MkdirAll(probeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(probeDir, "main.go"), []byte(protocol5Probe), 0o600); err != nil {
		t.Fatal(err)
	}
	probe := filepath.Join(t.TempDir(), "protocol5probe")
	build := exec.Command("go", "build", "-mod=mod", "-o", probe, "./cmd/protocol5probe")
	build.Dir = src
	build.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=")
	if out, err := build.CombinedOutput(); err != nil {
		t.Skipf("the protocol-5-era checkout could not be built here: %s %v", out, err)
	}
	return probe
}

func runProtocol5EraProbe(t *testing.T, probe, board string) string {
	t.Helper()
	out, err := exec.Command(probe, board).CombinedOutput()
	if err != nil {
		t.Fatalf("probe on %s: %s %v", board, out, err)
	}
	return string(out)
}
