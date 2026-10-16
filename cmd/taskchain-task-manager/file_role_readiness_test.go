package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestFileRoleReadinessConfigurationFailureIsExitTwo(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "tasks"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "tasks", ".task-manager-policy.json"), []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	for _, verb := range []string{"lint", "preflight"} {
		var out, errOut bytes.Buffer
		code := runTask([]string{verb, "--json"}, &out, &errOut)
		if code != 2 || out.Len() != 0 || errOut.Len() == 0 {
			t.Fatalf("%s code=%d stdout=%q stderr=%q", verb, code, out.String(), errOut.String())
		}
	}
}
