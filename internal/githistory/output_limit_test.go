package githistory

import (
	"context"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"testing"
)

func TestSubprocessPipeCopyCaps(t *testing.T) {
	if _, ok := any(&cappedBuffer{}).(io.ReaderFrom); ok {
		t.Fatal("promoted ReadFrom bypasses Write")
	}
	if maxOutput != 64<<20 || maxDiagnostics != 8192 {
		t.Fatalf("caps changed: stdout %d diagnostics %d", maxOutput, maxDiagnostics)
	}
	for _, stderr := range []bool{false, true} {
		name := "stdout"
		if stderr {
			name = "stderr"
		}
		t.Run(name, func(t *testing.T) {
			dir := historyRepo(t)
			buf := &cappedBuffer{limit: 4}
			script := "!head -c 8 /dev/zero"
			if stderr {
				script += " >&2"
			}
			cmd := exec.Command("git", "-c", "alias.spill="+script, "spill")
			cmd.Dir = dir
			if stderr {
				cmd.Stderr = buf
			} else {
				cmd.Stdout = buf
			}
			err := cmd.Run()
			if buf.Len() > buf.limit {
				t.Fatalf("stored %d bytes", buf.Len())
			}
			if err == nil || !strings.Contains(err.Error(), "Git output limit exceeded") {
				t.Fatalf("pipe copy accepted: %v", err)
			}
		})
	}
}

func TestScannerRunSubprocessCaps(t *testing.T) {
	dir := historyRepo(t)
	s := scanner{root: dir, board: "tasks"}
	ctx := context.Background()
	gitTest(t, dir, "config", "alias.smallout", "!head -c 32 /dev/zero")
	out, err := s.run(ctx, "", "smallout")
	if err != nil || len(out) != 32 {
		t.Fatalf("control output: %d %v", len(out), err)
	}
	gitTest(t, dir, "config", "alias.spillout", "!head -c "+strconv.Itoa(maxOutput+1)+" /dev/zero")
	out, err = s.run(ctx, "", "spillout")
	if err == nil || len(out) > maxOutput {
		t.Fatalf("stdout cap bypassed: %d %v", len(out), err)
	}
	gitTest(t, dir, "config", "alias.spillerr", "!head -c "+strconv.Itoa(maxDiagnostics+1)+" /dev/zero >&2")
	out, err = s.run(ctx, "", "spillerr")
	if err == nil || len(out) != 0 || !strings.Contains(err.Error(), "Git output limit exceeded") {
		t.Fatalf("stderr cap bypassed: %d %v", len(out), err)
	}
}
