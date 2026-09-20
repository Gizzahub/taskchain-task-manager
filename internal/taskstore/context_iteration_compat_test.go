package taskstore

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Gizzahub/taskchain-task-manager/internal/intentdoc"
)

// Run against an actual pre-maintenance executable, not a simulated codec.
func TestIterationLegacyBinaryCompatibility(t *testing.T) {
	t.Parallel()
	binary := os.Getenv("TASKCHAIN_ITERATION_LEGACY_BINARY")
	if binary == "" {
		t.Skip("set TASKCHAIN_ITERATION_LEGACY_BINARY to a pre-maintenance executable")
	}
	board := configuredFixture(t)
	if out, err := runLegacyPolicyCommand(binary, "list", "--dir", board, "--json"); err != nil {
		t.Fatalf("legacy baseline: %v %s", err, out)
	}
	for _, raw := range []string{testContextIntent, testContextBatch} {
		file := filepath.Join(t.TempDir(), "legacy.json")
		if err := os.WriteFile(file, []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
		out, err := runLegacyPolicyCommand(binary, "validate-context", file, "--json")
		if err != nil {
			t.Fatalf("legacy validation baseline: %v %s", err, out)
		}
		var legacy struct {
			Canonical json.RawMessage `json:"canonical"`
			Digest    string          `json:"digest"`
		}
		if err := json.Unmarshal(out, &legacy); err != nil {
			t.Fatal(err)
		}
		current, err := intentdoc.Parse([]byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		canonical, _ := current.Canonical()
		digest, _ := current.Digest()
		if !bytes.Equal(canonical, legacy.Canonical) || digest != legacy.Digest {
			t.Fatal("v1 canonical bytes or digest differ from actual old executable")
		}
	}
	intentRaw, intent := maintenanceIntentRaw(t)
	iterationRaw := maintenanceIterationRaw(t, intent, "ITERATION-"+strings.Repeat("d", 32), "idle", nil, nil)
	for _, raw := range [][]byte{intentRaw, iterationRaw} {
		doc, err := intentdoc.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		file := filepath.Join(t.TempDir(), "new.json")
		if err := os.WriteFile(file, raw, 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := RegisterContext(board, raw); err != nil {
			t.Fatal("new writer baseline:", err)
		}
		before := boardBytes(t, board)
		for _, args := range [][]string{
			{"validate-context", file, "--json"},
			{"register-context", file, "--dir", board, "--json"},
			{"show-context", "--dir", board, "--kind", doc.Kind(), "--id", doc.ID(), "--revision", "1", "--json"},
		} {
			out, err := runLegacyPolicyCommand(binary, args...)
			var exit *exec.ExitError
			if !errors.As(err, &exit) || (exit.ExitCode() != 1 && exit.ExitCode() != 2) {
				t.Fatalf("old executable accepted new document: %v %v %s", args, err, out)
			}
			if !strings.Contains(string(out), "kind") && !strings.Contains(string(out), "schema") && !strings.Contains(string(out), "maintenance") {
				t.Fatalf("unrelated rejection: %v %s", args, out)
			}
			if !reflect.DeepEqual(before, boardBytes(t, board)) {
				t.Fatal("legacy rejection modified board")
			}
		}
	}
	// New record kinds are not an activation barrier for ordinary card reads.
	if out, err := runLegacyPolicyCommand(binary, "list", "--dir", board, "--json"); err != nil {
		t.Fatalf("legacy card read unexpectedly blocked: %v %s", err, out)
	}
}
