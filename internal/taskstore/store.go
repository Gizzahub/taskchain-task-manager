// Package taskstore provides a local, bounded task-card store.
package taskstore

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/Gizzahub/taskchain-task-manager/internal/card"
	"gopkg.in/yaml.v3"
)

type Entry struct {
	Path string    `json:"path"`
	Card card.View `json:"card"`
}

type CreateRequest struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

var canonicalID = regexp.MustCompile(`^TASK-[1-9][0-9]*$`)
var knownDirs = []string{"todo", "doing", "review", "blocked", "done", "issue", "plan", "backlog", "archive"}

func Init(dir string) error {
	if dir == "" {
		return errors.New("task board directory is empty")
	}
	dir = filepath.Clean(dir)
	if info, err := os.Lstat(dir); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("task board root is a symlink: %s", dir)
		}
		if !info.IsDir() {
			return fmt.Errorf("task board root is not a directory: %s", dir)
		}
	} else if errors.Is(err, fs.ErrNotExist) {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create task board root: %w", err)
		}
	} else {
		return fmt.Errorf("inspect task board root: %w", err)
	}
	r, err := os.OpenRoot(dir)
	if err != nil {
		return fmt.Errorf("open task board root: %w", err)
	}
	defer r.Close()
	info, err := r.Lstat("todo")
	if err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return errors.New("task board todo is not a real directory")
		}
		return nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("inspect task board todo: %w", err)
	}
	if err := r.Mkdir("todo", 0o755); err != nil {
		return fmt.Errorf("create task board todo: %w", err)
	}
	return nil
}

func List(dir string) (entries []Entry, err error) {
	r, err := openBoard(dir)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	unlock, err := lock(r)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, unlock()) }()
	return listLocked(r)
}

func Create(dir string, req CreateRequest) (entry Entry, err error) {
	r, err := openBoard(dir)
	if err != nil {
		return Entry{}, err
	}
	defer r.Close()
	unlock, err := lock(r)
	if err != nil {
		return Entry{}, err
	}
	defer func() { err = errors.Join(err, unlock()) }()
	entries, err := listLocked(r)
	if err != nil {
		return Entry{}, err
	}
	used := make(map[string]bool, len(entries))
	var max uint64
	for _, e := range entries {
		used[e.Card.ID] = true
		n, parseErr := strconv.ParseUint(strings.TrimPrefix(e.Card.ID, "TASK-"), 10, 64)
		if parseErr != nil {
			return Entry{}, fmt.Errorf("task ID overflow: %s", e.Card.ID)
		}
		if n > max {
			max = n
		}
	}
	id := req.ID
	if id == "" {
		if max == ^uint64(0) {
			return Entry{}, errors.New("task ID allocation overflow")
		}
		id = fmt.Sprintf("TASK-%d", max+1)
	} else if !canonicalID.MatchString(id) {
		return Entry{}, fmt.Errorf("invalid task ID %q", id)
	} else if _, parseErr := strconv.ParseUint(strings.TrimPrefix(id, "TASK-"), 10, 64); parseErr != nil {
		return Entry{}, fmt.Errorf("task ID overflow: %s", id)
	}
	if used[id] {
		return Entry{}, fmt.Errorf("task ID already exists: %s", id)
	}
	if strings.TrimSpace(req.Title) == "" {
		return Entry{}, errors.New("task title is empty")
	}

	raw, err := render(id, req.Title)
	if err != nil {
		return Entry{}, err
	}
	doc, parseErr := card.Parse(raw)
	if parseErr != nil {
		return Entry{}, fmt.Errorf("parse created task: %w", parseErr)
	}
	name, err := stage(r, raw)
	if err != nil {
		return Entry{}, err
	}
	defer func() { err = errors.Join(err, r.Remove(name)) }()
	dest := filepath.ToSlash(filepath.Join("todo", id+".md"))
	if err = r.Link(name, dest); err != nil {
		return Entry{}, fmt.Errorf("publish %s: %w", dest, err)
	}
	view := doc.Snapshot(dest)
	return Entry{Path: dest, Card: view}, nil
}

func openBoard(dir string) (*os.Root, error) {
	if dir == "" {
		return nil, errors.New("task board directory is empty")
	}
	dir = filepath.Clean(dir)
	info, err := os.Lstat(dir)
	if err != nil {
		return nil, fmt.Errorf("inspect task board root: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("task board root is a symlink: %s", dir)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("task board root is not a directory: %s", dir)
	}
	r, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("open task board root: %w", err)
	}
	return r, nil
}

func lock(r *os.Root) (func() error, error) {
	if err := r.Mkdir(".task-manager.lock", 0o700); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return nil, fmt.Errorf("task board is locked (remove only after confirming the owner is gone): %w", err)
		}
		return nil, fmt.Errorf("acquire task board lock: %w", err)
	}
	return func() error { return r.Remove(".task-manager.lock") }, nil
}

func listLocked(r *os.Root) ([]Entry, error) {
	if err := validateRootLayout(r); err != nil {
		return nil, err
	}
	out := make([]Entry, 0)
	ids := map[string]string{}
	for _, dir := range knownDirs {
		if err := scanDir(r, dir, &out, ids); err != nil {
			return nil, err
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

func validateRootLayout(r *os.Root) error {
	entries, err := fs.ReadDir(r.FS(), ".")
	if err != nil {
		return fmt.Errorf("read task board root: %w", err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink task entry rejected: %s", name)
		}
		if strings.HasPrefix(name, ".") || strings.EqualFold(name, "README.md") {
			continue
		}
		if entry.IsDir() {
			if !containsKnownDir(name) {
				return fmt.Errorf("unsupported task directory at board root: %s", name)
			}
			continue
		}
		if filepath.Ext(name) == ".md" {
			return fmt.Errorf("task card must be inside a known board directory: %s", name)
		}
	}
	return nil
}

func containsKnownDir(name string) bool {
	for _, known := range knownDirs {
		if name == known {
			return true
		}
	}
	return false
}

func scanDir(r *os.Root, dir string, out *[]Entry, ids map[string]string) error {
	info, err := r.Lstat(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect %s: %w", dir, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("known task directory is not a real directory: %s", dir)
	}
	return fs.WalkDir(r.FS(), dir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return fmt.Errorf("scan %s: %w", path, walkErr)
		}
		if path != dir && d.IsDir() && strings.HasPrefix(d.Name(), ".") {
			return fs.SkipDir
		}
		if path == dir || d.IsDir() {
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink task entry rejected: %s", path)
		}
		if strings.HasPrefix(d.Name(), ".") || strings.EqualFold(d.Name(), "README.md") {
			return nil
		}
		if filepath.Ext(d.Name()) != ".md" {
			return nil
		}
		fi, err := d.Info()
		if err != nil {
			return fmt.Errorf("inspect task card %s: %w", path, err)
		}
		if !fi.Mode().IsRegular() {
			return fmt.Errorf("task card is not regular: %s", path)
		}
		raw, err := fs.ReadFile(r.FS(), path)
		if err != nil {
			return fmt.Errorf("read task card %s: %w", path, err)
		}
		doc, err := card.Parse(raw)
		if err != nil {
			return fmt.Errorf("parse task card %s: %w", path, err)
		}
		view := doc.Snapshot(path)
		if !canonicalID.MatchString(view.ID) {
			return fmt.Errorf("invalid task ID %q in %s", view.ID, path)
		}
		if old, ok := ids[view.ID]; ok {
			return fmt.Errorf("duplicate task ID %s in %s and %s", view.ID, old, path)
		}
		ids[view.ID] = path
		*out = append(*out, Entry{Path: filepath.ToSlash(path), Card: view})
		return nil
	})
}

func render(id, title string) ([]byte, error) {
	fm, err := yaml.Marshal(map[string]string{"id": id, "title": title, "status": "pending"})
	if err != nil {
		return nil, fmt.Errorf("render task frontmatter: %w", err)
	}
	raw := append([]byte("---\n"), fm...)
	raw = append(raw, []byte("---\n\n# "+title+"\n")...)
	return raw, nil
}

func stage(r *os.Root, raw []byte) (string, error) {
	return stageWith(r, raw, func(f *os.File, data []byte) error {
		if _, err := f.Write(data); err != nil {
			return err
		}
		return f.Sync()
	})
}

func stageWith(r *os.Root, raw []byte, write func(*os.File, []byte) error) (string, error) {
	var suffix [16]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return "", fmt.Errorf("generate staging name: %w", err)
	}
	name := fmt.Sprintf(".task-manager-stage-%x", suffix)
	f, err := r.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", fmt.Errorf("create staging file: %w", err)
	}
	err = write(f, raw)
	closeErr := f.Close()
	if err != nil {
		return "", errors.Join(fmt.Errorf("write staging file: %w", err), closeErr, r.Remove(name))
	}
	if closeErr != nil {
		return "", errors.Join(fmt.Errorf("close staging file: %w", closeErr), r.Remove(name))
	}
	return name, nil
}
