package main

// The files under testdata/golden are the CLI's published stdout, byte for
// byte. README names the stdout byte stream — not the Go types, the field
// order, or the struct shapes — as what a consumer contracts with, so the
// contract can only be graded where it is actually observable: here, on the
// bytes. A Go type may be renamed or restructured freely as long as these
// files do not move.
//
// A diff in one of these files is therefore a change to the public contract,
// never an incidental test failure. Before accepting one, decide the version
// question: an added key is compatible and may ride the current
// outputformat.Version, while a removed key, a renamed key, a changed value
// spelling or a reordered document breaks consumers and needs that version
// bumped along with a note for the consumers that read the old shape.
//
// Once that decision is made, regenerate with:
//
//	GOWORK=off go test ./cmd/taskchain-task-manager -run TestGoldenStdout -update
//
// then read the resulting diff before committing it.
//
// Determinism is what makes the files gradable. Each case builds its whole
// board from scratch inside a fresh working directory and drives the commands
// with fixed ids, owners, tokens and request ids, so every value in the file
// stays part of the graded contract. The handful of values that no input can
// fix — a crypto/rand authority id, a path only the test's temporary directory
// knows — are replaced by a placeholder via redact, which keeps the key and
// its shape under test but drops its value out of the contract. Each such call
// carries the reason it was unavoidable.

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/Gizzahub/taskchain-task-manager/internal/githistory"
	"github.com/Gizzahub/taskchain-task-manager/internal/taskstore"
)

var updateGolden = flag.Bool("update", false, "rewrite testdata/golden from the emitted stdout bytes")

const goldenPlaceholder = "<not-part-of-the-contract>"

// redact drops one string value out of the graded contract while keeping its
// key and its position. It is keyed on the JSON spelling a consumer reads, not
// on the Go field name, for the same reason the goldens exist at all.
//
// Two properties of the match are asserted rather than assumed, because a
// redaction that silently does nothing leaves a test that still passes:
//
//   - The value must be non-empty (`[^"]+`). With `*` the pattern also matches a
//     value that has already regressed to "", writing the placeholder over the
//     regression — and these are the identity-bearing fields, exactly the ones
//     whose disappearance must not be masked.
//   - The key must occur exactly once. `ReplaceAll` rewrites the key at any
//     nesting depth, so a second one appearing later would be dropped from the
//     contract without anyone deciding that; a renamed key would match zero
//     times and redact nothing at all, just as quietly.
func redact(t *testing.T, doc []byte, key string) []byte {
	t.Helper()
	pattern := regexp.MustCompile(`"` + regexp.QuoteMeta(key) + `":"[^"]+"`)
	if n := len(pattern.FindAll(doc, -1)); n != 1 {
		t.Fatalf("redact %q: matched %d non-empty occurrences, want exactly 1: %s", key, n, doc)
	}
	return pattern.ReplaceAll(doc, []byte(`"`+key+`":"`+goldenPlaceholder+`"`))
}

// goldenWorkspace puts the case in its own working directory so that every
// path a command echoes back is a short relative one that the fixture chose.
//
// t.Chdir moves the process, not the test, so this is safe only while nothing
// in this package runs in parallel — which is true today and is a standing
// constraint, not a property. The first t.Parallel() added anywhere in package
// main will race against every case here.
// The symlink resolution matters on macOS, where the temporary directory lives
// under a symlinked /var and the board writers refuse a path that traverses
// one.
func goldenWorkspace(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	return root
}

func goldenWrite(t *testing.T, path string, raw []byte) string {
	t.Helper()
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// goldenRun is the only way a case reaches stdout: it fails the case if the
// command did not succeed cleanly, because a golden captured from a partially
// written document would pin bytes no consumer ever sees.
func goldenRun(t *testing.T, args ...string) []byte {
	t.Helper()
	var out, diagnostics bytes.Buffer
	if code := run(args, &out, &diagnostics); code != 0 {
		t.Fatalf("%v: exit=%d diagnostics=%s", args, code, &diagnostics)
	}
	if diagnostics.Len() != 0 {
		t.Fatalf("%v: unexpected diagnostics=%s", args, &diagnostics)
	}
	return out.Bytes()
}

// goldenSetup runs a prerequisite command for its effect on the board. Its
// stdout is discarded: only the command the case names is pinned.
func goldenSetup(t *testing.T, args ...string) {
	t.Helper()
	goldenRun(t, args...)
}

const goldenArchiveRules = "schema-version: 1\narchive-admission:\n  fields:\n    review: review-result\n    evidence: review-proof\n    resolution: disposition\n    promoted-to: promoted\n    children: child-ids\n  accepted-reviews: [pass]\n"

// goldenBoard is the fixture every board-shaped case starts from: an
// initialized board holding one task and one plan, with fixed ids.
func goldenBoard(t *testing.T) {
	t.Helper()
	goldenSetup(t, "init", "--dir", "tasks", "--json")
	goldenSetup(t, "create", "--dir", "tasks", "--title", "First task", "--json")
	goldenSetup(t, "create", "--dir", "tasks", "--kind", "plan", "--title", "A plan", "--json")
}

// goldenArchivable leaves TASK-1 sitting in done/ with the review fields the
// archive rules demand, and returns the digest of the bytes it wrote.
func goldenArchivable(t *testing.T) string {
	t.Helper()
	goldenSetup(t, "init", "--dir", "tasks", "--json")
	goldenSetup(t, "create", "--dir", "tasks", "--title", "archive", "--json")
	raw := []byte("---\nid: TASK-1\ntitle: archive\nreview-result: pass\nreview-proof: checked\n---\n")
	if err := os.Mkdir(filepath.Join("tasks", "done"), 0o755); err != nil {
		t.Fatal(err)
	}
	goldenWrite(t, filepath.Join("tasks", "done", "TASK-1.md"), raw)
	if err := os.Remove(filepath.Join("tasks", "todo", "TASK-1.md")); err != nil {
		t.Fatal(err)
	}
	goldenWrite(t, "archive-rules.yaml", []byte(goldenArchiveRules))
	return repairDigest(raw)
}

const (
	goldenOwnerToken = "0123456789abcdef0123456789abcdef"
	goldenRequestID  = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
)

// goldenCases names one emitted document each. The name is the golden file's
// basename, so renaming a case orphans its file.
var goldenCases = []struct {
	name string
	emit func(t *testing.T) []byte
}{
	{"init", func(t *testing.T) []byte {
		goldenWorkspace(t)
		return goldenRun(t, "init", "--dir", "tasks", "--json")
	}},
	{"create-task", func(t *testing.T) []byte {
		goldenWorkspace(t)
		goldenSetup(t, "init", "--dir", "tasks", "--json")
		return goldenRun(t, "create", "--dir", "tasks", "--title", "First task", "--json")
	}},
	{"create-plan", func(t *testing.T) []byte {
		goldenWorkspace(t)
		goldenSetup(t, "init", "--dir", "tasks", "--json")
		return goldenRun(t, "create", "--dir", "tasks", "--kind", "plan", "--title", "A plan", "--json")
	}},
	{"list", func(t *testing.T) []byte {
		goldenWorkspace(t)
		goldenBoard(t)
		return goldenRun(t, "list", "--dir", "tasks", "--json")
	}},
	{"ready", func(t *testing.T) []byte {
		goldenWorkspace(t)
		goldenBoard(t)
		return goldenRun(t, "ready", "--dir", "tasks", "--json")
	}},
	{"reserve-ids-adopt", func(t *testing.T) []byte {
		goldenWorkspace(t)
		goldenBoard(t)
		return goldenRun(t, "reserve-ids", "--dir", "tasks", "--adopt", "--json")
	}},
	{"reserve-ids-explicit", func(t *testing.T) []byte {
		goldenWorkspace(t)
		goldenBoard(t)
		goldenSetup(t, "reserve-ids", "--dir", "tasks", "--adopt", "--json")
		return goldenRun(t, "reserve-ids", "--dir", "tasks", "--id", "TASK-7", "--json")
	}},
	{"claim", func(t *testing.T) []byte {
		goldenWorkspace(t)
		goldenBoard(t)
		return goldenRun(t, "claim", "--dir", "tasks", "--id", "TASK-1", "--owner", "alice", "--token", goldenOwnerToken, "--json")
	}},
	{"release", func(t *testing.T) []byte {
		goldenWorkspace(t)
		goldenBoard(t)
		goldenSetup(t, "claim", "--dir", "tasks", "--id", "TASK-1", "--owner", "alice", "--token", goldenOwnerToken, "--json")
		return goldenRun(t, "release", "--dir", "tasks", "--id", "TASK-1", "--owner", "alice", "--token", goldenOwnerToken, "--json")
	}},
	{"transition", func(t *testing.T) []byte {
		goldenWorkspace(t)
		goldenBoard(t)
		goldenSetup(t, "claim", "--dir", "tasks", "--id", "TASK-1", "--owner", "alice", "--token", goldenOwnerToken, "--json")
		return goldenRun(t, "transition", "--dir", "tasks", "--id", "TASK-1", "--owner", "alice", "--token", goldenOwnerToken,
			"--request-id", goldenRequestID, "--from", "todo", "--to", "doing", "--json")
	}},
	{"show", func(t *testing.T) []byte {
		goldenWorkspace(t)
		goldenWrite(t, "card.md", validCompletionCard("P1", "- [x] done\n"))
		return goldenRun(t, "show", "card.md", "--json")
	}},
	{"validate-syntax-only", func(t *testing.T) []byte {
		goldenWorkspace(t)
		goldenWrite(t, "card.md", validCompletionCard("P1", "- [x] done\n"))
		return goldenRun(t, "validate", "card.md", "--json")
	}},
	{"validate-configured", func(t *testing.T) []byte {
		goldenWorkspace(t)
		goldenWrite(t, "card.md", validCompletionCard("P1", "- [x] done\n"))
		goldenWrite(t, "rules.yaml", []byte(completionRules))
		return goldenRun(t, "validate", "card.md", "--config", "rules.yaml", "--json")
	}},
	{"validate-completion", func(t *testing.T) []byte {
		goldenWorkspace(t)
		goldenWrite(t, "card.md", validCompletionCard("P1", "- [x] done\n"))
		goldenWrite(t, "rules.yaml", []byte(completionRules))
		return goldenRun(t, "validate-completion", "card.md", "--config", "rules.yaml", "--json")
	}},
	{"validate-policy", func(t *testing.T) []byte {
		goldenWorkspace(t)
		goldenWrite(t, "policy.yaml", []byte("schema-version: 1\nboard-policy:\n  zones: [manual]\n  zone-status: {manual: done}\n  transitions:\n    - from: manual\n      to: [todo]\n"))
		return goldenRun(t, "validate-policy", "policy.yaml", "--json")
	}},
	{"validate-context", func(t *testing.T) []byte {
		goldenWorkspace(t)
		goldenWrite(t, "context.json", []byte(validContextJSON))
		return goldenRun(t, "validate-context", "context.json", "--json")
	}},
	{"register-context", func(t *testing.T) []byte {
		goldenWorkspace(t)
		goldenSetup(t, "init", "--dir", "tasks", "--json")
		goldenWrite(t, "context.json", []byte(validContextJSON))
		return goldenRun(t, "register-context", "context.json", "--dir", "tasks", "--json")
	}},
	{"show-context", func(t *testing.T) []byte {
		goldenWorkspace(t)
		goldenSetup(t, "init", "--dir", "tasks", "--json")
		goldenWrite(t, "context.json", []byte(validContextJSON))
		goldenSetup(t, "register-context", "context.json", "--dir", "tasks", "--json")
		return goldenRun(t, "show-context", "--dir", "tasks", "--kind", "intent",
			"--id", "INTENT-0123456789abcdef0123456789abcdef", "--revision", "1", "--json")
	}},
	{"create-bundle", func(t *testing.T) []byte {
		goldenWorkspace(t)
		goldenSetup(t, "init", "--dir", "tasks", "--json")
		goldenWrite(t, "context.json", []byte(validContextJSON))
		goldenSetup(t, "register-context", "context.json", "--dir", "tasks", "--json")
		goldenWrite(t, "bundle.json", cliBundleInput(t, "First"))
		return goldenRun(t, "create-bundle", "bundle.json", "--dir", "tasks", "--adopt", "--json")
	}},
	{"plan-progress", func(t *testing.T) []byte {
		goldenWorkspace(t)
		goldenBoard(t)
		plan := filepath.Join("tasks", "plan", "PLAN-1.md")
		raw, err := os.ReadFile(plan)
		if err != nil {
			t.Fatal(err)
		}
		goldenWrite(t, plan, []byte(strings.Replace(string(raw), "id: PLAN-1\n", "id: PLAN-1\nchild-ids: [TASK-1]\n", 1)))
		goldenWrite(t, "archive.yaml", []byte(planProgressRules))
		return goldenRun(t, "plan-progress", "--dir", "tasks", "--id", "PLAN-1", "--rules", "archive.yaml", "--json")
	}},
	{"activate-policy", func(t *testing.T) []byte {
		goldenWorkspace(t)
		goldenSetup(t, "init", "--dir", "tasks", "--json")
		goldenWrite(t, "policy.yaml", []byte("schema-version: 1\nboard-policy: {}\n"))
		// authorityId is drawn from crypto/rand inside taskstore with no seam
		// to inject through, so its value leaves the contract; its key,
		// position and presence stay graded. Digest, by contrast, hashes the
		// policy bytes and so remains fully pinned.
		return redact(t, goldenRun(t, "activate-policy", "policy.yaml", "--dir", "tasks", "--json"), "authorityId")
	}},
	{"revise-policy", func(t *testing.T) []byte {
		goldenWorkspace(t)
		goldenSetup(t, "init", "--dir", "tasks", "--json")
		goldenWrite(t, "policy.yaml", []byte("schema-version: 1\nboard-policy: {}\n"))
		var activation bytes.Buffer
		var diagnostics bytes.Buffer
		if code := run([]string{"activate-policy", "policy.yaml", "--dir", "tasks", "--json"}, &activation, &diagnostics); code != 0 {
			t.Fatalf("activate: exit=%d diagnostics=%s", code, &diagnostics)
		}
		var prior struct {
			AuthorityID string `json:"authorityId"`
			Digest      string `json:"digest"`
		}
		decodeGolden(t, activation.Bytes(), &prior)
		goldenWrite(t, "revised.yaml", []byte("schema-version: 1\nboard-policy:\n  transitions:\n    - from: todo\n      to: [done]\n"))
		// Same crypto/rand authority id as above; a revision mints a new one.
		return redact(t, goldenRun(t, "revise-policy", "revised.yaml", "--dir", "tasks",
			"--expected-authority", prior.AuthorityID, "--expected-digest", prior.Digest, "--json"), "authorityId")
	}},
	{"archive", func(t *testing.T) []byte {
		goldenWorkspace(t)
		digest := goldenArchivable(t)
		return goldenRun(t, "archive", "--dir", "tasks", "--id", "TASK-1", "--source", "done/TASK-1.md",
			"--rules", "archive-rules.yaml", "--owner", "worker", "--request-id", goldenRequestID,
			"--expected-sha256", digest, "--adopt", "--json")
	}},
	{"adopt-archive-capacity", func(t *testing.T) []byte {
		goldenWorkspace(t)
		digest := goldenArchivable(t)
		goldenSetup(t, "archive", "--dir", "tasks", "--id", "TASK-1", "--source", "done/TASK-1.md",
			"--rules", "archive-rules.yaml", "--owner", "worker", "--request-id", goldenRequestID,
			"--expected-sha256", digest, "--adopt", "--json")
		return goldenRun(t, "adopt-archive-capacity", "--dir", "tasks", "--upgrade-id", strings.Repeat("b", 32), "--adopt", "--json")
	}},
	{"adopt-legacy-archive", func(t *testing.T) []byte {
		goldenWorkspace(t)
		goldenSetup(t, "init", "--dir", "tasks", "--json")
		goldenSetup(t, "create", "--dir", "tasks", "--title", "legacy", "--json")
		goldenWrite(t, "archive-rules.yaml", []byte(goldenArchiveRules))
		source := filepath.Join("tasks", "todo", "TASK-1.md")
		raw, err := os.ReadFile(source)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join("tasks", "_archive", "done"), 0o755); err != nil {
			t.Fatal(err)
		}
		target := filepath.Join("tasks", "_archive", "done", "TASK-1.md")
		if err := os.Rename(source, target); err != nil {
			t.Fatal(err)
		}
		// The command refuses a mode it was not told to expect, and the mode
		// the board writer produced depends on the process umask, so the
		// fixture reads it back rather than hardcoding it. It is an argument,
		// not an emitted value: nothing about the golden document moves.
		info, err := os.Stat(target)
		if err != nil {
			t.Fatal(err)
		}
		return goldenRun(t, "adopt-legacy-archive", "--dir", "tasks", "--id", "TASK-1",
			"--source", "_archive/done/TASK-1.md", "--rules", "archive-rules.yaml", "--owner", "operator",
			"--request-id", goldenRequestID, "--expected-sha256", repairDigest(raw),
			"--expected-mode", strconv.FormatUint(uint64(info.Mode().Perm()), 8),
			"--assertion", "legacy evidence", "--adopt", "--json")
	}},
	{"relocate", func(t *testing.T) []byte {
		goldenWorkspace(t)
		goldenSetup(t, "init", "--dir", "tasks", "--json")
		goldenWrite(t, "policy.yaml", []byte("schema-version: 2\nboard-policy:\n  relocations:\n    - from: todo\n      to: [plan]\n"))
		goldenSetup(t, "activate-policy", "policy.yaml", "--dir", "tasks", "--json")
		goldenSetup(t, "create", "--dir", "tasks", "--title", "relocate", "--json")
		raw, err := os.ReadFile(filepath.Join("tasks", "todo", "TASK-1.md"))
		if err != nil {
			t.Fatal(err)
		}
		return goldenRun(t, "relocate", "--dir", "tasks", "--id", "TASK-1", "--source", "todo/TASK-1.md",
			"--target", "plan/TASK-1.md", "--owner", "worker", "--request-id", goldenRequestID,
			"--expected-sha256", repairDigest(raw), "--adopt", "--json")
	}},
	{"repair-status", func(t *testing.T) []byte {
		goldenWorkspace(t)
		goldenSetup(t, "init", "--dir", "tasks", "--json")
		goldenSetup(t, "create", "--dir", "tasks", "--title", "repair", "--json")
		path := filepath.Join("tasks", "todo", "TASK-1.md")
		original, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		mutated := wrongStatusBytes(t, canonicalStatusBytes(t, original))
		goldenWrite(t, path, mutated)
		return goldenRun(t, "repair-status", "--dir", "tasks", "--id", "TASK-1", "--path", "todo/TASK-1.md",
			"--owner", "worker", "--request-id", goldenRequestID, "--expected-sha256", repairDigest(mutated),
			"--adopt", "--json")
	}},
	{"import-ids-preview", func(t *testing.T) []byte {
		goldenWorkspace(t)
		return goldenImport(t, "import-ids", "--repo", "synthetic", "--preview", "--json")
	}},
	{"import-ids-adopt", func(t *testing.T) []byte {
		goldenWorkspace(t)
		goldenSetup(t, "init", "--dir", "tasks", "--json")
		return goldenImport(t, "import-ids", "--repo", "synthetic", "--dir", "tasks", "--json")
	}},
	{"enable-shared", func(t *testing.T) []byte {
		goldenGitBoard(t)
		// namespaceId is drawn from crypto/rand (internal/taskstore/
		// shared_enable.go), exactly like authorityId, so no fixture can fix
		// it — the seam is the random source, not the path. Saying "derived
		// from the temporary directory" here would send the next reader
		// looking for a fixed path that would change nothing. Its key and
		// shape stay graded.
		return redact(t, goldenRun(t, "enable-shared", "--dir", "tasks", "--all-worktrees", "--json"), "namespaceId")
	}},
	// activate-policy on a shared board is the only place "scope" reads
	// "shared"; every other pinned document says "local", so a regression in
	// outputvocab.AuthorityScopeShared would otherwise emit nothing any golden
	// contains.
	{"activate-policy-shared", func(t *testing.T) []byte {
		goldenGitBoard(t)
		goldenSetup(t, "enable-shared", "--dir", "tasks", "--all-worktrees", "--json")
		goldenWrite(t, "policy.yaml", []byte("schema-version: 1\nboard-policy: {}\n"))
		// Same crypto/rand authority id as the local activation above.
		return redact(t, goldenRun(t, "activate-policy", "policy.yaml", "--dir", "tasks", "--all-worktrees", "--json"), "authorityId")
	}},
	// The replay of an already-published bundle is what makes "replayed" read
	// true. Pinning only first publications leaves the true spelling ungraded,
	// and it is the value a retrying caller keys on to tell "I did this" from
	// "this was already done".
	{"create-bundle-replay", func(t *testing.T) []byte {
		goldenWorkspace(t)
		goldenSetup(t, "init", "--dir", "tasks", "--json")
		goldenWrite(t, "context.json", []byte(validContextJSON))
		goldenSetup(t, "register-context", "context.json", "--dir", "tasks", "--json")
		goldenWrite(t, "bundle.json", cliBundleInput(t, "First"))
		goldenSetup(t, "create-bundle", "bundle.json", "--dir", "tasks", "--adopt", "--json")
		return goldenRun(t, "create-bundle", "bundle.json", "--dir", "tasks", "--json")
	}},
	{"rejoin-board-status", func(t *testing.T) []byte {
		goldenGitBoard(t)
		return goldenRun(t, "rejoin-board", "--dir", "tasks", "--status", "--json")
	}},
	{"rejoin-board-prepare", func(t *testing.T) []byte {
		source, target := rejoinCLIFixture(t)
		work := t.TempDir()
		doc := goldenRun(t, "rejoin-board", "--dir", target, "--prepare", "--json",
			"--source", source, "--rejoin-id", strings.Repeat("d", 32), "--source-fenced",
			"--plan", filepath.Join(work, "plan.json"), "--payload", filepath.Join(work, "payload.bin"))
		// Every redaction below traces to one cause that no argument can fix:
		// EnableShared mints the namespace id from crypto/rand
		// (internal/taskstore/shared_enable.go:99) with no injection point on
		// the CLI, and that id is written into the ledgers, so every digest
		// computed over them - the plan, the payload, each file's original and
		// target content, and the capacity artifact whose own name is its
		// digest - changes on every run. The owners are absolute temporary
		// paths for the same reason inspect-worktrees redacts its locations: a
		// Git repository cannot be created at a fixed path.
		// What this golden does grade, and what the all-zero status document
		// could not: the mode and phase vocabulary, the storage protocols, and
		// the full element shape of files[] and artifacts[] - every key, the
		// role vocabulary, the journal file names, the presence booleans, the
		// file mode and the byte lengths.
		for _, key := range []string{
			"sourceOwner", "targetOwner", "sourceNamespace", "targetNamespace",
			"planSha256", "payloadSha256", "originalSha256", "targetSha256", "sha256",
		} {
			doc = redactAll(t, doc, key)
		}
		// The byte lengths fall to the same cause one step removed: the
		// journals record the board's absolute path, so their size follows the
		// length of a temporary directory name, which differs between runs and
		// between machines. -1 marks them as dropped without breaking the
		// numeric type the contract declares here.
		for _, key := range []string{"originalLength", "targetLength", "length"} {
			doc = redactAllNumbers(t, doc, key)
		}
		return redactEmbeddedDigest(t, doc)
	}},
	{"inspect-worktrees", func(t *testing.T) []byte {
		root := goldenGitBoard(t)
		doc := goldenRun(t, "inspect-worktrees", "--repo", root, "--board", "tasks", "--json")
		// Every redaction here has the same cause: a Git repository cannot be
		// created at a fixed path, so the document's absolute locations cannot
		// be made deterministic. namespaceKey is deliberately NOT among them —
		// internal/githistory/worktrees.go hashes the board name, which is the
		// fixture's own --board argument, so it is a constant this golden can
		// and does pin. Redacting it would have dropped the key derivation
		// itself out of the contract for no gain.
		// What stays graded is the rest of the report - the board name, the
		// namespace key, the per-worktree head, branch and flag fields, the
		// readiness verdict, and the position of every key.
		for _, key := range []string{"repository", "commonDirectory", "path"} {
			doc = redact(t, doc, key)
		}
		return doc
	}},
}

// redactAll is redact for a key that legitimately repeats: every element of
// files[] carries the same digest keys, so the one-match guard would reject
// them even though each value is equally unpinnable.
func redactAll(t *testing.T, doc []byte, key string) []byte {
	t.Helper()
	pattern := regexp.MustCompile(`"` + regexp.QuoteMeta(key) + `":"[^"]+"`)
	if len(pattern.FindAll(doc, -1)) == 0 {
		t.Fatalf("redactAll %q: no non-empty occurrence: %s", key, doc)
	}
	return pattern.ReplaceAll(doc, []byte(`"`+key+`":"`+goldenPlaceholder+`"`))
}

// redactAllNumbers is redactAll for a numeric field: a string placeholder
// would change the value's JSON type, which is itself part of the contract.
func redactAllNumbers(t *testing.T, doc []byte, key string) []byte {
	t.Helper()
	pattern := regexp.MustCompile(`"` + regexp.QuoteMeta(key) + `":[0-9]+`)
	if len(pattern.FindAll(doc, -1)) == 0 {
		t.Fatalf("redactAllNumbers %q: no occurrence: %s", key, doc)
	}
	return pattern.ReplaceAll(doc, []byte(`"`+key+`":-1`))
}

// redactEmbeddedDigest drops the one digest that is not a value of its own:
// the capacity artifact's file name contains its sha256, so the surrounding
// name stays graded only if the hex run alone is replaced.
func redactEmbeddedDigest(t *testing.T, doc []byte) []byte {
	t.Helper()
	pattern := regexp.MustCompile(`[0-9a-f]{64}`)
	if n := len(pattern.FindAll(doc, -1)); n != 1 {
		t.Fatalf("redactEmbeddedDigest: matched %d digests, want exactly 1: %s", n, doc)
	}
	return pattern.ReplaceAll(doc, []byte(goldenPlaceholder))
}

// goldenImport drives the import command with a fixed synthetic history. The
// real scanner reads a Git repository's whole object graph, which no fixture
// can pin; injecting the scan keeps the emitted document - the thing under
// test - entirely determined by the fixture.
func goldenImport(t *testing.T, args ...string) []byte {
	t.Helper()
	scan := func(context.Context, string, string) (githistory.Report, error) {
		// The ref carries a synthetic name and an all-f object id so that
		// githistory.Ref's own keys are graded by a golden; an empty slice
		// would have left the element shape ungraded.
		refs := []githistory.Ref{{Name: "refs/heads/fixture", OID: strings.Repeat("f", 40)}}
		return githistory.Report{Refs: refs, IDs: []string{"TASK-90", "PLAN-7"}}, nil
	}
	var out, diagnostics bytes.Buffer
	if code := runImportIDsWithScan(args, &out, &diagnostics, scan); code != 0 {
		t.Fatalf("%v: exit=%d diagnostics=%s", args, code, &diagnostics)
	}
	return out.Bytes()
}

// goldenGitBoard builds the one fixture shape that a temporary directory alone
// cannot provide: a committed board inside a real Git worktree. The three
// commands that read Git state refuse anything less.
func goldenGitBoard(t *testing.T) string {
	t.Helper()
	root := goldenWorkspace(t)
	gitFixture(t, root, "init", "-b", "fixture")
	if err := taskstore.Init("tasks"); err != nil {
		t.Fatal(err)
	}
	gitFixture(t, root, "add", "tasks")
	gitFixture(t, root, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid",
		"-c", "commit.gpgsign=false", "commit", "-m", "fixture")
	return root
}

// gitFixture pins the commit identity and both timestamps so that the commit
// hash the worktree report prints is itself deterministic and stays inside the
// graded contract, rather than having to be redacted out of it.
//
// The config files are sealed off too, because the hash is a graded value and
// the machine's own Git configuration reaches into how the object is formed:
// init.defaultObjectFormat alone changes it, and core.autocrlf, a commit
// template or a signing format would each fail these cases with a diff that
// says nothing about the contract — the usual reason a golden suite gets
// switched off.
//
// The seal must live here, on the fixture's own command, and must NOT be a
// process-wide t.Setenv: internal/githistory's scanner.safe refuses to scan at
// all while GIT_CONFIG_GLOBAL or GIT_CONFIG_SYSTEM is set anywhere in the
// environment, because such an override can silently redirect a scan. Sealing
// the fixture is nevertheless enough — the only Git-derived value inside the
// contract is the commit hash, and the fixture is what creates it. The
// commands under test read Git state but form no objects, and they are meant
// to see the real environment, guard and all.
func gitFixture(t *testing.T, root string, args ...string) {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", root}, args...)...)
	command.Env = append(os.Environ(),
		"GIT_AUTHOR_DATE=2000-01-01T00:00:00+00:00",
		"GIT_COMMITTER_DATE=2000-01-01T00:00:00+00:00",
		"GIT_CONFIG_GLOBAL="+os.DevNull,
		"GIT_CONFIG_SYSTEM="+os.DevNull,
		"GIT_CONFIG_NOSYSTEM=1",
	)
	if raw, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v %s", args, err, raw)
	}
}

func decodeGolden(t *testing.T, raw []byte, into any) {
	t.Helper()
	if err := json.Unmarshal(raw, into); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
}

// TestGoldenStdout is the byte-level half of the output contract: the tests
// beside it grade one key or one invariant, while this one grades the whole
// emitted document at once and so notices a change nobody thought to assert.
func TestGoldenStdout(t *testing.T) {
	assertNoOrphanGoldens(t)
	for _, testCase := range goldenCases {
		t.Run(testCase.name, func(t *testing.T) {
			path := filepath.Join("testdata", "golden", testCase.name+".json")
			absolute, err := filepath.Abs(path)
			if err != nil {
				t.Fatal(err)
			}
			// The case chdirs into its own workspace, so the golden file has
			// to be addressed absolutely from here on.
			got := testCase.emit(t)
			if *updateGolden {
				if err := os.WriteFile(absolute, got, 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(absolute)
			if err != nil {
				t.Fatalf("read golden: %v\nrun with -update to create %s", err, path)
			}
			if !bytes.Equal(got, want) {
				t.Errorf("stdout no longer matches the published contract.\n"+
					"golden (%s):\n%s\nactual:\n%s\n"+
					"If this change is intended, decide whether it needs an outputformat.Version bump, "+
					"then regenerate with: GOWORK=off go test ./cmd/taskchain-task-manager -run TestGoldenStdout -update",
					path, want, got)
			}
		})
	}
}

// assertNoOrphanGoldens fails when testdata/golden holds a file no case emits.
// Without it the suite is silent in exactly one direction: a renamed or deleted
// case leaves its old bytes on disk, still committed and still looking like a
// pinned contract, while nothing compares against them any more. A stale golden
// is worse than a missing one, because it reads as evidence.
func assertNoOrphanGoldens(t *testing.T) {
	t.Helper()
	expected := make(map[string]bool, len(goldenCases))
	for _, testCase := range goldenCases {
		expected[testCase.name+".json"] = true
	}
	entries, err := os.ReadDir(filepath.Join("testdata", "golden"))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if !expected[entry.Name()] {
			t.Errorf("testdata/golden/%s matches no case in goldenCases: delete it, or restore the case that emitted it", entry.Name())
		}
	}
}
