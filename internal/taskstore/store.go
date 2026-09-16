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
	"strings"

	"github.com/Gizzahub/taskchain-task-manager/internal/boardpolicy"
	"github.com/Gizzahub/taskchain-task-manager/internal/card"
	"github.com/Gizzahub/taskchain-task-manager/internal/cardpath"
	"gopkg.in/yaml.v3"
)

type Entry struct {
	Path string    `json:"path"`
	Card card.View `json:"card"`
}

type CreateRequest struct {
	Kind      string          `json:"kind,omitempty"`
	ID        string          `json:"id"`
	Title     string          `json:"title"`
	DependsOn []string        `json:"dependsOn,omitempty"`
	Template  *CreateTemplate `json:"-"`
}

var canonicalID = regexp.MustCompile(`^TASK-[1-9][0-9]*$`)

func Init(dir string) (err error) {
	if dir == "" {
		return errors.New("task board directory is empty")
	}
	shared, release, err := acquireShared(dir, false)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, release()) }()
	dir = filepath.Clean(dir)
	created := false
	if info, err := os.Lstat(dir); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("task board root is a symlink: %s", dir)
		}
		if !info.IsDir() {
			return fmt.Errorf("task board root is not a directory: %s", dir)
		}
	} else if errors.Is(err, fs.ErrNotExist) {
		if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
			return fmt.Errorf("create task board parent: %w", err)
		}
		if err := os.Mkdir(dir, 0o755); err != nil {
			return fmt.Errorf("create task board root: %w", err)
		}
		created = true
	} else {
		return fmt.Errorf("inspect task board root: %w", err)
	}
	r, err := os.OpenRoot(dir)
	if err != nil {
		return fmt.Errorf("open task board root: %w", err)
	}
	defer r.Close()
	unlock, err := lock(r)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, unlock()) }()
	if err := rejectPendingTransitions(r); err != nil {
		return err
	}
	ledger, loadErr := loadIDs(r)
	initial := errors.Is(loadErr, fs.ErrNotExist)
	if loadErr != nil {
		if !created || !initial {
			return fmt.Errorf("load ID ledger (existing boards require reserve-ids --adopt): %w", loadErr)
		}
		ledger = idLedger{SchemaVersion: 2, Reserved: []string{}}
	}
	ledger, err = shared.merge(ledger)
	if err != nil {
		return err
	}
	if err := shared.verifyBoard(r); err != nil {
		return err
	}
	if err := shared.publish(ledger); err != nil {
		return err
	}
	if initial || ledger.SchemaVersion == 3 {
		if err := publishIDs(r, ledger, initial); err != nil {
			return err
		}
	}
	policy, err := policyForBoard(r)
	if err != nil {
		return err
	}
	initialZone := policy.InitialZone()
	info, err := r.Lstat(initialZone)
	if err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return errors.New("task board todo is not a real directory")
		}
		return nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("inspect task board todo: %w", err)
	}
	if err := r.Mkdir(initialZone, 0o755); err != nil {
		return fmt.Errorf("create task board todo: %w", err)
	}
	return nil
}

func List(dir string) (entries []Entry, err error) {
	session, err := openBoardSession(dir)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, session.close()) }()
	r := session.root
	if err := rejectPendingTransitions(r); err != nil {
		return nil, err
	}
	entries, err = listLocked(r)
	if err != nil {
		return nil, err
	}
	if _, err := loadClaims(r, entries); err != nil {
		return nil, err
	}
	return entries, nil
}

func Create(dir string, req CreateRequest) (entry Entry, err error) {
	return createWithStep(dir, req, nil)
}

func createWithStep(dir string, req CreateRequest, step func(string) error) (entry Entry, err error) {
	if req.Template != nil {
		if err := validateConfiguredRequest(req); err != nil {
			return Entry{}, err
		}
		if _, _, err := validateConfiguredCard("TASK-1", req); err != nil {
			return Entry{}, err
		}
	}
	reservedID := ""
	defer func() {
		if err != nil && reservedID != "" {
			err = fmt.Errorf("ID %s is reserved; inspect card publication before retry: %w", reservedID, err)
		}
	}()
	shared, release, err := acquireShared(dir, false)
	if err != nil {
		return Entry{}, err
	}
	defer func() { err = errors.Join(err, release()) }()
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
	if err := rejectPendingTransitions(r); err != nil {
		return Entry{}, err
	}
	entries, err := listLocked(r)
	if err != nil {
		return Entry{}, err
	}
	if _, err := loadClaims(r, entries); err != nil {
		return Entry{}, err
	}
	if err := validateGraph(entries); err != nil {
		return Entry{}, err
	}
	ledger, err := loadIDs(r)
	if err != nil {
		return Entry{}, fmt.Errorf("load ID ledger (existing boards require reserve-ids --adopt): %w", err)
	}
	ledger, err = observedIDs(r, entries, ledger, nil)
	if err != nil {
		return Entry{}, err
	}
	ledger, err = shared.merge(ledger)
	if err != nil {
		return Entry{}, err
	}
	prefix, err := createPrefix(req)
	if err != nil {
		return Entry{}, err
	}
	id, err := allocateID(ledger, req.ID, prefix)
	if err != nil {
		return Entry{}, err
	}
	if strings.TrimSpace(req.Title) == "" {
		return Entry{}, errors.New("task title is empty")
	}
	if err := validateDependencies(req.DependsOn, id, entries); err != nil {
		return Entry{}, err
	}

	prepared, err := prepareCreatedCard(id, req)
	if err != nil {
		return Entry{}, err
	}
	name, err := stage(r, prepared.Raw)
	if err != nil {
		return Entry{}, err
	}
	defer func() { err = errors.Join(err, r.Remove(name)) }()
	ledger.Reserved = append(ledger.Reserved, identityKey(id))
	sort.Strings(ledger.Reserved)
	if err := shared.verifyBoard(r); err != nil {
		return Entry{}, err
	}
	if err := shared.publish(ledger); err != nil {
		return Entry{}, fmt.Errorf("shared ID %s publication may have applied; inspect before retry: %w", id, err)
	}
	if shared != nil && shared.state != nil {
		reservedID = id
		if step != nil {
			if err := step("after-shared-reservation"); err != nil {
				return Entry{}, err
			}
		}
	}
	if err := publishIDs(r, ledger, false); err != nil {
		return Entry{}, err
	}
	reservedID = id
	if step != nil {
		if err := step("after-reservation"); err != nil {
			return Entry{}, err
		}
	}
	zone := filepath.Dir(prepared.Entry.Path)
	if err := ensureTransitionDir(r, zone); err != nil {
		return Entry{}, err
	}
	dest := prepared.Entry.Path
	if err := shared.verifyBoard(r); err != nil {
		return Entry{}, err
	}
	if err = r.Link(name, dest); err != nil {
		return Entry{}, fmt.Errorf("publish %s: %w", dest, err)
	}
	return prepared.Entry, nil
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
	return listLockedExcept(r, "")
}

func listLockedExcept(r *os.Root, skip string) ([]Entry, error) {
	policy, err := policyForBoard(r)
	if err != nil {
		return nil, err
	}
	if err := validateOptionalIDs(r); err != nil {
		return nil, err
	}
	if err := validateRootLayoutWithPolicy(r, policy); err != nil {
		return nil, err
	}
	out := make([]Entry, 0)
	ids := map[string]string{}
	for _, dir := range policy.KnownDirs() {
		if err := scanDirExceptWithPolicy(r, dir, &out, ids, skip, policy); err != nil {
			return nil, err
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

// Ready returns pending top-level todo cards whose canonical prerequisites are done.
func Ready(dir string) (entries []Entry, err error) {
	session, err := openBoardSession(dir)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, session.close()) }()
	r := session.root
	if err := rejectPendingTransitions(r); err != nil {
		return nil, err
	}
	all, err := listLocked(r)
	if err != nil {
		return nil, err
	}
	ledger, err := loadClaims(r, all)
	if err != nil {
		return nil, err
	}
	policy, err := policyForBoard(r)
	if err != nil {
		return nil, err
	}
	return readyLockedWithPolicy(all, ledger, policy)
}

func validateDependencies(deps []string, id string, entries []Entry) error {
	proposed := append([]Entry(nil), entries...)
	policy := currentPolicy()
	status, _ := policy.Status(policy.InitialZone())
	proposed = append(proposed, Entry{Path: policy.InitialZone() + "/" + id + ".md", Card: card.View{ID: id, Status: status, DependsOn: deps}})
	return validateGraph(proposed)
}

func validateGraph(entries []Entry) error {
	byID := make(map[string]Entry, len(entries))
	for _, entry := range entries {
		key := identityKey(entry.Card.ID)
		if key == "" {
			return fmt.Errorf("invalid card ID %q", entry.Card.ID)
		}
		if _, exists := byID[key]; exists {
			return fmt.Errorf("duplicate task identity %s", key)
		}
		byID[key] = entry
	}
	for _, entry := range entries {
		seen := map[string]bool{}
		for _, dep := range entry.Card.DependsOn {
			key := identityKey(dep)
			if key == "" {
				return fmt.Errorf("invalid dependency ID %q in %s", dep, entry.Path)
			}
			if seen[key] {
				return fmt.Errorf("duplicate dependency %s in %s", dep, entry.Path)
			}
			seen[key] = true
			if sameIdentity(dep, entry.Card.ID) {
				return fmt.Errorf("self dependency %s in %s", dep, entry.Path)
			}
			if _, ok := byID[key]; !ok {
				return fmt.Errorf("missing dependency %s referenced by %s", dep, entry.Path)
			}
		}
	}
	ids := make([]string, 0, len(byID))
	for id := range byID {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	state := map[string]uint8{}
	var visit func(string) error
	visit = func(id string) error {
		if state[id] == 1 {
			return fmt.Errorf("dependency cycle involving %s", id)
		}
		if state[id] == 2 {
			return nil
		}
		state[id] = 1
		for _, dep := range byID[id].Card.DependsOn {
			if err := visit(identityKey(dep)); err != nil {
				return err
			}
		}
		state[id] = 2
		return nil
	}
	for _, id := range ids {
		if err := visit(id); err != nil {
			return err
		}
	}
	return nil
}

func validateRootLayout(r *os.Root) error {
	return validateRootLayoutWithPolicy(r, currentPolicy())
}

func validateRootLayoutWithPolicy(r *os.Root, policy boardpolicy.Policy) error {
	entries, err := fs.ReadDir(r.FS(), ".")
	if err != nil {
		return fmt.Errorf("read task board root: %w", err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink task entry rejected: %s", name)
		}
		if strings.HasPrefix(name, ".") || (!entry.IsDir() && cardpath.IsDocumentation(name)) || (entry.IsDir() && cardpath.IsExcludedDirectory(name)) {
			continue
		}
		if entry.IsDir() {
			if !containsPolicyDir(name, policy) {
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
	return containsPolicyDir(name, currentPolicy())
}

func containsPolicyDir(name string, policy boardpolicy.Policy) bool {
	for _, known := range policy.KnownDirs() {
		if name == known {
			return true
		}
	}
	return false
}

func scanDir(r *os.Root, dir string, out *[]Entry, ids map[string]string) error {
	return scanDirExcept(r, dir, out, ids, "")
}

func scanDirExcept(r *os.Root, dir string, out *[]Entry, ids map[string]string, skip string) error {
	return scanDirExceptWithPolicy(r, dir, out, ids, skip, currentPolicy())
}

func scanDirExceptWithPolicy(r *os.Root, dir string, out *[]Entry, ids map[string]string, skip string, policy boardpolicy.Policy) error {
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
		if path != dir && d.IsDir() && (strings.HasPrefix(d.Name(), ".") || cardpath.IsExcludedDirectory(d.Name())) {
			return fs.SkipDir
		}
		if path == skip {
			return nil
		}
		if path == dir || d.IsDir() {
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink task entry rejected: %s", path)
		}
		if strings.HasPrefix(d.Name(), ".") || cardpath.IsDocumentation(d.Name()) {
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
		if policy.Parked(dir) {
			// A nested workflow-looking path must not override parking semantics.
			view = doc.View()
			if status, ok := policy.Status(dir); ok {
				view.Status = status
			}
		}
		key := identityKey(view.ID)
		if key == "" {
			return fmt.Errorf("invalid task ID %q in %s", view.ID, path)
		}
		if old, ok := ids[key]; ok {
			return fmt.Errorf("duplicate task ID %s in %s and %s", view.ID, old, path)
		}
		ids[key] = path
		*out = append(*out, Entry{Path: filepath.ToSlash(path), Card: view})
		return nil
	})
}

func render(id, title string) ([]byte, error) {
	return renderWithDependencies(id, title, nil)
}

func renderWithDependencies(id, title string, deps []string) ([]byte, error) {
	policy := currentPolicy()
	status, _ := policy.Status(policy.InitialZone())
	metadata := map[string]any{"id": id, "title": title, "status": status}
	if len(deps) > 0 {
		metadata["depends-on"] = deps
	}
	fm, err := yaml.Marshal(metadata)
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
