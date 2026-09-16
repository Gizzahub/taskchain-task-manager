package taskstore

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Gizzahub/taskchain-task-manager/internal/boardpolicy"
)

func activationFixture(t *testing.T) policyActivationState {
	t.Helper()
	canonical, err := boardpolicy.Default().Canonical()
	if err != nil {
		t.Fatal(err)
	}
	return policyActivationState{SchemaVersion: 1, Phase: "pending", AuthorityID: strings.Repeat("a", 32), Scope: "local", Canonical: canonical, Digest: bytesDigest(canonical), Plan: policyActivationPlan{
		Root: filepath.Join(t.TempDir(), "tasks"), Snapshot: strings.Repeat("b", 64), TargetJournal: strings.Repeat("c", 64), OriginalIDs: strings.Repeat("d", 64), TargetIDs: strings.Repeat("d", 64), IDTarget: []byte{},
	}}
}

func authorityFixture(t *testing.T) sharedPolicyAuthority {
	t.Helper()
	canonical, err := boardpolicy.Default().Canonical()
	if err != nil {
		t.Fatal(err)
	}
	return sharedPolicyAuthority{AuthorityID: strings.Repeat("d", 32), Phase: "initializing", Canonical: canonical, Digest: bytesDigest(canonical), Pending: []policyActivationPlan{{Root: filepath.Join(t.TempDir(), "tasks"), HEAD: strings.Repeat("e", 40), Snapshot: strings.Repeat("f", 64), TargetJournal: strings.Repeat("0", 64), OriginalIDs: strings.Repeat("1", 64), TargetIDs: strings.Repeat("1", 64), IDTarget: []byte{}}}}
}

func TestPolicyActivationStateRoundTripAndNoOverwrite(t *testing.T) {
	dir := t.TempDir()
	r, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	state := activationFixture(t)
	if err := savePolicyActivation(r, state, true); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadPolicyActivation(r)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(loaded.Canonical, state.Canonical) || loaded.Digest != state.Digest {
		t.Fatalf("loaded=%+v", loaded)
	}
	before, err := r.ReadFile(policyActivationFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := savePolicyActivation(r, state, true); err == nil {
		t.Fatal("initial save overwrote existing file")
	}
	after, _ := r.ReadFile(policyActivationFile)
	if !bytes.Equal(before, after) {
		t.Fatal("failed initial save changed bytes")
	}
	state.Phase = "completed"
	if err := savePolicyActivation(r, state, false); err != nil {
		t.Fatal(err)
	}
}

func TestPolicyActivationStateValidationBoundaries(t *testing.T) {
	base := activationFixture(t)
	mutations := map[string]func(*policyActivationState){
		"authority": func(s *policyActivationState) { s.AuthorityID = "bad" },
		"phase":     func(s *policyActivationState) { s.Phase = "joining" },
		"scope":     func(s *policyActivationState) { s.Scope = "shared" },
		"namespace": func(s *policyActivationState) { s.Namespace = "bad" },
		"canonical": func(s *policyActivationState) {
			s.Canonical = append([]byte(nil), []byte(`{"schema-version":1}`)...)
			s.Digest = bytesDigest(s.Canonical)
		},
		"digest":   func(s *policyActivationState) { s.Digest = strings.Repeat("0", 64) },
		"root":     func(s *policyActivationState) { s.Plan.Root = "relative" },
		"snapshot": func(s *policyActivationState) { s.Plan.Snapshot = "bad" },
		"target":   func(s *policyActivationState) { s.Plan.TargetJournal = "bad" },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			state := base
			mutate(&state)
			if err := validatePolicyActivationState(state); err == nil {
				t.Fatal("invalid state accepted")
			}
		})
	}
}

func TestPolicyActivationIDTargetValidation(t *testing.T) {
	base := activationFixture(t)
	if err := validatePolicyActivationState(base); err != nil {
		t.Fatalf("baseline invalid: %v", err)
	}
	for name, mutate := range map[string]func(*policyActivationState){
		"empty unequal": func(s *policyActivationState) { s.Plan.TargetIDs = strings.Repeat("e", 64) },
		"nil target":    func(s *policyActivationState) { s.Plan.IDTarget = nil },
		"local bytes": func(s *policyActivationState) {
			s.Plan.IDTarget = []byte(`{"schemaVersion":3,"reserved":[],"namespace":"` + strings.Repeat("a", 32) + `"}`)
		},
		"target digest": func(s *policyActivationState) {
			s.Scope = "shared"
			s.Namespace = strings.Repeat("a", 32)
			s.Plan.HEAD = strings.Repeat("f", 40)
			s.Plan.IDTarget = []byte(`{"schemaVersion":3,"reserved":[],"namespace":"` + s.Namespace + `"}`)
			s.Plan.TargetIDs = strings.Repeat("0", 64)
		},
		"target namespace": func(s *policyActivationState) {
			s.Scope = "shared"
			s.Namespace = strings.Repeat("a", 32)
			s.Plan.HEAD = strings.Repeat("f", 40)
			s.Plan.IDTarget = []byte(`{"schemaVersion":3,"reserved":[],"namespace":"` + strings.Repeat("b", 32) + `"}`)
			s.Plan.TargetIDs = bytesDigest(s.Plan.IDTarget)
		},
	} {
		t.Run(name, func(t *testing.T) {
			state := base
			mutate(&state)
			if err := validatePolicyActivationState(state); err == nil {
				t.Fatal("invalid ID target accepted")
			}
		})
	}
}

func TestSharedPolicyAuthorityValidation(t *testing.T) {
	base := authorityFixture(t)
	if err := validateSharedPolicyAuthority(base); err != nil {
		t.Fatal(err)
	}
	active := base
	active.Phase = "active"
	active.Pending = []policyActivationPlan{}
	if err := validateSharedPolicyAuthority(active); err != nil {
		t.Fatal(err)
	}
	unsorted := base
	unsorted.Pending = append([]policyActivationPlan(nil), base.Pending...)
	unsorted.Pending[0].Root = "/b/tasks"
	second := unsorted.Pending[0]
	second.Root = "/a/tasks"
	unsorted.Pending = append(unsorted.Pending, second)
	if err := validateSharedPolicyAuthority(unsorted); err == nil {
		t.Fatal("unsorted shared authority accepted")
	}
	for name, mutate := range map[string]func(*sharedPolicyAuthority){
		"null pending":   func(a *sharedPolicyAuthority) { a.Pending = nil },
		"active pending": func(a *sharedPolicyAuthority) { a.Phase = "active" },
		"joining count": func(a *sharedPolicyAuthority) {
			a.Phase = "joining"
			second := a.Pending[0]
			second.Root = "/b/tasks"
			a.Pending[0].Root = "/a/tasks"
			a.Pending = append(a.Pending, second)
		},
		"duplicate root": func(a *sharedPolicyAuthority) { a.Pending = append(a.Pending, a.Pending[0]) },
	} {
		t.Run(name, func(t *testing.T) {
			a := base
			a.Pending = append([]policyActivationPlan(nil), base.Pending...)
			mutate(&a)
			if err := validateSharedPolicyAuthority(a); err == nil {
				t.Fatal("invalid authority accepted")
			}
		})
	}
}

func TestPolicyActivationShapesRejectStrictJSON(t *testing.T) {
	state := activationFixture(t)
	raw, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	mutateField := func(key, value string) []byte {
		var object map[string]json.RawMessage
		if err := json.Unmarshal(raw, &object); err != nil {
			t.Fatal(err)
		}
		object[key] = json.RawMessage(value)
		mutated, err := json.Marshal(object)
		if err != nil {
			t.Fatal(err)
		}
		return mutated
	}
	mutatedNull := mutateField("canonical", "null")
	mutatedArray := mutateField("canonical", "[]")
	for name, mutated := range map[string][]byte{
		"duplicate":      bytes.Replace(raw, []byte(`"phase":"pending"`), []byte(`"phase":"pending","phase":"completed"`), 1),
		"unknown":        bytes.Replace(raw, []byte(`"phase":"pending"`), []byte(`"phase":"pending","extra":1`), 1),
		"null canonical": mutatedNull,
		"array bytes":    mutatedArray,
	} {
		t.Run(name, func(t *testing.T) {
			if err := validatePolicyActivationShape(mutated); err == nil {
				t.Fatal("invalid shape accepted")
			}
		})
	}
	authority := authorityFixture(t)
	authority.Phase, authority.Pending = "active", []policyActivationPlan{}
	authRaw, err := json.Marshal(authority)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateSharedPolicyShape(authRaw); err != nil {
		t.Fatal(err)
	}
}

func TestLoadPolicyActivationRejectsStrictBoundaries(t *testing.T) {
	dir := t.TempDir()
	r, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	state := activationFixture(t)
	if err := savePolicyActivation(r, state, true); err != nil {
		t.Fatal(err)
	}
	valid, err := r.ReadFile(policyActivationFile)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := loadPolicyActivation(r); err != nil {
		t.Fatalf("valid baseline rejected: %v", err)
	}
	mutateField := func(key, value string) []byte {
		var object map[string]json.RawMessage
		if err := json.Unmarshal(valid, &object); err != nil {
			t.Fatal(err)
		}
		object[key] = json.RawMessage(value)
		mutated, err := json.Marshal(object)
		if err != nil {
			t.Fatal(err)
		}
		return mutated
	}
	mutations := map[string][]byte{
		"trailing":         append(append([]byte(nil), valid...), []byte("{}")...),
		"duplicate":        bytes.Replace(valid, []byte(`"phase":"pending"`), []byte(`"phase":"pending","phase":"completed"`), 1),
		"case":             bytes.Replace(valid, []byte(`"phase"`), []byte(`"Phase"`), 1),
		"null":             mutateField("canonical", "null"),
		"surrogate":        bytes.Replace(valid, []byte(`"root":"`+state.Plan.Root+`"`), []byte(`"root":"/tasks/\ud800"`), 1),
		"nested duplicate": bytes.Replace(valid, []byte(`"root":"`), []byte(`"root":"/a","root":"`), 1),
	}
	paired := bytes.Replace(valid, []byte(`"root":"`+state.Plan.Root+`"`), []byte(`"root":"/tasks/\ud83d\ude00"`), 1)
	if err := r.WriteFile(policyActivationFile, paired, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadPolicyActivation(r); err != nil {
		t.Fatalf("paired surrogate rejected: %v", err)
	}
	if err := rejectJSONSurrogates([]byte(`{"root":"\\uD800"}`)); err != nil {
		t.Fatalf("literal backslash-u rejected: %v", err)
	}
	if err := r.WriteFile(policyActivationFile, append(bytes.Repeat([]byte{'x'}, maxPolicyActivationBytes+1), '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadPolicyActivation(r); err == nil {
		t.Fatal("oversized activation accepted")
	}
	invalidUTF8 := append([]byte(nil), valid...)
	invalidUTF8[len(invalidUTF8)-1] = 0xff
	if err := r.WriteFile(policyActivationFile, invalidUTF8, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadPolicyActivation(r); err == nil {
		t.Fatal("invalid UTF-8 activation accepted")
	}
	for name, raw := range mutations {
		t.Run(name, func(t *testing.T) {
			if err := r.WriteFile(policyActivationFile, raw, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := loadPolicyActivation(r); err == nil {
				t.Fatal("invalid activation accepted")
			}
		})
	}
	if err := r.Remove(policyActivationFile); err != nil {
		t.Fatal(err)
	}
	if err := r.Symlink("missing", policyActivationFile); err != nil {
		t.Fatal(err)
	}
	if _, err := loadPolicyActivation(r); err == nil {
		t.Fatal("symlink activation accepted")
	}
	if err := r.Remove(policyActivationFile); err != nil {
		t.Fatal(err)
	}
	if err := r.Mkdir(policyActivationFile, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := loadPolicyActivation(r); err == nil {
		t.Fatal("directory activation accepted")
	}
}
