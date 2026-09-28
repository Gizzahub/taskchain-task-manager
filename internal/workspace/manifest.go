// Package workspace loads a closed set of task-manager repositories.
package workspace

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

const maxManifestBytes = 256 << 10

var repositoryName = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

// Repository is one explicitly configured repository and its board. Path and
// Board are absolute, symlink-free paths after successful parsing.
type Repository struct {
	Name  string
	Path  string
	Board string
}

// Manifest is a validated workspace manifest. Repositories are sorted by Name.
type Manifest struct {
	Repositories []Repository
}

// Load reads and validates the manifest at path.
func Load(path string) (Manifest, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return Manifest{}, fmt.Errorf("inspect workspace manifest: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() > maxManifestBytes {
		return Manifest{}, errors.New("workspace manifest must be a regular file of at most 256 KiB")
	}
	file, err := os.Open(path)
	if err != nil {
		return Manifest{}, fmt.Errorf("read workspace manifest: %w", err)
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, maxManifestBytes+1))
	if err != nil {
		return Manifest{}, fmt.Errorf("read workspace manifest: %w", err)
	}
	return Parse(raw, filepath.Dir(path))
}

// Parse validates raw manifest JSON relative to manifestDir. It does not
// discover repositories or execute commands.
func Parse(raw []byte, manifestDir string) (Manifest, error) {
	if len(raw) == 0 || len(raw) > maxManifestBytes || !utf8.Valid(raw) {
		return Manifest{}, errors.New("workspace manifest must be nonempty UTF-8 and at most 256 KiB")
	}
	dir, err := filepath.Abs(manifestDir)
	if err != nil {
		return Manifest{}, fmt.Errorf("resolve manifest directory: %w", err)
	}
	dir, err = filepath.EvalSymlinks(dir)
	if err != nil {
		return Manifest{}, fmt.Errorf("resolve manifest directory: %w", err)
	}
	if err := requireDirectory(dir); err != nil {
		return Manifest{}, fmt.Errorf("manifest directory: %w", err)
	}

	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	value, err := decodeValue(dec, 0)
	if err != nil {
		return Manifest{}, fmt.Errorf("parse workspace manifest: %w", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return Manifest{}, errors.New("workspace manifest must contain exactly one JSON value")
	}
	root, ok := value.(map[string]any)
	if !ok {
		return Manifest{}, errors.New("workspace manifest must be an object")
	}
	if err := exactKeys(root, "workspace manifest", "schemaVersion", "repositories"); err != nil {
		return Manifest{}, err
	}
	version, ok := root["schemaVersion"].(json.Number)
	if !ok || string(version) != "1" {
		return Manifest{}, errors.New("workspace manifest schemaVersion must be 1")
	}
	rows, ok := root["repositories"].([]any)
	if !ok || len(rows) == 0 {
		return Manifest{}, errors.New("workspace manifest repositories must be a nonempty array")
	}

	seenNames := map[string]bool{}
	seenRepositories := map[string]bool{}
	seenBoards := map[string]bool{}
	result := Manifest{Repositories: make([]Repository, 0, len(rows))}
	for i, value := range rows {
		row, ok := value.(map[string]any)
		if !ok {
			return Manifest{}, fmt.Errorf("repositories[%d] must be an object", i)
		}
		if err := exactKeys(row, fmt.Sprintf("repositories[%d]", i), "name", "path", "board"); err != nil {
			return Manifest{}, err
		}
		name, nameOK := row["name"].(string)
		repoPath, pathOK := row["path"].(string)
		boardPath, boardOK := row["board"].(string)
		if !nameOK || !repositoryName.MatchString(name) {
			return Manifest{}, fmt.Errorf("repositories[%d].name must be lowercase kebab-case", i)
		}
		if !pathOK || !validRelativePath(repoPath) {
			return Manifest{}, fmt.Errorf("repositories[%d].path must be a clean relative path", i)
		}
		if !boardOK || !validRelativePath(boardPath) {
			return Manifest{}, fmt.Errorf("repositories[%d].board must be a clean relative path", i)
		}
		key := strings.ToLower(name)
		if seenNames[key] {
			return Manifest{}, fmt.Errorf("duplicate repository name %q", name)
		}
		seenNames[key] = true

		repository, err := resolvePath(dir, repoPath)
		if err != nil {
			return Manifest{}, fmt.Errorf("repositories[%d].path: %w", i, err)
		}
		if err := requireDirectory(repository); err != nil {
			return Manifest{}, fmt.Errorf("repositories[%d].path: %w", i, err)
		}
		board, err := resolvePath(repository, boardPath)
		if err != nil {
			return Manifest{}, fmt.Errorf("repositories[%d].board: %w", i, err)
		}
		if err := requireDirectory(board); err != nil {
			return Manifest{}, fmt.Errorf("repositories[%d].board: %w", i, err)
		}
		if seenRepositories[repository] {
			return Manifest{}, fmt.Errorf("duplicate physical repository %q", repository)
		}
		if seenBoards[board] {
			return Manifest{}, fmt.Errorf("duplicate physical board %q", board)
		}
		seenRepositories[repository] = true
		seenBoards[board] = true
		result.Repositories = append(result.Repositories, Repository{Name: name, Path: repository, Board: board})
	}
	for i := range result.Repositories {
		for j := 0; j < i; j++ {
			if overlaps(result.Repositories[i].Board, result.Repositories[j].Board) {
				return Manifest{}, fmt.Errorf("overlapping boards %q and %q", result.Repositories[j].Board, result.Repositories[i].Board)
			}
		}
	}
	sort.Slice(result.Repositories, func(i, j int) bool { return result.Repositories[i].Name < result.Repositories[j].Name })
	return result, nil
}

func decodeValue(dec *json.Decoder, depth int) (any, error) {
	if depth > 8 {
		return nil, errors.New("JSON nesting exceeds 8")
	}
	token, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if token == nil {
		return nil, errors.New("null is not allowed")
	}
	if delim, ok := token.(json.Delim); ok {
		switch delim {
		case '{':
			object := map[string]any{}
			for dec.More() {
				keyToken, err := dec.Token()
				if err != nil {
					return nil, err
				}
				key, ok := keyToken.(string)
				if !ok {
					return nil, errors.New("object key must be a string")
				}
				if _, exists := object[key]; exists {
					return nil, fmt.Errorf("duplicate JSON field %q", key)
				}
				item, err := decodeValue(dec, depth+1)
				if err != nil {
					return nil, err
				}
				object[key] = item
			}
			if end, err := dec.Token(); err != nil || end != json.Delim('}') {
				return nil, errors.New("unterminated JSON object")
			}
			return object, nil
		case '[':
			array := []any{}
			for dec.More() {
				if len(array) == 128 {
					return nil, errors.New("repositories exceeds 128 entries")
				}
				item, err := decodeValue(dec, depth+1)
				if err != nil {
					return nil, err
				}
				array = append(array, item)
			}
			if end, err := dec.Token(); err != nil || end != json.Delim(']') {
				return nil, errors.New("unterminated JSON array")
			}
			return array, nil
		}
		return nil, errors.New("unexpected JSON delimiter")
	}
	return token, nil
}

func exactKeys(object map[string]any, where string, keys ...string) error {
	allowed := make(map[string]bool, len(keys))
	for _, key := range keys {
		allowed[key] = true
		if _, present := object[key]; !present {
			return fmt.Errorf("%s requires %s", where, key)
		}
	}
	for key := range object {
		if !allowed[key] {
			return fmt.Errorf("unknown field %s.%s", where, key)
		}
	}
	return nil
}

func validRelativePath(path string) bool {
	if path == "" || filepath.IsAbs(path) || filepath.Clean(path) != path || strings.HasPrefix(path, ".."+string(filepath.Separator)) || path == ".." {
		return false
	}
	for _, character := range path {
		if character == '\\' || unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func resolvePath(base, relative string) (string, error) {
	path := filepath.Join(base, relative)
	if err := rejectSymlinkComponents(base, relative); err != nil {
		return "", err
	}
	return path, nil
}

func rejectSymlinkComponents(base, relative string) error {
	current := base
	for _, part := range strings.Split(relative, string(filepath.Separator)) {
		if part == "." {
			continue
		}
		children, err := os.ReadDir(current)
		if err != nil {
			return err
		}
		exact := false
		for _, child := range children {
			if child.Name() == part {
				exact = true
				break
			}
		}
		if !exact {
			return fmt.Errorf("path component %q does not match an exact directory entry", part)
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink path component %q is not allowed", current)
		}
	}
	return nil
}

func requireDirectory(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return errors.New("must be a directory")
	}
	return nil
}

func overlaps(first, second string) bool {
	return containsPath(first, second) || containsPath(second, first)
}

func containsPath(parent, child string) bool {
	relative, err := filepath.Rel(parent, child)
	return err == nil && (relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))))
}
