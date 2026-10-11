package boardruntime

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Gizzahub/taskchain-task-manager/internal/boardpolicy"
	"github.com/Gizzahub/taskchain-task-manager/internal/cardid"
	"github.com/Gizzahub/taskchain-task-manager/internal/githistory"
	"github.com/Gizzahub/taskchain-task-manager/internal/taskflow"
	"github.com/Gizzahub/taskchain-task-manager/internal/taskruntime"
)

// CleanupEntry is one finalized card. JSON keys stay PascalCase: the struct
// is untagged so encoding matches the cleanup contract.
type CleanupEntry struct {
	SourceOID    string
	Path         string
	ID           string
	Artifacts    []string
	ArtifactDirs []string
	Blocked      []string
	Removable    bool
}

// CleanupCandidates lists finalized cards and every reason one cannot be removed.
// It does not mutate the tree and does not take the mutation lock.
func CleanupCandidates(ctx context.Context, root, source string) ([]CleanupEntry, error) {
	if err := gitEnv(); err != nil {
		return nil, err
	}
	if err := SourceReady(ctx, root, source); err != nil {
		return nil, err
	}
	oid, err := revParse(ctx, root, source)
	if err != nil {
		return nil, fmt.Errorf("read cleanup source: %w", err)
	}
	cards, err := taskflow.FindCards(root, false)
	if err != nil {
		return nil, err
	}
	tracked, err := trackedSet(ctx, root)
	if err != nil {
		return nil, err
	}
	tasksDir := taskflow.TasksDirName()
	var entries []CleanupEntry
	highest := map[string]uint64{}
	haveFloor := map[string]bool{}
	seen := map[string]int{}
	for _, card := range cards {
		if card == nil {
			continue
		}
		path := filepath.ToSlash(card.RepoRel())
		if same, cmpErr := matchesSource(ctx, root, oid, path); cmpErr == nil && same {
			if id, parseErr := cardid.Parse(card.ID); parseErr == nil && (!haveFloor[id.Prefix] || id.Number > highest[id.Prefix]) {
				highest[id.Prefix] = id.Number
				haveFloor[id.Prefix] = true
			}
		}
		if !finalizedCard(card) {
			continue
		}
		entry := CleanupEntry{SourceOID: oid, Path: path, ID: card.ID}
		if _, parseErr := cardid.Parse(card.ID); parseErr != nil {
			entry.Blocked = append(entry.Blocked, "id is not canonical")
		} else if previous, exists := seen[card.ID]; exists {
			entries[previous].Blocked = append(entries[previous].Blocked, "duplicate id: "+card.ID)
			entry.Blocked = append(entry.Blocked, "duplicate id: "+card.ID)
		} else {
			seen[card.ID] = len(entries)
		}
		if !reviewed(card) {
			entry.Blocked = append(entry.Blocked, "quality-review is not pass, conditional, or waived with evidence")
		}
		if !tracked[path] {
			entry.Blocked = append(entry.Blocked, "not tracked by git")
		} else if same, cmpErr := matchesSource(ctx, root, oid, path); cmpErr != nil {
			entry.Blocked = append(entry.Blocked, cmpErr.Error())
		} else if !same {
			entry.Blocked = append(entry.Blocked, "differs from configured source tip")
		}
		entries = append(entries, entry)
	}
	for i := range entries {
		id, parseErr := cardid.Parse(entries[i].ID)
		if parseErr != nil {
			continue
		}
		if haveFloor[id.Prefix] && id.Number == highest[id.Prefix] {
			if err := proveFloor(ctx, root, tasksDir, id); err != nil {
				entries[i].Blocked = append(entries[i].Blocked, err.Error())
			}
		}
		files, dirs, artErr := ownedArtifacts(root, tasksDir, entries[i].ID, entries[i].Path)
		if artErr != nil {
			entries[i].Blocked = append(entries[i].Blocked, artErr.Error())
		}
		entries[i].ArtifactDirs = dirs
		entries[i].Artifacts = files
		for _, artifact := range files {
			if !tracked[artifact] {
				entries[i].Blocked = append(entries[i].Blocked, "owned artifact is not tracked: "+artifact)
				continue
			}
			same, cmpErr := matchesSource(ctx, root, oid, artifact)
			if cmpErr != nil {
				entries[i].Blocked = append(entries[i].Blocked, "owned artifact "+artifact+": "+cmpErr.Error())
			} else if !same {
				entries[i].Blocked = append(entries[i].Blocked, "owned artifact differs from configured source tip: "+artifact)
			}
		}
		legacy := filepath.ToSlash(filepath.Join("tmp", "task", entries[i].ID))
		if _, statErr := os.Lstat(filepath.Join(root, legacy)); statErr == nil {
			entries[i].Blocked = append(entries[i].Blocked, "legacy owned artifacts cleanup unsupported: "+legacy)
		}
	}
	refs, err := referenceFiles(ctx, root, tracked)
	if err != nil {
		return nil, err
	}
	for i := range entries {
		for _, ref := range refs {
			if ref.path == entries[i].Path || containsPath(entries[i].Artifacts, ref.path) {
				continue
			}
			if ref.tracked && durableReviewReceipt(root, tasksDir, ref.path) {
				continue
			}
			if blocked, reason := namesEntry(tasksDir, ref, entries[i]); blocked {
				entries[i].Blocked = append(entries[i].Blocked, "referenced by "+ref.path+" ("+reason+")")
				break
			}
		}
		entries[i].Removable = len(entries[i].Blocked) == 0
	}
	return entries, nil
}

// AnnotateRuntime adds the runtime blocker to each group. Preview calls it
// without the mutation lock; apply rechecks inside ProtectCleanup.
func AnnotateRuntime(ctx context.Context, root string, entries []CleanupEntry) error {
	svc := taskruntime.NewService(root, taskruntime.NewExecutor())
	for i := range entries {
		if err := svc.CleanupBlocker(ctx, cleanupAliases(entries[i].ID, entries[i].Path)); err != nil {
			entries[i].Blocked = append(entries[i].Blocked, err.Error())
			entries[i].Removable = false
		}
	}
	return nil
}

func cleanupAliases(id, path string) []string {
	base := filepath.Base(path)
	return []string{id, path, base, strings.TrimSuffix(base, filepath.Ext(base))}
}

func finalizedCard(card *taskflow.Card) bool {
	if card.Status != taskflow.StatusDone {
		return false
	}
	parts := strings.Split(card.TasksRel, "/")
	for _, part := range parts[:len(parts)-1] {
		if taskflow.IsStorageDir(part) {
			return true
		}
	}
	return card.Zone == "done"
}

func reviewed(card *taskflow.Card) bool {
	verdict := strings.ToLower(strings.TrimSpace(card.QualityReview))
	evidence := strings.TrimSpace(card.QualityReviewEvidence)
	ok := verdict == "pass" || verdict == "conditional" || verdict == "waived"
	return ok && evidence != ""
}

func proveFloor(ctx context.Context, root, tasksDir string, id cardid.ID) error {
	scanRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return fmt.Errorf("cannot prove durable ID history: %w", err)
	}
	report, err := githistory.Scan(ctx, scanRoot, tasksDir)
	if err != nil {
		return fmt.Errorf("cannot prove durable ID history: %w", err)
	}
	var max uint64
	for _, raw := range report.IDs {
		parsed, parseErr := cardid.Parse(raw)
		if parseErr != nil || parsed.Prefix != id.Prefix {
			continue
		}
		if parsed.Number > max {
			max = parsed.Number
		}
	}
	if max < id.Number {
		return fmt.Errorf("candidate ID is absent from allocator history")
	}
	return nil
}

// LiveDonePath reports a card in the board-configured done zone. Storage and
// evidence paths are not live. The tasks directory name is not hardcoded.
func LiveDonePath(tasksDir, path string) bool {
	rel := filepath.ToSlash(path)
	prefix := filepath.ToSlash(tasksDir) + "/"
	if tasksDir == "" || !strings.HasPrefix(rel, prefix) {
		return false
	}
	parts := strings.Split(strings.TrimPrefix(rel, prefix), "/")
	if len(parts) < 2 {
		return false
	}
	for _, part := range parts {
		if part == "evidence" || taskflow.IsStorageDir(part) {
			return false
		}
	}
	index, _, ok := taskflow.ZoneSegment(parts)
	if !ok {
		return false
	}
	return parts[index] == (boardpolicy.Policy{}).DoneZone()
}

func trackedSet(ctx context.Context, root string) (map[string]bool, error) {
	out, err := git(ctx, root, "ls-files", "-z")
	if err != nil {
		return nil, fmt.Errorf("list tracked files: %w", err)
	}
	set := map[string]bool{}
	for _, path := range bytes.Split(out, []byte{0}) {
		if len(path) == 0 {
			continue
		}
		set[filepath.ToSlash(string(path))] = true
	}
	return set, nil
}

func matchesSource(ctx context.Context, root, oid, path string) (bool, error) {
	want, err := git(ctx, root, "show", oid+":"+filepath.ToSlash(path))
	if err != nil {
		return false, fmt.Errorf("not present at configured source tip")
	}
	if err := contained(root, path, false); err != nil {
		return false, err
	}
	got, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
	if err != nil {
		return false, fmt.Errorf("cannot read candidate: %w", err)
	}
	staged, err := git(ctx, root, "show", ":"+filepath.ToSlash(path))
	if err != nil || !bytes.Equal(staged, want) {
		return false, fmt.Errorf("index differs from configured source tip")
	}
	return bytes.Equal(got, want), nil
}

func contained(root, rel string, allowDirectory bool) error {
	rel = filepath.ToSlash(rel)
	if filepath.IsAbs(rel) || rel == "." || filepath.Clean(rel) != rel {
		return fmt.Errorf("unsafe cleanup path: %s", rel)
	}
	current := root
	parts := strings.Split(rel, "/")
	for i, part := range parts {
		if part == ".." || part == "" {
			return fmt.Errorf("unsafe cleanup path: %s", rel)
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if os.IsNotExist(err) && allowDirectory {
			return nil
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("cleanup path contains symlink: %s", rel)
		}
		if i < len(parts)-1 || allowDirectory {
			if !info.IsDir() {
				return fmt.Errorf("cleanup parent is not a directory: %s", rel)
			}
		} else if !info.Mode().IsRegular() {
			return fmt.Errorf("cleanup candidate is not regular: %s", rel)
		}
	}
	return nil
}

func containsPath(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func sortLongest(dirs []string) {
	sort.Slice(dirs, func(i, j int) bool { return len(dirs[i]) > len(dirs[j]) })
}
