package workspace

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/Gizzahub/taskchain-task-manager/internal/card"
	"github.com/Gizzahub/taskchain-task-manager/internal/cardid"
	"github.com/Gizzahub/taskchain-task-manager/internal/cardpath"
	"github.com/Gizzahub/taskchain-task-manager/internal/githistory"
	"github.com/Gizzahub/taskchain-task-manager/internal/taskstore"
)

const (
	SnapshotMaxRepositories  = 32
	SnapshotMaxQueryIDs      = 256
	SnapshotMaxIDBytes       = 128
	SnapshotMaxCardsPerBoard = 4096
	SnapshotMaxBoardBytes    = 64 << 20
	SnapshotMaxBoardNodes    = 8192
)

// SnapshotOutput is the result of a bounded multi-card lookup.
type SnapshotOutput struct {
	Results []SnapshotResult `json:"results"`
}

// SnapshotResult classifies one requested numeric card identity.
type SnapshotResult struct {
	RequestedID string          `json:"requestedId"`
	CardID      string          `json:"cardId"`
	Status      string          `json:"status"`
	Matches     []SnapshotMatch `json:"matches"`
}

// SnapshotMatch identifies one card using only workspace-relative data.
type SnapshotMatch struct {
	Repository string    `json:"repository"`
	CardID     string    `json:"cardId"`
	Path       string    `json:"path"`
	Card       card.View `json:"card"`
}

type snapshotRepository struct {
	name      string
	root      string
	board     string
	boardPath string
	commonDir string
}

type snapshotBoardState struct {
	digest []byte
	nodes  map[string]os.FileInfo
}

// QuerySnapshot scans the repositories from an already loaded workspace
// manifest and returns every match for the requested card IDs. It does not
// acquire taskstore writer locks. Callers that need a stable observation rely
// on this function's bounded per-board before/after check; it does not
// participate in shared-writer coordination or provide an atomic workspace
// snapshot.
func QuerySnapshot(manifest Manifest, requestedIDs []string) (SnapshotOutput, error) {
	return querySnapshot(manifest, requestedIDs, nil)
}

func querySnapshot(manifest Manifest, requestedIDs []string, afterRead func(string)) (SnapshotOutput, error) {
	repositories, ids, err := prepareSnapshot(manifest, requestedIDs)
	if err != nil {
		return SnapshotOutput{}, err
	}
	requested := make(map[string]cardid.ID, len(ids))
	for _, id := range ids {
		requested[id.id.Key()] = id.id
	}
	matchesByID := make(map[string][]SnapshotMatch, len(ids))
	for _, id := range ids {
		matchesByID[id.id.Key()] = []SnapshotMatch{}
	}
	for _, repository := range repositories {
		matches, err := scanSnapshotBoard(repository, requested, afterRead)
		if err != nil {
			return SnapshotOutput{}, fmt.Errorf("snapshot repository %q: %w", repository.name, err)
		}
		for _, match := range matches {
			id, _ := cardid.Parse(match.CardID)
			matchesByID[id.Key()] = append(matchesByID[id.Key()], match)
		}
	}

	out := SnapshotOutput{Results: make([]SnapshotResult, 0, len(ids))}
	for _, id := range ids {
		matches := matchesByID[id.id.Key()]
		sort.Slice(matches, func(i, j int) bool {
			if matches[i].Repository != matches[j].Repository {
				return matches[i].Repository < matches[j].Repository
			}
			if matches[i].CardID != matches[j].CardID {
				return matches[i].CardID < matches[j].CardID
			}
			return matches[i].Path < matches[j].Path
		})
		status := "missing"
		if len(matches) == 1 {
			status = "found"
		} else if len(matches) > 1 {
			status = "ambiguous"
		}
		out.Results = append(out.Results, SnapshotResult{
			RequestedID: id.raw,
			CardID:      id.id.Key(),
			Status:      status,
			Matches:     matches,
		})
	}
	return out, nil
}

type requestedCardID struct {
	raw string
	id  cardid.ID
}

func prepareSnapshot(manifest Manifest, requestedIDs []string) ([]snapshotRepository, []requestedCardID, error) {
	if err := validateSnapshotRepositoryCount(len(manifest.Repositories)); err != nil {
		return nil, nil, err
	}
	if len(requestedIDs) == 0 || len(requestedIDs) > SnapshotMaxQueryIDs {
		return nil, nil, fmt.Errorf("snapshot requires 1..%d card IDs", SnapshotMaxQueryIDs)
	}
	ids := make([]requestedCardID, 0, len(requestedIDs))
	seenIDs := make(map[string]bool, len(requestedIDs))
	for _, raw := range requestedIDs {
		if len(raw) == 0 || len(raw) > SnapshotMaxIDBytes || !utf8.ValidString(raw) {
			return nil, nil, errors.New("snapshot card ID exceeds byte or UTF-8 bound")
		}
		id, err := cardid.Parse(raw)
		if err != nil {
			return nil, nil, fmt.Errorf("invalid snapshot card ID: %w", err)
		}
		key := id.Key()
		if seenIDs[key] {
			return nil, nil, fmt.Errorf("duplicate snapshot card ID identity %q", key)
		}
		seenIDs[key] = true
		ids = append(ids, requestedCardID{raw: raw, id: id})
	}

	repositories := append([]Repository(nil), manifest.Repositories...)
	sort.Slice(repositories, func(i, j int) bool { return repositories[i].Name < repositories[j].Name })
	prepared := make([]snapshotRepository, 0, len(repositories))
	seenNames := map[string]bool{}
	seenRoots := map[string]bool{}
	seenBoards := map[string]bool{}
	seenCommonDirs := map[string]bool{}
	ctx := context.Background()
	for _, repository := range repositories {
		if !repositoryName.MatchString(repository.Name) || seenNames[repository.Name] {
			return nil, nil, fmt.Errorf("invalid or duplicate workspace repository name %q", repository.Name)
		}
		seenNames[repository.Name] = true
		if !filepath.IsAbs(repository.Path) || filepath.Clean(repository.Path) != repository.Path ||
			!filepath.IsAbs(repository.Board) || filepath.Clean(repository.Board) != repository.Board {
			return nil, nil, fmt.Errorf("repository %q has an invalid canonical path", repository.Name)
		}
		rootInfo, err := os.Lstat(repository.Path)
		if err != nil || rootInfo.Mode()&os.ModeSymlink != 0 || !rootInfo.IsDir() {
			return nil, nil, fmt.Errorf("repository %q root is not a real directory", repository.Name)
		}
		root, err := filepath.EvalSymlinks(repository.Path)
		if err != nil || root != repository.Path {
			return nil, nil, fmt.Errorf("repository %q root is not canonical", repository.Name)
		}
		if seenRoots[root] {
			return nil, nil, fmt.Errorf("duplicate canonical Git root %q", root)
		}
		seenRoots[root] = true
		boardInfo, err := os.Lstat(repository.Board)
		if err != nil || boardInfo.Mode()&os.ModeSymlink != 0 || !boardInfo.IsDir() {
			return nil, nil, fmt.Errorf("repository %q board is not a real directory", repository.Name)
		}
		boardPath, err := filepath.EvalSymlinks(repository.Board)
		if err != nil || boardPath != repository.Board {
			return nil, nil, fmt.Errorf("repository %q board is not canonical", repository.Name)
		}
		boardRelative, err := filepath.Rel(root, boardPath)
		if err != nil || !validRelativePath(filepath.ToSlash(boardRelative)) {
			return nil, nil, fmt.Errorf("repository %q board is outside its repository", repository.Name)
		}
		boardRelative = filepath.ToSlash(boardRelative)
		for previous := range seenBoards {
			if overlaps(previous, boardPath) {
				return nil, nil, fmt.Errorf("overlapping workspace boards %q and %q", previous, boardPath)
			}
		}
		seenBoards[boardPath] = true
		location, err := githistory.LocateBoard(ctx, boardPath)
		if err != nil || location == nil || location.Repository != root || location.Board != boardRelative {
			return nil, nil, fmt.Errorf("repository %q has an unsafe Git board boundary: %v", repository.Name, err)
		}
		if seenCommonDirs[location.CommonDirectory] {
			return nil, nil, fmt.Errorf("duplicate Git common directory %q", location.CommonDirectory)
		}
		seenCommonDirs[location.CommonDirectory] = true
		prepared = append(prepared, snapshotRepository{
			name:      repository.Name,
			root:      root,
			board:     boardRelative,
			boardPath: boardPath,
			commonDir: location.CommonDirectory,
		})
	}
	return prepared, ids, nil
}

func validateSnapshotRepositoryCount(count int) error {
	if count < 1 || count > SnapshotMaxRepositories {
		return fmt.Errorf("snapshot requires 1..%d repositories", SnapshotMaxRepositories)
	}
	return nil
}

func scanSnapshotBoard(repository snapshotRepository, requested map[string]cardid.ID, afterRead func(string)) ([]SnapshotMatch, error) {
	if err := preflightSnapshotBoard(repository.boardPath); err != nil {
		return nil, err
	}
	before, err := fingerprintSnapshotBoard(repository.boardPath)
	if err != nil {
		return nil, err
	}
	entries, err := taskstore.ListReadOnly(repository.boardPath)
	if err != nil {
		return nil, err
	}
	out := []SnapshotMatch{}
	for _, entry := range entries {
		id, err := cardid.Parse(entry.Card.ID)
		if err != nil {
			return nil, fmt.Errorf("invalid listed card ID %q: %w", entry.Card.ID, err)
		}
		if _, ok := requested[id.Key()]; !ok {
			continue
		}
		out = append(out, SnapshotMatch{
			Repository: repository.name,
			CardID:     id.Key(),
			Path:       entry.Path,
			Card:       entry.Card,
		})
	}
	if afterRead != nil {
		afterRead(repository.boardPath)
	}
	after, err := fingerprintSnapshotBoard(repository.boardPath)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(before.digest, after.digest) || !sameSnapshotNodes(before.nodes, after.nodes) {
		return nil, errors.New("board changed during inspection; retry")
	}
	location, err := githistory.LocateBoard(context.Background(), repository.boardPath)
	if err != nil || location == nil || location.Repository != repository.root || location.Board != repository.board || location.CommonDirectory != repository.commonDir {
		return nil, errors.New("Git repository or board boundary changed during inspection; retry")
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CardID != out[j].CardID {
			return out[i].CardID < out[j].CardID
		}
		return out[i].Path < out[j].Path
	})
	return out, nil
}

func preflightSnapshotBoard(root string) error {
	cards, nodes, total := 0, 0, int64(0)
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		nodes++
		if nodes > SnapshotMaxBoardNodes {
			return fmt.Errorf("board exceeds %d filesystem entries", SnapshotMaxBoardNodes)
		}
		if path != root && entry.Name() == ".git" {
			return errors.New("nested Git metadata boundary")
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return errors.New("symlink board entry rejected")
		}
		if entry.IsDir() && path != root && (strings.HasPrefix(entry.Name(), ".") || cardpath.IsExcludedDirectory(entry.Name())) {
			return filepath.SkipDir
		}
		if !entry.IsDir() {
			info, err := entry.Info()
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() {
				return fmt.Errorf("board contains non-regular file: %s", path)
			}
			total += info.Size()
			if total > SnapshotMaxBoardBytes {
				return fmt.Errorf("board exceeds %d bytes", SnapshotMaxBoardBytes)
			}
			if isSnapshotCardCandidate(entry.Name()) {
				cards++
				if cards > SnapshotMaxCardsPerBoard {
					return fmt.Errorf("board exceeds %d cards", SnapshotMaxCardsPerBoard)
				}
			}
		}
		return nil
	})
}

func fingerprintSnapshotBoard(root string) (snapshotBoardState, error) {
	rootFS, err := os.OpenRoot(root)
	if err != nil {
		return snapshotBoardState{}, err
	}
	defer rootFS.Close()
	h := sha256.New()
	nodes, cards, total := 0, 0, int64(0)
	identities := map[string]os.FileInfo{}
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		nodes++
		if nodes > SnapshotMaxBoardNodes {
			return fmt.Errorf("board exceeds %d filesystem entries", SnapshotMaxBoardNodes)
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if path != root && entry.Name() == ".git" {
			return errors.New("nested Git metadata boundary")
		}
		if entry.IsDir() && path != root && (strings.HasPrefix(entry.Name(), ".") || cardpath.IsExcludedDirectory(entry.Name())) {
			return filepath.SkipDir
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return errors.New("symlink in board")
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !entry.IsDir() && !info.Mode().IsRegular() {
			return fmt.Errorf("board contains non-regular file: %s", path)
		}
		if !entry.IsDir() && isSnapshotCardCandidate(entry.Name()) {
			cards++
			if cards > SnapshotMaxCardsPerBoard {
				return fmt.Errorf("board exceeds %d cards", SnapshotMaxCardsPerBoard)
			}
		}
		identities[filepath.ToSlash(rel)] = info
		fmt.Fprintf(h, "%s\x00%d\x00%d\x00", filepath.ToSlash(rel), info.Size(), info.ModTime().UnixNano())
		if !entry.IsDir() {
			declaredSize := info.Size()
			total += declaredSize
			if total > SnapshotMaxBoardBytes {
				return fmt.Errorf("board exceeds %d bytes", SnapshotMaxBoardBytes)
			}
			file, err := rootFS.Open(filepath.Clean(rel))
			if err != nil {
				return err
			}
			openedInfo, statErr := file.Stat()
			if statErr != nil {
				return errors.Join(statErr, file.Close())
			}
			if !os.SameFile(info, openedInfo) || !openedInfo.Mode().IsRegular() {
				return errors.Join(errors.New("board file changed during inspection"), file.Close())
			}
			n, copyErr := io.Copy(h, io.LimitReader(file, SnapshotMaxBoardBytes-total+declaredSize+1))
			closeErr := file.Close()
			if copyErr != nil {
				return copyErr
			}
			if closeErr != nil {
				return closeErr
			}
			if n > SnapshotMaxBoardBytes-total+declaredSize {
				return fmt.Errorf("board exceeds %d bytes", SnapshotMaxBoardBytes)
			}
			total = total - declaredSize + n
		}
		return nil
	})
	return snapshotBoardState{digest: h.Sum(nil), nodes: identities}, err
}

func isSnapshotCardCandidate(name string) bool {
	return filepath.Ext(name) == ".md" && !cardpath.IsDocumentation(name) && !strings.HasPrefix(name, ".")
}

func sameSnapshotNodes(before, after map[string]os.FileInfo) bool {
	if len(before) != len(after) {
		return false
	}
	for path, left := range before {
		right, ok := after[path]
		if !ok || left.Mode() != right.Mode() || left.Size() != right.Size() || left.ModTime() != right.ModTime() || !os.SameFile(left, right) {
			return false
		}
	}
	return true
}
