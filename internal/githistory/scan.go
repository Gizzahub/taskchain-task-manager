package githistory

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

var objectID = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)

type Ref struct {
	Name string `json:"name"`
	OID  string `json:"oid"`
}
type Report struct {
	Refs  []Ref    `json:"refs"`
	Trees int      `json:"trees"`
	Blobs int      `json:"blobs"`
	IDs   []string `json:"ids"`
}

// Scan reads a captured local ref snapshot. It never fetches or mutates Git.
func Scan(ctx context.Context, repo, board string) (Report, error) {
	return scanWithHook(ctx, repo, board, nil)
}

func scanWithHook(ctx context.Context, repo, board string, afterRead func() error) (Report, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	s, err := newScanner(repo, board)
	if err != nil {
		return Report{}, err
	}
	if err := s.safe(ctx); err != nil {
		return Report{}, err
	}
	refs, before, err := s.refs(ctx)
	if err != nil {
		return Report{}, err
	}
	commits := map[string]bool{}
	for _, ref := range refs {
		peeled, err := s.run(ctx, "", "rev-parse", "--verify", ref.OID+"^{commit}")
		if err != nil {
			return Report{}, fmt.Errorf("ref %s must resolve to a commit: %w", ref.Name, err)
		}
		oid := strings.TrimSpace(string(peeled))
		if !objectID.MatchString(oid) {
			return Report{}, errors.New("invalid peeled commit OID")
		}
		commits[oid] = true
	}
	trees := []string{}
	if len(commits) > 0 {
		ids := make([]string, 0, len(commits))
		for id := range commits {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		raw, err := s.run(ctx, strings.Join(ids, "\n")+"\n", "log", "--stdin", "--full-history", "--no-renames", "--no-show-signature", "--no-notes", "--no-patch", "--no-color", "--no-graph", "--format=%T", "--", s.board+"/")
		if err != nil {
			return Report{}, err
		}
		set := map[string]bool{}
		for _, oid := range strings.Fields(string(raw)) {
			if !objectID.MatchString(oid) {
				return Report{}, errors.New("invalid history tree OID")
			}
			set[oid] = true
		}
		if len(set) > 8192 {
			return Report{}, errors.New("history exceeds 8192 trees")
		}
		for oid := range set {
			trees = append(trees, oid)
		}
		sort.Strings(trees)
	}
	blobs, err := s.blobs(ctx, trees)
	if err != nil {
		return Report{}, err
	}
	ids, err := s.readBlobs(ctx, blobs)
	if err != nil {
		return Report{}, err
	}
	if afterRead != nil {
		if err := afterRead(); err != nil {
			return Report{}, err
		}
	}
	if err := s.safe(ctx); err != nil {
		return Report{}, err
	}
	_, after, err := s.refs(ctx)
	if err != nil {
		return Report{}, err
	}
	if !bytes.Equal(before, after) {
		return Report{}, errors.New("Git refs changed during scan; retry")
	}
	if err := ctx.Err(); err != nil {
		return Report{}, err
	}
	return Report{Refs: refs, Trees: len(trees), Blobs: len(blobs), IDs: ids}, nil
}

func (s scanner) refs(ctx context.Context) ([]Ref, []byte, error) {
	raw, err := s.run(ctx, "", "for-each-ref", "--sort=refname", "--format=%(refname) %(objectname)")
	if err != nil {
		return nil, nil, err
	}
	if !utf8.Valid(raw) {
		return nil, nil, errors.New("Git ref names must be valid UTF-8")
	}
	refs := []Ref{}
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if line == "" {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) != 2 || !strings.HasPrefix(parts[0], "refs/") || !objectID.MatchString(parts[1]) {
			return nil, nil, errors.New("invalid Git ref record")
		}
		refs = append(refs, Ref{Name: parts[0], OID: parts[1]})
	}
	if len(refs) > 4096 {
		return nil, nil, errors.New("history exceeds 4096 refs")
	}
	return refs, raw, nil
}

func (s scanner) blobs(ctx context.Context, trees []string) ([]string, error) {
	seen := map[string]bool{}
	for _, tree := range trees {
		raw, err := s.run(ctx, "", "ls-tree", "-r", "-z", "--full-tree", tree, "--", s.board+"/")
		if err != nil {
			return nil, err
		}
		for _, entry := range strings.Split(string(raw), "\x00") {
			if entry == "" {
				continue
			}
			meta, file, ok := strings.Cut(entry, "\t")
			parts := strings.Fields(meta)
			if !ok || len(parts) != 3 || !objectID.MatchString(parts[2]) {
				return nil, errors.New("invalid Git tree entry")
			}
			if !IsCardPath(file, s.board) {
				continue
			}
			if parts[1] != "blob" || (parts[0] != "100644" && parts[0] != "100755") {
				return nil, fmt.Errorf("historical card is not regular: %s", file)
			}
			seen[parts[2]] = true
		}
		if len(seen) > 65536 {
			return nil, errors.New("history exceeds 65536 card blobs")
		}
	}
	blobs := make([]string, 0, len(seen))
	for oid := range seen {
		blobs = append(blobs, oid)
	}
	sort.Strings(blobs)
	return blobs, nil
}
