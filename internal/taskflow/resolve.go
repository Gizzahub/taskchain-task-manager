package taskflow

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// ResolveRequest closes one issue card in place. BySet distinguishes an
// omitted --by from a present flag with an empty value. Now pins resolved-at.
type ResolveRequest struct {
	Path       string
	Resolution string
	By         string
	BySet      bool
	Now        time.Time
}

// ResolveResult is the text the command prints. Summary is unquoted; the
// file stores that same sentence as a YAML double-quoted scalar.
type ResolveResult struct {
	Path       string
	Resolution string
	ResolvedAt string
	Summary    string
}

// Resolve records resolution, resolved-at, and resolution-summary on an issue
// card. It does not move the card or rewrite disposition or the title.
// fixed and duplicate accept an optional --by card; wontfix and invalid reject
// --by before any write. A declared task-projection snapshots the tasks tree
// and the git index and restores both when the write fails.
func Resolve(ctx context.Context, root string, req ResolveRequest) (result ResolveResult, retErr error) {
	resolution := strings.TrimSpace(req.Resolution)
	switch resolution {
	case "fixed", "wontfix", "duplicate", "invalid":
	default:
		return ResolveResult{}, fmt.Errorf("invalid resolution %q (allowed: fixed, wontfix, duplicate, invalid)", req.Resolution)
	}
	if _, err := os.Stat(req.Path); err != nil {
		if os.IsNotExist(err) {
			return ResolveResult{}, fmt.Errorf("file not found: %s", req.Path)
		}
		return ResolveResult{}, err
	}
	if filepath.Base(filepath.Dir(req.Path)) != "issue" {
		return ResolveResult{}, fmt.Errorf("%s is not under issue/; ce task resolve only closes issue cards", req.Path)
	}
	lookup, err := resolveBy(root, resolution, req)
	if err != nil {
		return ResolveResult{}, err
	}
	recordable, err := frontmatterRecordable(req.Path)
	if err != nil {
		return ResolveResult{}, err
	}
	if !recordable {
		return ResolveResult{}, errors.New("issue card has no frontmatter to record a resolution")
	}
	declared, owned, err := taskProjection(root)
	if err != nil {
		return ResolveResult{}, err
	}
	if declared {
		if projectionOwns(root, req.Path, owned) {
			return ResolveResult{}, fmt.Errorf("refusing lifecycle mutation of marker-owned projection: %s", req.Path)
		}
		snap, snapErr := snapshotProjection(ctx, root)
		if snapErr != nil {
			return ResolveResult{}, snapErr
		}
		defer func() {
			if retErr == nil {
				return
			}
			if rerr := snap.restore(); rerr != nil {
				retErr = fmt.Errorf("%w; restore projection: %v", retErr, rerr)
			}
		}()
	}
	summary := "Resolved as " + resolution + "."
	if lookup != "" {
		summary = "Resolved as " + resolution + " by " + lookup + "."
	}
	now := req.Now
	if now.IsZero() {
		now = time.Now()
	}
	resolvedAt := now.UTC().Format(time.RFC3339)
	if _, err := stampFrontmatterFields(req.Path, [][2]string{
		{"resolution", resolution},
		{"resolved-at", resolvedAt},
		{"resolution-summary", strconv.Quote(summary)},
	}); err != nil {
		return ResolveResult{}, err
	}
	return ResolveResult{
		Path:       req.Path,
		Resolution: resolution,
		ResolvedAt: resolvedAt,
		Summary:    summary,
	}, nil
}

func resolveBy(root, resolution string, req ResolveRequest) (string, error) {
	if !req.BySet {
		return "", nil
	}
	token := strings.TrimSpace(req.By)
	if strings.HasPrefix(token, "file:") {
		token = strings.TrimSpace(strings.TrimPrefix(token, "file:"))
	}
	switch resolution {
	case "wontfix":
		return "", errors.New("--by is not accepted with wontfix; it records no card")
	case "invalid":
		return "", errors.New("--by is not accepted with invalid; it records no card")
	}
	if token == "" {
		if resolution == "fixed" {
			return "", errors.New("--by needs a TASK id")
		}
		return "", errors.New("--by needs an ISSUE id")
	}
	cards, err := FindCards(root, false)
	if err != nil {
		return "", fmt.Errorf("cannot verify --by target: %s", err.Error())
	}
	var matches []*Card
	for _, card := range cards {
		if cardMatchesToken(root, card, token) {
			matches = append(matches, card)
		}
	}
	want := "TASK"
	if resolution == "duplicate" {
		want = "ISSUE"
	}
	if len(matches) == 1 && referenceKind(matches[0]) == want {
		return token, nil
	}
	if len(matches) > 1 {
		return "", fmt.Errorf("cannot verify --by target: %s matches %d cards", token, len(matches))
	}
	if want == "TASK" {
		return "", fmt.Errorf("--by names %q, which is not a TASK card in this corpus; fixed is resolved by a TASK card", token)
	}
	return "", fmt.Errorf("--by names %q, which is not an ISSUE card in this corpus; duplicate points at the original ISSUE card", token)
}

func referenceKind(card *Card) string {
	id := strings.TrimSpace(card.ID)
	if strings.HasPrefix(id, "TASK-") {
		return "TASK"
	}
	if strings.HasPrefix(id, "ISSUE-") {
		return "ISSUE"
	}
	switch kindDir(card.TasksRel) {
	case "issue":
		return "ISSUE"
	case "plan":
		return "PLAN"
	case "backlog":
		return "BACKLOG"
	default:
		return "TASK"
	}
}

func cardMatchesToken(root string, card *Card, token string) bool {
	slash := filepath.ToSlash(token)
	if strings.Contains(slash, "/") || filepath.IsAbs(token) {
		return cardPathMatch(root, card, token)
	}
	if id := normalizeCardID(token); id != "" && id == normalizeCardID(card.ID) {
		return true
	}
	if card.ID != "" && card.ID == token {
		return true
	}
	stem := strings.TrimSuffix(filepath.Base(card.TasksRel), ".md")
	return token == stem
}

func cardPathMatch(root string, card *Card, token string) bool {
	slash := filepath.ToSlash(filepath.Clean(token))
	rels := []string{
		filepath.ToSlash(card.TasksRel),
		filepath.ToSlash(card.RepoRel()),
	}
	for _, rel := range rels {
		if slash == rel {
			return true
		}
	}
	cleaned := filepath.Clean(token)
	candidates := []string{
		filepath.Join(root, filepath.FromSlash(card.RepoRel())),
		filepath.Join(root, tasksDirName(), filepath.FromSlash(card.TasksRel)),
	}
	for _, abs := range candidates {
		if cleaned == filepath.Clean(abs) {
			return true
		}
	}
	return false
}

func frontmatterRecordable(path string) (bool, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return false, err
	}
	text := string(content)
	if !strings.HasPrefix(text, "---\n") && !strings.HasPrefix(text, "---\r\n") {
		return false, nil
	}
	rest := text
	if strings.HasPrefix(rest, "---\r\n") {
		rest = rest[len("---\r\n"):]
	} else {
		rest = rest[len("---\n"):]
	}
	for _, line := range strings.Split(rest, "\n") {
		if strings.TrimRight(line, "\r") == "---" {
			return true, nil
		}
	}
	return false, nil
}

func taskProjection(root string) (bool, []string, error) {
	raw, err := os.ReadFile(filepath.Join(root, "ce-tasks.yaml"))
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil, nil
		}
		return false, nil, fmt.Errorf("read ce-tasks.yaml: %w", err)
	}
	var top map[string]yaml.Node
	if err := yaml.Unmarshal(raw, &top); err != nil {
		return false, nil, fmt.Errorf("ce-tasks.yaml task-projection: %w", err)
	}
	node, ok := top["task-projection"]
	if !ok {
		return false, nil, nil
	}
	return true, collectProjectionPaths(&node), nil
}

func collectProjectionPaths(node *yaml.Node) []string {
	if node == nil {
		return nil
	}
	var out []string
	var walk func(*yaml.Node)
	walk = func(n *yaml.Node) {
		if n == nil {
			return
		}
		switch n.Kind {
		case yaml.DocumentNode, yaml.SequenceNode:
			for _, child := range n.Content {
				walk(child)
			}
		case yaml.MappingNode:
			for i := 0; i+1 < len(n.Content); i += 2 {
				walk(n.Content[i+1])
			}
		case yaml.ScalarNode:
			text := strings.TrimSpace(n.Value)
			if text == "" {
				return
			}
			if strings.ContainsAny(text, `/\\`) || strings.HasSuffix(text, ".md") {
				out = append(out, text)
			}
		}
	}
	walk(node)
	return out
}

func projectionOwns(root, target string, paths []string) bool {
	absTarget := target
	if !filepath.IsAbs(absTarget) {
		absTarget = filepath.Join(root, target)
	}
	absTarget = filepath.Clean(absTarget)
	slashTarget := filepath.ToSlash(filepath.Clean(target))
	for _, path := range paths {
		if filepath.ToSlash(filepath.Clean(path)) == slashTarget {
			return true
		}
		abs := path
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(root, path)
		}
		if filepath.Clean(abs) == absTarget {
			return true
		}
	}
	return false
}

type snapFile struct {
	path string
	data []byte
	mode os.FileMode
}

type projectionSnapshot struct {
	files     []snapFile
	indexPath string
	index     []byte
	indexMode os.FileMode
	hadIndex  bool
}

func snapshotProjection(ctx context.Context, root string) (projectionSnapshot, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	var snap projectionSnapshot
	tasksPath := filepath.Join(root, tasksDirName())
	err := filepath.WalkDir(tasksPath, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		snap.files = append(snap.files, snapFile{path: path, data: data, mode: info.Mode().Perm()})
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		return projectionSnapshot{}, fmt.Errorf("snapshot tasks tree: %w", err)
	}
	rel, err := gitOutput(ctx, root, "rev-parse", "--git-path", "index")
	if err != nil {
		return projectionSnapshot{}, fmt.Errorf("cannot snapshot git index: %w", err)
	}
	indexPath := strings.TrimSpace(rel)
	if indexPath == "" {
		return projectionSnapshot{}, errors.New("cannot snapshot git index: git returned an empty index path")
	}
	if !filepath.IsAbs(indexPath) {
		indexPath = filepath.Join(root, indexPath)
	}
	info, err := os.Stat(indexPath)
	if err != nil {
		return projectionSnapshot{}, fmt.Errorf("cannot snapshot git index: %w", err)
	}
	data, err := os.ReadFile(indexPath)
	if err != nil {
		return projectionSnapshot{}, fmt.Errorf("cannot snapshot git index: %w", err)
	}
	snap.indexPath = indexPath
	snap.index = append([]byte(nil), data...)
	snap.indexMode = info.Mode().Perm()
	snap.hadIndex = true
	return snap, nil
}

func (s projectionSnapshot) restore() error {
	var errs []error
	for _, file := range s.files {
		if err := os.WriteFile(file.path, file.data, file.mode); err != nil {
			errs = append(errs, err)
			continue
		}
		if err := os.Chmod(file.path, file.mode); err != nil {
			errs = append(errs, err)
		}
	}
	if s.hadIndex {
		if err := os.WriteFile(s.indexPath, s.index, s.indexMode); err != nil {
			errs = append(errs, err)
		} else if err := os.Chmod(s.indexPath, s.indexMode); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
