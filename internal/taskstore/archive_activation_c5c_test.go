package taskstore

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func protocol5SharedFixture(t *testing.T) (string, string, string) {
	t.Helper()
	repo, first, second := sharedFixture(t)
	for i, dir := range []string{first, second} {
		source := filepath.Join(dir, "todo", "TASK-1.md")
		raw := mustReadFile(t, source)
		raw = bytes.Replace(raw, []byte("---\n"), []byte("---\nreview-result: pass\nreview-proof: c5c fixture\n"), 1)
		if err := os.WriteFile(source, raw, 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(dir, "done"), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(source, filepath.Join(dir, "done", "TASK-1.md")); err != nil {
			t.Fatal(err)
		}
		req := ArchiveRequest{ID: "TASK-1", Owner: "worker", RequestID: strings.Repeat(string(rune('a'+i)), 32), Source: "done/TASK-1.md", ExpectedSHA256: bytesDigest(raw), Operation: "archive", Rules: []byte(archiveCompletionRulesFixture)}
		if _, err := Archive(dir, req, true); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(filepath.Join(dir, archivesFile), 0640); err != nil {
			t.Fatal(err)
		}
		if err := AdoptArchiveCapacity(dir, strings.Repeat(string(rune('c'+i)), 32)); err != nil {
			t.Fatal(err)
		}
	}
	return repo, first, second
}

func c5cSharedState(t *testing.T, board string) sharedState {
	t.Helper()
	s, release, err := acquireShared(board, true)
	if err != nil {
		t.Fatal(err)
	}
	if s == nil || s.state == nil {
		t.Fatal("missing shared state")
	}
	state := *s.state
	if err := release(); err != nil {
		t.Fatal(err)
	}
	return state
}

func c5cCommonBytes(t *testing.T, board string) []byte {
	t.Helper()
	s, release, err := acquireShared(board, true)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := s.root.ReadFile(sharedStateFile)
	if closeErr := release(); err == nil {
		err = closeErr
	}
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestProtocol5EnableSharedResumesOriginalAndMixedTargetCutpoints(t *testing.T) {
	t.Parallel()
	for _, point := range []string{"after-initializing", "after-local-0"} {
		t.Run(point, func(t *testing.T) {
			_, first, second := protocol5SharedFixture(t)
			stop := errors.New("c5c stop")
			if _, err := enableSharedStep(first, false, func(at string) error {
				if at == point {
					return stop
				}
				return nil
			}); !errors.Is(err, stop) {
				t.Fatalf("cutpoint=%s err=%v", point, err)
			}
			state := c5cSharedState(t, first)
			for _, p := range state.Participants {
				if p.ArchiveActivationBinding == nil {
					t.Fatal("protocol5 participant lacks binding")
				}
			}
			if _, err := EnableShared(first, true); err != nil {
				t.Fatal(err)
			}
			for _, dir := range []string{first, second} {
				r, err := openBoard(dir)
				if err != nil {
					t.Fatal(err)
				}
				tr, err := loadTransitions(r)
				if err != nil {
					r.Close()
					t.Fatal(err)
				}
				j, err := archiveForBoard(r, tr)
				if err != nil {
					r.Close()
					t.Fatal(err)
				}
				info, statErr := r.Lstat(archivesFile)
				r.Close()
				if j.Namespace != state.NamespaceID || statErr != nil || info.Mode().Perm() != 0640 {
					t.Fatalf("archive scope/mode=%q %v %v", j.Namespace, info, statErr)
				}
			}
		})
	}
}

func TestProtocol5EnableSharedBindsCurrentModeAfterCapacityHistory(t *testing.T) {
	t.Parallel()
	_, first, second := protocol5SharedFixture(t)
	for _, dir := range []string{first, second} {
		if err := os.Chmod(filepath.Join(dir, archivesFile), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := EnableShared(first, false); err != nil {
		t.Fatal(err)
	}
	state := c5cSharedState(t, first)
	for _, p := range state.Participants {
		if p.ArchiveActivationBinding == nil || p.ArchiveActivationBinding.JournalMode != 0600 {
			t.Fatalf("current mode not bound: %+v", p.ArchiveActivationBinding)
		}
	}
}

func TestProtocol5EnableSharedPendingTamperIsNoMutation(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"receipt", "missing-receipt", "payload", "missing-payload", "artifact", "missing-artifact", "mode", "hash", "third-state"} {
		t.Run(kind, func(t *testing.T) {
			_, first, _ := protocol5SharedFixture(t)
			stop := errors.New("c5c stop")
			if _, err := enableSharedStep(first, false, func(at string) error {
				if at == "after-initializing" {
					return stop
				}
				return nil
			}); !errors.Is(err, stop) {
				t.Fatal(err)
			}
			state := c5cSharedState(t, first)
			p := state.Participants[0]
			dir := filepath.Join(p.Root, filepath.FromSlash(state.BoardPath))
			r, err := openBoard(dir)
			if err != nil {
				t.Fatal(err)
			}
			adoption, err := loadArchiveCapacityAdoption(r)
			if err != nil {
				r.Close()
				t.Fatal(err)
			}
			switch kind {
			case "receipt":
				raw := mustReadFile(t, filepath.Join(dir, archiveCapacityFile))
				if err := os.WriteFile(filepath.Join(dir, archiveCapacityFile), append(raw, ' '), 0600); err != nil {
					t.Fatal(err)
				}
			case "missing-receipt":
				if err := os.Remove(filepath.Join(dir, archiveCapacityFile)); err != nil {
					t.Fatal(err)
				}
			case "payload":
				name, _ := archiveCapacityPayloadName(adoption.PayloadSHA256)
				raw := mustReadFile(t, filepath.Join(dir, name))
				raw[len(raw)-1] ^= 1
				if err := os.WriteFile(filepath.Join(dir, name), raw, 0600); err != nil {
					t.Fatal(err)
				}
			case "missing-payload":
				name, _ := archiveCapacityPayloadName(adoption.PayloadSHA256)
				if err := os.Remove(filepath.Join(dir, name)); err != nil {
					t.Fatal(err)
				}
			case "artifact":
				name, _ := archiveRebindArtifactName(p.ArchiveActivationBinding.RebindArtifactSHA256)
				raw := mustReadFile(t, filepath.Join(dir, name))
				raw[len(raw)-1] ^= 1
				if err := os.WriteFile(filepath.Join(dir, name), raw, 0600); err != nil {
					t.Fatal(err)
				}
			case "missing-artifact":
				name, _ := archiveRebindArtifactName(p.ArchiveActivationBinding.RebindArtifactSHA256)
				if err := os.Remove(filepath.Join(dir, name)); err != nil {
					t.Fatal(err)
				}
			case "mode":
				if err := os.Chmod(filepath.Join(dir, archivesFile), 0600); err != nil {
					t.Fatal(err)
				}
			case "hash":
				s, release, err := acquireShared(first, true)
				if err != nil {
					t.Fatal(err)
				}
				next := *s.state
				next.Participants = append([]sharedParticipant(nil), s.state.Participants...)
				binding := *next.Participants[0].ArchiveActivationBinding
				binding.TargetJournalSHA256 = strings.Repeat("7", 64)
				next.Participants[0].ArchiveActivationBinding = &binding
				if err := publishSharedState(s.root, next, false); err != nil {
					t.Fatal(err)
				}
				if err := release(); err != nil {
					t.Fatal(err)
				}
			case "third-state":
				raw := mustReadFile(t, filepath.Join(dir, archivesFile))
				if err := os.WriteFile(filepath.Join(dir, archivesFile), append(raw, ' '), 0640); err != nil {
					t.Fatal(err)
				}
			}
			r.Close()
			before := boardBytes(t, dir)
			commonBefore := c5cCommonBytes(t, first)
			if _, err := EnableShared(first, true); err == nil {
				t.Fatal("tampered pending activation resumed")
			}
			if !reflectEqualBoard(before, boardBytes(t, dir)) {
				t.Fatal("refusal changed board")
			}
			if !bytes.Equal(commonBefore, c5cCommonBytes(t, first)) {
				t.Fatal("refusal changed common state")
			}
		})
	}
}

func TestProtocol5EnableSharedRejectsPendingCapacityAndForeignNamespace(t *testing.T) {
	t.Parallel()
	t.Run("pending capacity", func(t *testing.T) {
		_, first, _ := protocol5SharedFixture(t)
		r, err := openBoard(first)
		if err != nil {
			t.Fatal(err)
		}
		adoption, err := loadArchiveCapacityAdoption(r)
		if err != nil {
			t.Fatal(err)
		}
		adoption.Phase = "pending"
		if err := saveArchiveCapacityAdoption(r, adoption, false); err != nil {
			t.Fatal(err)
		}
		r.Close()
		before := boardBytes(t, first)
		if _, err := EnableShared(first, false); err == nil {
			t.Fatal("pending capacity admitted")
		}
		if !reflectEqualBoard(before, boardBytes(t, first)) {
			t.Fatal("pending capacity refusal changed board")
		}
	})
	t.Run("foreign namespace", func(t *testing.T) {
		_, first, _ := protocol5SharedFixture(t)
		r, err := openBoard(first)
		if err != nil {
			t.Fatal(err)
		}
		j, err := loadArchiveJournalForProtocol(r, 5)
		if err != nil {
			t.Fatal(err)
		}
		j.Namespace = strings.Repeat("f", 32)
		for i := range j.Records {
			j.Records[i].Namespace = j.Namespace
		}
		raw, err := archiveCapacityJournalBytes(j)
		if err != nil {
			t.Fatal(err)
		}
		if err := publishArchiveCapacityTarget(r, raw, 0640, bytesDigest(raw)); err != nil {
			t.Fatal(err)
		}
		r.Close()
		before := boardBytes(t, first)
		if _, err := EnableShared(first, false); err == nil {
			t.Fatal("foreign archive namespace admitted")
		}
		if !reflectEqualBoard(before, boardBytes(t, first)) {
			t.Fatal("foreign refusal changed board")
		}
	})
}

func TestProtocol5SharedPolicyInitialJoinRevisionAndCompletedReplay(t *testing.T) {
	t.Parallel()
	repo, first, second := protocol5SharedFixture(t)
	if _, err := EnableShared(first, false); err != nil {
		t.Fatal(err)
	}
	secondRoot := filepath.Dir(second)
	sharedGit(t, secondRoot, "add", "tasks")
	sharedGit(t, secondRoot, "commit", "-m", "protocol5 returning worktree")
	commit := sharedGit(t, secondRoot, "rev-parse", "HEAD")
	sharedGit(t, repo, "worktree", "remove", "--force", secondRoot)
	policyRaw := defaultPolicyBytes(t)
	active, err := ActivatePolicy(first, policyRaw, PolicyActivationOptions{AllWorktrees: true})
	if err != nil || active.Boards != 1 {
		t.Fatalf("initial=%+v err=%v", active, err)
	}
	sharedGit(t, repo, "worktree", "add", "--detach", secondRoot, commit)
	for _, pattern := range []string{".task-manager-archive-capacity-*.bin", ".task-manager-archive-rebind-*.bin"} {
		matches, err := filepath.Glob(filepath.Join(second, pattern))
		if err != nil {
			t.Fatal(err)
		}
		for _, match := range matches {
			if err := os.Chmod(match, 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := os.Chmod(filepath.Join(second, archivesFile), 0640); err != nil {
		t.Fatal(err)
	}
	r, err := openBoard(second)
	if err != nil {
		t.Fatal(err)
	}
	adoption, err := loadArchiveCapacityAdoption(r)
	if err != nil {
		t.Fatal(err)
	}
	_, localTarget, mode, err := loadArchiveCapacityPayload(r, adoption.PayloadSHA256)
	if err != nil {
		t.Fatal(err)
	}
	if err := publishArchiveCapacityTarget(r, localTarget, mode, bytesDigest(localTarget)); err != nil {
		t.Fatal(err)
	}
	r.Close()
	stop := errors.New("policy join target cutpoint")
	if _, err := activatePolicyWithStep(second, policyRaw, PolicyActivationOptions{AllWorktrees: true}, func(at string) error {
		if at == "board-0/after-policy-ids" {
			return stop
		}
		return nil
	}); !errors.Is(err, stop) {
		t.Fatalf("join cutpoint err=%v", err)
	}
	joined, err := ActivatePolicy(second, policyRaw, PolicyActivationOptions{AllWorktrees: true, Resume: true})
	if err != nil || joined.Boards != 1 || joined.AuthorityID != active.AuthorityID {
		t.Fatalf("join=%+v err=%v", joined, err)
	}
	revisionRaw := revisionPolicyBytes(t)
	options := PolicyRevisionOptions{ExpectedAuthorityID: active.AuthorityID, ExpectedDigest: active.Digest, AllWorktrees: true}
	revised, err := RevisePolicy(first, revisionRaw, options)
	if err != nil || revised.Boards != 2 {
		t.Fatalf("revision=%+v err=%v", revised, err)
	}
	created, err := Create(second, CreateRequest{ID: "TASK-2", Title: "later archive"})
	if err != nil {
		t.Fatal(err)
	}
	raw := mustReadFile(t, filepath.Join(second, created.Path))
	raw = bytes.Replace(raw, []byte("---\n"), []byte("---\nreview-result: pass\nreview-proof: later archive\n"), 1)
	if err := os.WriteFile(filepath.Join(second, created.Path), raw, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(second, "done"), 0755); err != nil {
		t.Fatal(err)
	}
	done := filepath.Join(second, "done", "TASK-2.md")
	if err := os.Rename(filepath.Join(second, created.Path), done); err != nil {
		t.Fatal(err)
	}
	req := ArchiveRequest{ID: "TASK-2", Owner: "worker", RequestID: strings.Repeat("9", 32), Source: "done/TASK-2.md", ExpectedSHA256: bytesDigest(raw), Operation: "archive", Rules: []byte(archiveCompletionRulesFixture)}
	if _, err := Archive(second, req, false); err != nil {
		t.Fatal(err)
	}
	options.Resume = true
	replay, err := RevisePolicy(second, revisionRaw, options)
	if err != nil || !replay.Replayed {
		t.Fatalf("completed replay=%+v err=%v", replay, err)
	}
}
