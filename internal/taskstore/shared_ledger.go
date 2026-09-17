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

	"github.com/Gizzahub/taskchain-task-manager/internal/boardpolicy"
	"github.com/Gizzahub/taskchain-task-manager/internal/cardid"
)

const sharedStateFile = "state.json"
const maxSharedStateBytes = 16 << 20

var sharedHex32 = regexp.MustCompile(`^[0-9a-f]{32}$`)
var sharedHex40Or64 = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)
var sharedHex64 = regexp.MustCompile(`^[0-9a-f]{64}$`)

type sharedState struct {
	SchemaVersion          int                     `json:"schemaVersion"`
	NamespaceID            string                  `json:"namespaceId"`
	BoardPath              string                  `json:"boardPath"`
	Phase                  string                  `json:"phase"`
	Reserved               []string                `json:"reserved"`
	Participants           []sharedParticipant     `json:"participants"`
	BundleProtocol         int                     `json:"bundleProtocol,omitempty"`
	PendingBundle          *sharedBundlePending    `json:"pendingBundle,omitempty"`
	Policy                 *sharedPolicyAuthority  `json:"policy,omitempty"`
	StorageProtocol        int                     `json:"storageProtocol,omitempty"`
	PendingRepair          *sharedRepairPending    `json:"pendingRepair,omitempty"`
	PendingRelocation      *sharedRepairPending    `json:"pendingRelocation,omitempty"`
	PendingArchive         *sharedRepairPending    `json:"pendingArchive,omitempty"`
	PendingArchiveDelta    *archivePendingDelta    `json:"pendingArchiveDelta,omitempty"`
	PendingArchiveCapacity *archiveCapacityPending `json:"pendingArchiveCapacity,omitempty"`
	PolicyRevisionProtocol int                     `json:"policyRevisionProtocol,omitempty"`
	ModuleProtocol         int                     `json:"moduleProtocol,omitempty"`
}

type sharedBundlePending struct {
	RequestID string   `json:"requestId"`
	Digest    string   `json:"digest"`
	BoardID   string   `json:"boardId"`
	Owner     string   `json:"owner"`
	IDs       []string `json:"ids"`
}

type sharedParticipant struct {
	Root                     string                    `json:"root"`
	HEAD                     string                    `json:"head"`
	Snapshot                 string                    `json:"snapshot"`
	OriginalLedger           string                    `json:"originalLedger"`
	TargetLedger             string                    `json:"targetLedger"`
	ArchiveActivationBinding *archiveActivationBinding `json:"archiveActivationBinding,omitempty"`
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
	if raw, ok := root["moduleProtocol"]; ok {
		var protocol int
		if err := json.Unmarshal(raw, &protocol); err != nil || protocol != 1 {
			return errors.New("unsupported shared module protocol")
		}
		allowed["moduleProtocol"] = true
	}
	if raw, ok := root["policyRevisionProtocol"]; ok {
		var protocol int
		if err := json.Unmarshal(raw, &protocol); err != nil || protocol != 1 {
			return errors.New("unsupported shared policy revision protocol")
		}
		allowed["policyRevisionProtocol"] = true
	}
	if raw, ok := root["storageProtocol"]; ok {
		var protocol int
		if err := json.Unmarshal(raw, &protocol); err != nil || protocol < 1 || protocol > 5 {
			return errors.New("unsupported shared storage protocol")
		}
		allowed["storageProtocol"] = true
	}
	if raw, ok := root["pendingRepair"]; ok {
		if err := validateSharedRepairShape(raw); err != nil {
			return err
		}
		allowed["pendingRepair"] = true
	}
	if raw, ok := root["pendingRelocation"]; ok {
		if err := validateSharedRepairShape(raw); err != nil {
			return err
		}
		allowed["pendingRelocation"] = true
	}
	if raw, ok := root["pendingArchive"]; ok {
		if err := validateSharedArchiveShape(raw); err != nil {
			return err
		}
		allowed["pendingArchive"] = true
	}
	if raw, ok := root["pendingArchiveDelta"]; ok {
		if _, err := decodeArchivePendingDelta(raw); err != nil {
			return err
		}
		allowed["pendingArchiveDelta"] = true
	}
	if raw, ok := root["pendingArchiveCapacity"]; ok {
		if err := validateArchiveCapacityPendingShape(raw); err != nil {
			return err
		}
		allowed["pendingArchiveCapacity"] = true
	}
	var version int
	if err := json.Unmarshal(root["schemaVersion"], &version); err != nil {
		return err
	}
	if version == 2 || version == 3 {
		if version == 2 {
			allowed["bundleProtocol"] = true
		} else if _, present := root["bundleProtocol"]; present {
			allowed["bundleProtocol"] = true
		}
		if protocolRaw, present := root["bundleProtocol"]; present {
			if string(protocolRaw) == "null" {
				return errors.New("bundle protocol cannot be null")
			}
			var protocol int
			if err := json.Unmarshal(protocolRaw, &protocol); err != nil {
				return errors.New("bundle protocol must be an integer")
			}
			if version == 3 && protocol != 1 {
				return errors.New("schema 3 bundle protocol must be 1 when present")
			}
		}
		if pending, ok := root["pendingBundle"]; ok {
			allowed["pendingBundle"] = true
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(pending, &fields); err != nil || len(fields) != 5 {
				return errors.New("invalid shared bundle shape")
			}
			for _, key := range []string{"requestId", "digest", "boardId", "owner", "ids"} {
				if fields[key] == nil || string(fields[key]) == "null" {
					return errors.New("shared bundle requires all fields")
				}
			}
		}
	}
	if version == 3 {
		allowed["policy"] = true
		policy, ok := root["policy"]
		if !ok || string(policy) == "null" {
			return errors.New("schema 3 shared state requires policy")
		}
		if err := validateSharedPolicyShape(policy); err != nil {
			return err
		}
	}
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
	pallowed := map[string]bool{"root": true, "head": true, "snapshot": true, "originalLedger": true, "targetLedger": true, "archiveActivationBinding": true}
	for _, part := range parts {
		if len(part) != len(pallowed) && len(part) != len(pallowed)-1 {
			return errors.New("participant requires all fields")
		}
		for key := range part {
			if !pallowed[key] {
				return fmt.Errorf("unknown participant field %q", key)
			}
		}
		if raw, ok := part["archiveActivationBinding"]; ok && string(raw) == "null" {
			return errors.New("archive activation binding cannot be null")
		}
	}
	return nil
}

func validateSharedState(s sharedState) error {
	if s.ModuleProtocol != 0 && (s.ModuleProtocol != 1 || s.SchemaVersion != 3 || s.Policy == nil) {
		return errors.New("module protocol requires shared policy authority")
	}
	if s.Policy != nil {
		policy, err := boardpolicy.Parse(s.Policy.Canonical)
		if err != nil {
			return err
		}
		if len(policy.Modules()) > 0 && s.ModuleProtocol != 1 {
			return errors.New("shared module scope requires permanent module protocol")
		}
		for _, plan := range s.Policy.Pending {
			if plan.ModuleAdoption == nil {
				continue
			}
			ledger, err := decodeIDs(plan.IDTarget)
			if err != nil || ledger.Namespace != s.NamespaceID || !sameStringSlice(ledger.Reserved, s.Reserved) {
				return errors.New("module adoption target must preserve exact frozen common reservations")
			}
		}
	}
	if s.PolicyRevisionProtocol != 0 && (s.PolicyRevisionProtocol != 1 || s.SchemaVersion != 3 || s.Policy == nil) {
		return errors.New("policy revision protocol requires shared policy authority")
	}
	if s.Policy != nil && s.Policy.Revision != nil && s.PolicyRevisionProtocol != 1 {
		return errors.New("shared policy revision requires permanent protocol barrier")
	}
	if err := validateSharedStorage(s); err != nil {
		return err
	}
	if err := validateSharedArchiveCapacity(s); err != nil {
		return err
	}
	if err := validateSharedArchive(s); err != nil {
		return err
	}
	if (s.SchemaVersion != 1 && s.SchemaVersion != 2 && s.SchemaVersion != 3) || !sharedHex32.MatchString(s.NamespaceID) || !validSharedBoardPath(s.BoardPath) || (s.Phase != "initializing" && s.Phase != "active") {
		return errors.New("invalid shared state header")
	}
	if s.SchemaVersion == 1 && (s.BundleProtocol != 0 || s.PendingBundle != nil || s.Policy != nil) {
		return errors.New("legacy shared state cannot contain bundle protocol")
	}
	if s.SchemaVersion == 2 && (s.BundleProtocol != 1 || s.Phase != "active" || s.Policy != nil) {
		return errors.New("invalid shared bundle protocol")
	}
	if s.SchemaVersion == 3 {
		if s.Phase != "active" || (s.BundleProtocol != 0 && s.BundleProtocol != 1) || s.Policy == nil {
			return errors.New("invalid schema 3 shared state")
		}
		if err := validateSharedPolicyAuthority(*s.Policy); err != nil {
			return fmt.Errorf("invalid shared policy authority: %w", err)
		}
		reserved := map[string]bool{}
		for _, id := range s.Reserved {
			reserved[id] = true
		}
		for _, plan := range s.Policy.Pending {
			if len(plan.IDTarget) == 0 {
				continue
			}
			ledger, err := decodeIDs(plan.IDTarget)
			if err != nil || ledger.Namespace != s.NamespaceID {
				return errors.New("shared policy ID target namespace mismatch")
			}
			for _, id := range ledger.Reserved {
				if !reserved[id] {
					return errors.New("shared policy target includes unreserved IDs")
				}
			}
		}
		if s.PendingBundle != nil && len(s.Policy.Pending) != 0 {
			return errors.New("shared policy and bundle reservations cannot both be pending")
		}
		if s.PendingBundle != nil && s.BundleProtocol != 1 {
			return errors.New("shared pending bundle requires bundle protocol 1")
		}
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
	if p := s.PendingBundle; p != nil {
		if !sharedHex32.MatchString(p.RequestID) || !sharedHex64.MatchString(p.Digest) || !sharedHex32.MatchString(p.BoardID) || !validSharedRoot(p.Owner) || len(p.IDs) == 0 || len(p.IDs) > 128 {
			return errors.New("invalid shared pending bundle")
		}
		reserved := map[string]bool{}
		for _, id := range s.Reserved {
			reserved[id] = true
		}
		for i, id := range p.IDs {
			parsed, err := cardid.Parse(id)
			if err != nil || parsed.Prefix != "TASK" || parsed.Key() != id || !reserved[id] || (i > 0 && p.IDs[i-1] >= id) {
				return errors.New("shared bundle IDs must be sorted unique reserved TASK keys")
			}
		}
	}
	last := ""
	for _, p := range s.Participants {
		if !validSharedRoot(p.Root) || p.Root <= last || !sharedHex40Or64.MatchString(p.HEAD) || !sharedHex64.MatchString(p.Snapshot) || !sharedHex64.MatchString(p.OriginalLedger) || !sharedHex64.MatchString(p.TargetLedger) || validateArchiveActivationBinding(p.ArchiveActivationBinding) != nil {
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
