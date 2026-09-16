package taskstore

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/Gizzahub/taskchain-task-manager/internal/cardid"
)

const sharedStateFile = "state.json"
const maxSharedStateBytes = 16 << 20

var sharedHex32 = regexp.MustCompile(`^[0-9a-f]{32}$`)
var sharedHex40Or64 = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)
var sharedHex64 = regexp.MustCompile(`^[0-9a-f]{64}$`)

type sharedState struct {
	SchemaVersion int                 `json:"schemaVersion"`
	NamespaceID   string              `json:"namespaceId"`
	BoardPath     string              `json:"boardPath"`
	Phase         string              `json:"phase"`
	Reserved      []string            `json:"reserved"`
	Participants  []sharedParticipant `json:"participants"`
}

type sharedParticipant struct {
	Root           string `json:"root"`
	HEAD           string `json:"head"`
	Snapshot       string `json:"snapshot"`
	OriginalLedger string `json:"originalLedger"`
	TargetLedger   string `json:"targetLedger"`
}

func loadSharedState(r *os.Root) (sharedState, error) {
	info, err := r.Lstat(sharedStateFile)
	if err != nil {
		return sharedState{}, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return sharedState{}, errors.New("shared state is not a regular file")
	}
	if info.Size() > maxSharedStateBytes {
		return sharedState{}, errors.New("shared state exceeds 16 MiB")
	}
	f, err := r.Open(sharedStateFile)
	if err != nil {
		return sharedState{}, err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, maxSharedStateBytes+1))
	if err != nil {
		return sharedState{}, err
	}
	if len(raw) > maxSharedStateBytes || !utf8.Valid(raw) {
		return sharedState{}, errors.New("invalid shared state size or UTF-8")
	}
	if err := rejectDuplicateJSON(raw); err != nil {
		return sharedState{}, err
	}
	if err := validateSharedShape(raw); err != nil {
		return sharedState{}, err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var state sharedState
	if err := dec.Decode(&state); err != nil {
		return sharedState{}, err
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return sharedState{}, errors.New("shared state has trailing content")
	}
	if err := validateSharedState(state); err != nil {
		return sharedState{}, err
	}
	return state, nil
}

func publishSharedState(r *os.Root, state sharedState, initial bool) error {
	if err := validateSharedState(state); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	if len(raw) > maxSharedStateBytes {
		return errors.New("shared state exceeds 16 MiB")
	}
	name, err := stage(r, raw)
	if err != nil {
		return err
	}
	if initial {
		if err := r.Link(name, sharedStateFile); err != nil {
			return errors.Join(err, r.Remove(name))
		}
		if err := r.Remove(name); err != nil {
			return err
		}
	} else if err := r.Rename(name, sharedStateFile); err != nil {
		return errors.Join(err, r.Remove(name))
	}
	dir, err := r.Open(".")
	if err != nil {
		return err
	}
	return errors.Join(dir.Sync(), dir.Close())
}

func validateSharedShape(raw []byte) error {
	var root map[string]json.RawMessage
	if err := json.Unmarshal(raw, &root); err != nil {
		return err
	}
	allowed := map[string]bool{"schemaVersion": true, "namespaceId": true, "boardPath": true, "phase": true, "reserved": true, "participants": true}
	if len(root) != len(allowed) {
		return errors.New("shared state requires all fields exactly once")
	}
	for key := range root {
		if !allowed[key] {
			return fmt.Errorf("unknown shared state field %q", key)
		}
	}
	var parts []map[string]json.RawMessage
	if err := json.Unmarshal(root["participants"], &parts); err != nil || parts == nil {
		return errors.New("participants must be a non-null array")
	}
	pallowed := map[string]bool{"root": true, "head": true, "snapshot": true, "originalLedger": true, "targetLedger": true}
	for _, part := range parts {
		if len(part) != len(pallowed) {
			return errors.New("participant requires all fields")
		}
		for key := range part {
			if !pallowed[key] {
				return fmt.Errorf("unknown participant field %q", key)
			}
		}
	}
	return nil
}

func validateSharedState(s sharedState) error {
	if s.SchemaVersion != 1 || !sharedHex32.MatchString(s.NamespaceID) || !validSharedBoardPath(s.BoardPath) || (s.Phase != "initializing" && s.Phase != "active") {
		return errors.New("invalid shared state header")
	}
	if s.Reserved == nil || s.Participants == nil || len(s.Participants) == 0 || len(s.Participants) > 256 {
		return errors.New("invalid shared state collections")
	}
	for i, id := range s.Reserved {
		parsed, err := cardid.Parse(id)
		if err != nil || parsed.Key() != id || (i > 0 && s.Reserved[i-1] >= id) {
			return errors.New("reserved IDs must be sorted unique normalized keys")
		}
	}
	last := ""
	for _, p := range s.Participants {
		if !validSharedRoot(p.Root) || p.Root <= last || !sharedHex40Or64.MatchString(p.HEAD) || !sharedHex64.MatchString(p.Snapshot) || !sharedHex64.MatchString(p.OriginalLedger) || !sharedHex64.MatchString(p.TargetLedger) {
			return errors.New("invalid shared participant")
		}
		last = p.Root
	}
	return nil
}

func validSharedBoardPath(p string) bool {
	if p == "" || !utf8.ValidString(p) || strings.HasPrefix(p, "../") || strings.ContainsAny(p, "\\\x00") || path.IsAbs(p) || path.Clean(p) != p || p == "." || p == ".." {
		return false
	}
	for _, r := range p {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func validSharedRoot(p string) bool {
	return path.IsAbs(p) && path.Clean(p) == p && !strings.ContainsAny(p, "\\\x00") && utf8.ValidString(p)
}
