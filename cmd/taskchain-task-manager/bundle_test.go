package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gizzahub/taskchain-task-manager/internal/intentdoc"
	"github.com/Gizzahub/taskchain-task-manager/internal/taskstore"
)

const cliBundleIntent = `{"schemaVersion":1,"kind":"intent","id":"INTENT-0123456789abcdef0123456789abcdef","revision":1,"title":"Publish the release","outcome":"A reviewed release is available","mode":"completion","constraints":[],"nonGoals":[],"successCriteria":[{"key":"published","text":"The release is published."}]}`

func cliBundleInput(t *testing.T, title string) []byte {
	t.Helper()
	intent, err := intentdoc.Parse([]byte(cliBundleIntent))
	if err != nil {
		t.Fatal(err)
	}
	digest, err := intent.Digest()
	if err != nil {
		t.Fatal(err)
	}
	request := intentdoc.BundleRequest{
		SchemaVersion: 1,
		Kind:          "task-bundle",
		RequestID:     "0123456789abcdef0123456789abcdef",
		Batch: intentdoc.BundleMetadata{
			ID: "BATCH-abcdef0123456789abcdef0123456789", Revision: 1,
			Intent: intentdoc.IntentRef{ID: intent.ID(), Revision: intent.Revision(), Digest: digest},
			Gap:    "Publish the reviewed release", Constraints: []string{}, AuthorizationRefs: []string{},
		},
		Tasks: []intentdoc.TaskDraft{{Key: "first", Title: title, DependsOn: []intentdoc.TaskReference{}}},
	}
	raw, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

type bundleFailWriter struct{}

func (bundleFailWriter) Write([]byte) (int, error) { return 0, errors.New("synthetic output failure") }

func TestCreateBundleUsageHelpAndInputErrors(t *testing.T) {
	var out, diag bytes.Buffer
	if code := run([]string{"create-bundle", "--help"}, &out, &diag); code != 0 || out.Len() == 0 || diag.Len() != 0 {
		t.Fatalf("help code=%d out=%q err=%q", code, out.String(), diag.String())
	}
	for _, args := range [][]string{
		{"create-bundle"},
		{"create-bundle", "file.json", "--json"},
		{"create-bundle", "file.json", "--dir", "board"},
	} {
		out.Reset()
		diag.Reset()
		if code := run(args, &out, &diag); code != 2 || out.Len() != 0 || diag.Len() == 0 {
			t.Fatalf("usage args=%v code=%d out=%q err=%q", args, code, out.String(), diag.String())
		}
	}
}

func TestCreateBundleRejectsMalformedAndOversizedInput(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bundle.json")
	if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, diag bytes.Buffer
	if code := run([]string{"create-bundle", path, "--dir", filepath.Join(dir, "board"), "--json"}, &out, &diag); code != 1 || out.Len() != 0 || diag.Len() == 0 {
		t.Fatalf("malformed code=%d out=%q err=%q", code, out.String(), diag.String())
	}
	if err := os.WriteFile(path, []byte(strings.Repeat("x", intentdoc.MaxDocumentBytes+1)), 0o600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	diag.Reset()
	if code := run([]string{"create-bundle", path, "--dir", filepath.Join(dir, "board"), "--json"}, &out, &diag); code != 1 || out.Len() != 0 || !strings.Contains(diag.String(), "no larger than") {
		t.Fatalf("oversized code=%d out=%q err=%q", code, out.String(), diag.String())
	}
}

func TestCreateBundlePublishesReplaysAndRejectsContentConflict(t *testing.T) {
	board := filepath.Join(t.TempDir(), "tasks")
	if err := taskstore.Init(board); err != nil {
		t.Fatal(err)
	}
	if _, err := taskstore.RegisterContext(board, []byte(cliBundleIntent)); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "bundle.json")
	if err := os.WriteFile(path, cliBundleInput(t, "First"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, diag bytes.Buffer
	if code := run([]string{"create-bundle", path, "--dir", board, "--adopt", "--json"}, &out, &diag); code != 0 || diag.Len() != 0 {
		t.Fatalf("publish code=%d out=%s err=%s", code, out.Bytes(), diag.Bytes())
	}
	var first taskstore.BundleResult
	if err := json.Unmarshal(out.Bytes(), &first); err != nil || first.Status != "completed" || first.Replayed || len(first.Tasks) != 1 {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	out.Reset()
	if code := run([]string{"create-bundle", path, "--dir", board, "--json"}, &out, &diag); code != 0 {
		t.Fatalf("replay code=%d out=%s err=%s", code, out.Bytes(), diag.Bytes())
	}
	var replay taskstore.BundleResult
	if err := json.Unmarshal(out.Bytes(), &replay); err != nil || !replay.Replayed || replay.Tasks[0].ID != first.Tasks[0].ID {
		t.Fatalf("replay=%+v err=%v", replay, err)
	}
	if err := os.WriteFile(path, cliBundleInput(t, "Changed"), 0o600); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	diag.Reset()
	if code := run([]string{"create-bundle", path, "--dir", board, "--json"}, &out, &diag); code != 1 || out.Len() != 0 || diag.Len() == 0 {
		t.Fatalf("conflict code=%d out=%q err=%q", code, out.String(), diag.String())
	}
}

func TestCreateBundleNoAdoptAndOutputFailureSafety(t *testing.T) {
	board := filepath.Join(t.TempDir(), "tasks")
	if err := taskstore.Init(board); err != nil {
		t.Fatal(err)
	}
	if _, err := taskstore.RegisterContext(board, []byte(cliBundleIntent)); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "bundle.json")
	if err := os.WriteFile(path, cliBundleInput(t, "First"), 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadDir(filepath.Join(board, "todo"))
	if err != nil {
		t.Fatal(err)
	}
	var out, diag bytes.Buffer
	if code := run([]string{"create-bundle", path, "--dir", board, "--json"}, &out, &diag); code != 1 || out.Len() != 0 {
		t.Fatalf("implicit adoption code=%d out=%q err=%q", code, out.String(), diag.String())
	}
	if _, err := os.Stat(filepath.Join(board, ".task-manager-bundles.json")); !os.IsNotExist(err) {
		t.Fatalf("implicit adoption changed journal: %v", err)
	}
	after, _ := os.ReadDir(filepath.Join(board, "todo"))
	if len(before) != len(after) {
		t.Fatal("implicit adoption published a card")
	}
	if code := run([]string{"create-bundle", path, "--dir", board, "--adopt", "--json"}, bundleFailWriter{}, &diag); code != 1 || !strings.Contains(diag.String(), "may already be completed") {
		t.Fatalf("output failure code=%d err=%q", code, diag.String())
	}
	out.Reset()
	if code := run([]string{"create-bundle", path, "--dir", board, "--json"}, &out, &diag); code != 0 {
		t.Fatalf("retry code=%d out=%q err=%q", code, out.String(), diag.String())
	}
	var replay taskstore.BundleResult
	if err := json.Unmarshal(out.Bytes(), &replay); err != nil || !replay.Replayed {
		t.Fatalf("retry=%+v err=%v", replay, err)
	}
}
