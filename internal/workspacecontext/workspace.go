// Package workspacecontext provides a bounded, read-only lookup across an
// explicitly declared set of task boards.
package workspacecontext

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
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
	SchemaVersion    = 1
	OutputVersion    = 1
	MaxManifestBytes = 256 << 10
	MaxRepositories  = 32
	MaxQueryIDs      = 256
	MaxIDBytes       = 128
	MaxCardsPerBoard = 4096
	MaxBoardBytes    = 64 << 20
	MaxBoardNodes    = 8192
)

// scanAfterReadHook is test-only instrumentation for proving the retry guard.
var scanAfterReadHook func(string)

type Manifest struct {
	SchemaVersion int          `json:"schemaVersion"`
	Repositories  []Repository `json:"repositories"`
	CardIDs       []string     `json:"cardIds"`
}
type Repository struct {
	RepositoryID string `json:"repositoryId"`
	Root         string `json:"root"`
	Board        string `json:"board"`
}

func (r *Repository) UnmarshalJSON(raw []byte) error {
	if err := exactObjectKeys(raw, map[string]bool{"repositoryId": true, "root": true, "board": true}); err != nil {
		return err
	}
	var v struct {
		RepositoryID string `json:"repositoryId"`
		Root         string `json:"root"`
		Board        string `json:"board"`
	}
	if err := strictDecode(raw, &v); err != nil {
		return err
	}
	r.RepositoryID, r.Root, r.Board = v.RepositoryID, v.Root, v.Board
	return nil
}

type Result struct {
	RequestedID string  `json:"requestedId"`
	CardID      string  `json:"cardId"`
	Status      string  `json:"status"`
	Matches     []Match `json:"matches"`
}
type Match struct {
	RepositoryID string    `json:"repositoryId"`
	CardID       string    `json:"cardId"`
	Path         string    `json:"path"`
	Card         card.View `json:"card"`
}
type Output struct {
	OutputVersion int      `json:"outputVersion"`
	Results       []Result `json:"results"`
}

func (m *Manifest) UnmarshalJSON(raw []byte) error {
	if err := validateJSON(raw); err != nil {
		return err
	}
	if err := exactObjectKeys(raw, map[string]bool{"schemaVersion": true, "repositories": true, "cardIds": true}); err != nil {
		return err
	}
	var v struct {
		SchemaVersion int          `json:"schemaVersion"`
		Repositories  []Repository `json:"repositories"`
		CardIDs       []string     `json:"cardIds"`
	}
	if err := strictDecode(raw, &v); err != nil {
		return err
	}
	m.SchemaVersion, m.Repositories, m.CardIDs = v.SchemaVersion, v.Repositories, v.CardIDs
	return validateManifest(*m)
}

func validateManifest(m Manifest) error {
	if m.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported manifest schemaVersion %d", m.SchemaVersion)
	}
	if len(m.Repositories) == 0 || len(m.Repositories) > MaxRepositories {
		return fmt.Errorf("repositories must contain 1..%d items", MaxRepositories)
	}
	if len(m.CardIDs) == 0 || len(m.CardIDs) > MaxQueryIDs {
		return fmt.Errorf("cardIds must contain 1..%d items", MaxQueryIDs)
	}
	seenIDs := map[string]bool{}
	for _, id := range m.CardIDs {
		if len(id) == 0 || len(id) > MaxIDBytes || !utf8.ValidString(id) {
			return errors.New("cardIds contains an invalid ID")
		}
		parsed, err := cardid.Parse(id)
		if err != nil {
			return err
		}
		if seenIDs[parsed.Key()] {
			return fmt.Errorf("duplicate card ID identity %q", parsed.Key())
		}
		seenIDs[parsed.Key()] = true
	}
	seen := map[string]bool{}
	for _, r := range m.Repositories {
		if len(r.RepositoryID) > MaxManifestBytes || len(r.Root) > MaxManifestBytes || len(r.Board) > MaxManifestBytes || !utf8.ValidString(r.RepositoryID) || !utf8.ValidString(r.Root) || !utf8.ValidString(r.Board) {
			return errors.New("manifest field exceeds byte or UTF-8 bound")
		}
		if !validRepositoryID(r.RepositoryID) || r.Root == "" || !utf8.ValidString(r.Root) || !filepath.IsAbs(r.Root) || filepath.Clean(r.Root) != r.Root || r.Board == "" || !utf8.ValidString(r.Board) || pathUnsafe(r.Board) {
			return errors.New("repository requires bounded ID, absolute clean root and safe relative board")
		}
		if seen[r.RepositoryID] {
			return fmt.Errorf("duplicate repositoryId %q", r.RepositoryID)
		}
		seen[r.RepositoryID] = true
	}
	if manifestJSONLen(m) > MaxManifestBytes {
		return fmt.Errorf("manifest exceeds %d bytes", MaxManifestBytes)
	}
	return nil
}

func manifestJSONLen(m Manifest) int {
	n := len(`{"schemaVersion":1,"repositories":[`) + len(`,"cardIds":[`)
	for i, r := range m.Repositories {
		if i > 0 {
			n++
		}
		n += len(`{"repositoryId":,"root":,"board":}`) + jsonStringLen(r.RepositoryID) + jsonStringLen(r.Root) + jsonStringLen(r.Board)
	}
	for i, id := range m.CardIDs {
		if i > 0 {
			n++
		}
		n += jsonStringLen(id)
	}
	return n + 2
}

func jsonStringLen(s string) int {
	n := 2
	for _, r := range s {
		switch r {
		case '"', '\\':
			n += 2
		case '\b', '\f', '\n', '\r', '\t':
			n += 2
		case '\u2028', '\u2029':
			n += 6
		default:
			if r < 0x20 {
				n += 6
			} else {
				n += utf8.RuneLen(r)
			}
		}
	}
	return n
}

func DecodeManifest(raw []byte) (Manifest, error) {
	if len(raw) == 0 || len(raw) > MaxManifestBytes || !utf8.Valid(raw) {
		return Manifest{}, errors.New("manifest exceeds byte or UTF-8 bound")
	}
	var m Manifest
	dec := json.NewDecoder(bytes.NewReader(raw))
	if err := dec.Decode(&m); err != nil {
		return Manifest{}, fmt.Errorf("decode manifest: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err == nil {
		return Manifest{}, errors.New("manifest has trailing JSON")
	} else if !errors.Is(err, io.EOF) {
		return Manifest{}, errors.New("manifest has invalid trailing JSON")
	}
	return m, nil
}

func Lookup(ctx context.Context, m Manifest) (Output, error) {
	if err := validateManifest(m); err != nil {
		return Output{}, err
	}
	type board struct {
		id, root, board string
		cards           []Match
	}
	boards := make([]board, len(m.Repositories))
	roots := map[string]bool{}
	commons := map[string]bool{}
	for i, r := range m.Repositories {
		rootInfo, statErr := os.Lstat(r.Root)
		if statErr != nil || rootInfo.Mode()&os.ModeSymlink != 0 || !rootInfo.IsDir() {
			return Output{}, fmt.Errorf("repository %s root is not a real directory", r.RepositoryID)
		}
		root, err := filepath.EvalSymlinks(r.Root)
		if err != nil {
			return Output{}, fmt.Errorf("repository %s: %w", r.RepositoryID, err)
		}
		if roots[root] {
			return Output{}, fmt.Errorf("duplicate canonical Git root %q", root)
		}
		roots[root] = true
		boardPath := filepath.Join(root, filepath.FromSlash(r.Board))
		info, err := os.Lstat(boardPath)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return Output{}, fmt.Errorf("repository %s board is not a real directory", r.RepositoryID)
		}
		loc, err := githistory.LocateBoard(ctx, boardPath)
		if err != nil || loc == nil || loc.Repository != root || loc.Board != r.Board {
			return Output{}, fmt.Errorf("repository %s unsafe board boundary: %v", r.RepositoryID, err)
		}
		if commons[loc.CommonDirectory] {
			return Output{}, fmt.Errorf("duplicate Git common directory %q", loc.CommonDirectory)
		}
		commons[loc.CommonDirectory] = true
		cards, err := scanBoard(ctx, root, r.Board)
		if err != nil {
			return Output{}, fmt.Errorf("repository %s: %w", r.RepositoryID, err)
		}
		for j := range cards {
			cards[j].RepositoryID = r.RepositoryID
		}
		boards[i] = board{r.RepositoryID, root, r.Board, cards}
	}
	out := Output{OutputVersion: OutputVersion, Results: make([]Result, 0, len(m.CardIDs))}
	for _, requested := range m.CardIDs {
		want, _ := cardid.Parse(requested)
		matches := []Match{}
		for _, b := range boards {
			for _, match := range b.cards {
				got, _ := cardid.Parse(match.CardID)
				if got.Prefix == want.Prefix && got.Number == want.Number {
					matches = append(matches, match)
				}
			}
		}
		sort.Slice(matches, func(i, j int) bool {
			if matches[i].RepositoryID != matches[j].RepositoryID {
				return matches[i].RepositoryID < matches[j].RepositoryID
			}
			return matches[i].Path < matches[j].Path
		})
		status := "missing"
		if len(matches) == 1 {
			status = "found"
		} else if len(matches) > 1 {
			status = "ambiguous"
		}
		cardID := want.Key()
		out.Results = append(out.Results, Result{RequestedID: requested, CardID: cardID, Status: status, Matches: matches})
	}
	return out, nil
}

func scanBoard(ctx context.Context, root, board string) ([]Match, error) {
	base := filepath.Join(root, filepath.FromSlash(board))
	if err := preflightBoard(base); err != nil {
		return nil, err
	}
	before, err := fingerprint(base)
	if err != nil {
		return nil, err
	}
	entries, err := taskstore.ListReadOnly(base)
	if err != nil {
		return nil, err
	}
	var out []Match
	for _, entry := range entries {
		if len(out) >= MaxCardsPerBoard {
			return nil, fmt.Errorf("board exceeds %d cards", MaxCardsPerBoard)
		}
		id, err := cardid.Parse(entry.Card.ID)
		if err != nil {
			return nil, fmt.Errorf("invalid card ID in %s: %w", entry.Path, err)
		}
		out = append(out, Match{CardID: id.Key(), Path: entry.Path, Card: entry.Card})
	}
	if scanAfterReadHook != nil {
		scanAfterReadHook(base)
	}
	after, err := fingerprint(base)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(before.digest, after.digest) || !sameNodes(before.nodes, after.nodes) {
		return nil, errors.New("board changed during inspection; retry")
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

func preflightBoard(root string) error {
	count, nodes, total := 0, 0, int64(0)
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		nodes++
		if nodes > MaxBoardNodes {
			return fmt.Errorf("board exceeds %d filesystem entries", MaxBoardNodes)
		}
		if path != root && d.Name() == ".git" {
			return errors.New("nested Git metadata boundary")
		}
		if d.Type()&os.ModeSymlink != 0 {
			return errors.New("symlink board entry rejected")
		}
		if d.IsDir() && path != root && (strings.HasPrefix(d.Name(), ".") || cardpath.IsExcludedDirectory(d.Name())) {
			return filepath.SkipDir
		}
		if !d.IsDir() {
			info, statErr := d.Info()
			if statErr != nil {
				return statErr
			}
			if !info.Mode().IsRegular() {
				return fmt.Errorf("board contains non-regular file: %s", path)
			}
			total += info.Size()
			if total > MaxBoardBytes {
				return fmt.Errorf("board exceeds %d bytes", MaxBoardBytes)
			}
			if filepath.Ext(d.Name()) == ".md" && !cardpath.IsDocumentation(d.Name()) && !strings.HasPrefix(d.Name(), ".") {
				count++
				if count > MaxCardsPerBoard {
					return fmt.Errorf("board exceeds %d cards", MaxCardsPerBoard)
				}
			}
		}
		return nil
	})
}

type boardSnapshot struct {
	digest []byte
	nodes  map[string]os.FileInfo
}

func fingerprint(root string) (boardSnapshot, error) {
	h := sha256.New()
	nodes, total := 0, int64(0)
	identities := map[string]os.FileInfo{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		nodes++
		if nodes > MaxBoardNodes {
			return fmt.Errorf("board exceeds %d filesystem entries", MaxBoardNodes)
		}
		rel, _ := filepath.Rel(root, path)
		if path != root && d.Name() == ".git" {
			return errors.New("nested Git metadata boundary")
		}
		if d.IsDir() && path != root && (strings.HasPrefix(d.Name(), ".") || cardpath.IsExcludedDirectory(d.Name())) {
			return filepath.SkipDir
		}
		if d.Type()&os.ModeSymlink != 0 {
			return errors.New("symlink in board")
		}
		info, e := d.Info()
		if e != nil {
			return e
		}
		if !d.IsDir() && !info.Mode().IsRegular() {
			return fmt.Errorf("board contains non-regular file: %s", path)
		}
		identities[filepath.ToSlash(rel)] = info
		if !d.IsDir() {
			total += info.Size()
			if total > MaxBoardBytes {
				return fmt.Errorf("board exceeds %d bytes", MaxBoardBytes)
			}
		}
		fmt.Fprintf(h, "%s\x00%d\x00%d\x00", filepath.ToSlash(rel), info.Size(), info.ModTime().UnixNano())
		if !d.IsDir() {
			declaredSize := info.Size()
			f, openErr := os.Open(path)
			if openErr != nil {
				return openErr
			}
			n, copyErr := io.Copy(h, io.LimitReader(f, MaxBoardBytes-total+declaredSize+1))
			closeErr := f.Close()
			if copyErr != nil {
				return copyErr
			}
			if closeErr != nil {
				return closeErr
			}
			if n > MaxBoardBytes-total+declaredSize {
				return fmt.Errorf("board exceeds %d bytes", MaxBoardBytes)
			}
			total = total - declaredSize + n
		}
		return nil
	})
	return boardSnapshot{digest: h.Sum(nil), nodes: identities}, err
}

func sameNodes(a, b map[string]os.FileInfo) bool {
	if len(a) != len(b) {
		return false
	}
	for path, left := range a {
		right, ok := b[path]
		if !ok || left.Mode() != right.Mode() || left.Size() != right.Size() || left.ModTime() != right.ModTime() || !os.SameFile(left, right) {
			return false
		}
	}
	return true
}

func validRepositoryID(s string) bool {
	if s == "" || len(s) > 64 {
		return false
	}
	for _, c := range s {
		if !(c == '-' || c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}
func pathUnsafe(s string) bool {
	return path.IsAbs(s) || path.Clean(s) != s || s == "." || s == ".." || strings.HasPrefix(s, "../") || strings.ContainsAny(s, "\\\r\n*?[") || strings.IndexByte(s, 0) >= 0
}

func exactObjectKeys(raw []byte, allowed map[string]bool) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return err
	}
	for key := range fields {
		if !allowed[key] {
			return fmt.Errorf("unknown or non-canonical JSON field %q", key)
		}
	}
	return nil
}

func strictDecode(raw []byte, dst any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return err
	}
	var extra any
	if err := dec.Decode(&extra); err == nil {
		return errors.New("trailing JSON")
	} else if !errors.Is(err, io.EOF) {
		return errors.New("invalid trailing JSON")
	}
	return nil
}
func validateJSON(raw []byte) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := walkJSON(dec); err != nil {
		return err
	}
	var extra any
	if err := dec.Decode(&extra); err == nil {
		return errors.New("trailing JSON")
	} else if !errors.Is(err, io.EOF) {
		return errors.New("invalid trailing JSON")
	}
	return nil
}

func walkJSON(dec *json.Decoder) error {
	t, err := dec.Token()
	if err != nil {
		return err
	}
	if d, ok := t.(json.Delim); ok {
		switch d {
		case '{':
			seen := map[string]bool{}
			for dec.More() {
				token, tokenErr := dec.Token()
				if tokenErr != nil {
					return tokenErr
				}
				key, ok := token.(string)
				if !ok {
					return errors.New("invalid JSON object key")
				}
				if seen[key] {
					return fmt.Errorf("duplicate JSON field %q", key)
				}
				seen[key] = true
				if err := walkJSON(dec); err != nil {
					return err
				}
			}
			_, err = dec.Token()
		case '[':
			for dec.More() {
				if err := walkJSON(dec); err != nil {
					return err
				}
			}
			_, err = dec.Token()
		}
	}
	return err
}
