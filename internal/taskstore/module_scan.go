package taskstore

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/Gizzahub/taskchain-task-manager/internal/boardpolicy"
	"github.com/Gizzahub/taskchain-task-manager/internal/card"
	"github.com/Gizzahub/taskchain-task-manager/internal/cardpath"
)

func scanModulesWithPolicy(r *os.Root, out *[]Entry, ids map[string]string, skip string, policy boardpolicy.Policy) error {
	for _, module := range policy.Modules() {
		info, err := r.Lstat(module)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("inspect module %s: %w", module, err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return fmt.Errorf("module is not a real directory: %s", module)
		}
		entries, err := fs.ReadDir(r.FS(), module)
		if err != nil {
			return fmt.Errorf("read module %s: %w", module, err)
		}
		for _, entry := range entries {
			name := entry.Name()
			path := filepath.Join(module, name)
			if entry.Type()&os.ModeSymlink != 0 {
				return fmt.Errorf("symlink task entry rejected: %s", path)
			}
			if entry.IsDir() {
				if strings.HasPrefix(name, ".") || cardpath.IsExcludedDirectory(name) {
					continue
				}
				if !moduleZoneAllowed(name, policy) {
					return fmt.Errorf("unsupported module zone directory: %s", path)
				}
				if err := scanModuleZone(r, path, out, ids, skip, policy); err != nil {
					return err
				}
				continue
			}
			if strings.HasPrefix(name, ".") || cardpath.IsDocumentation(name) {
				continue
			}
			{
				info, err := entry.Info()
				if err != nil {
					return fmt.Errorf("inspect module entry %s: %w", path, err)
				}
				if !info.Mode().IsRegular() {
					return fmt.Errorf("module entry is not regular: %s", path)
				}
				if filepath.Ext(name) == ".md" {
					return fmt.Errorf("module card must be inside a zone directory: %s", path)
				}
				continue
			}
		}
	}
	return nil
}

func moduleZoneAllowed(zone string, policy boardpolicy.Policy) bool {
	return policy.Workflow(zone) || policy.Parked(zone) || policy.IsKind(zone) || zone == "archive" || zone == "_archive"
}

func scanModuleZone(r *os.Root, root string, out *[]Entry, ids map[string]string, skip string, policy boardpolicy.Policy) error {
	return fs.WalkDir(r.FS(), root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return fmt.Errorf("scan %s: %w", path, walkErr)
		}
		if path == skip {
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink task entry rejected: %s", path)
		}
		if path != root && d.IsDir() && (strings.HasPrefix(d.Name(), ".") || cardpath.IsExcludedDirectory(d.Name())) {
			return fs.SkipDir
		}
		if path == root || d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return fmt.Errorf("inspect task card %s: %w", path, err)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("task card is not regular: %s", path)
		}
		if strings.HasPrefix(d.Name(), ".") || cardpath.IsDocumentation(d.Name()) || filepath.Ext(d.Name()) != ".md" {
			return nil
		}
		name := filepath.ToSlash(path)
		modulePath, err := classifyModulePath(name, policy)
		if err != nil {
			return fmt.Errorf("classify module card %s: %w", path, err)
		}
		raw, err := fs.ReadFile(r.FS(), path)
		if err != nil {
			return fmt.Errorf("read task card %s: %w", path, err)
		}
		doc, err := card.Parse(raw)
		if err != nil {
			return fmt.Errorf("parse task card %s: %w", path, err)
		}
		view := doc.View()
		if policy.Workflow(modulePath.Zone) || policy.Parked(modulePath.Zone) {
			if status, ok := policy.Status(modulePath.Zone); ok {
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
		ids[key] = name
		*out = append(*out, Entry{Path: name, Card: view})
		return nil
	})
}
