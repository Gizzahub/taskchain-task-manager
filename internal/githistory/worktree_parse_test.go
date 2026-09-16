package githistory

import "testing"

func TestParseWorktreesPorcelain(t *testing.T) {
	sha := "0123456789abcdef0123456789abcdef01234567"
	sha256 := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	raw := "worktree /z path\x00HEAD " + sha256 + "\x00detached\x00\x00" +
		"worktree /a\x00HEAD " + sha + "\x00branch refs/heads/main\x00locked reason with\nnewline\x00prunable gone\x00\x00"
	got, err := parseWorktrees([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Path != "/a" || !got[0].Locked || got[0].LockReason != "reason with\nnewline" || !got[0].Prunable {
		t.Fatalf("got=%#v", got)
	}
	if !got[1].Detached || got[1].HEAD != sha256 {
		t.Fatalf("got=%#v", got[1])
	}
}

func TestParseWorktreesBareAndUnborn(t *testing.T) {
	zero := "0000000000000000000000000000000000000000"
	got, err := parseWorktrees([]byte("worktree /repo.git\x00bare\x00\x00worktree /repo\x00HEAD " + zero + "\x00branch refs/heads/new\x00\x00"))
	if err != nil || len(got) != 2 {
		t.Fatalf("got=%#v err=%v", got, err)
	}
	for _, wt := range got {
		if wt.Path == "/repo" && wt.HEAD != zero {
			t.Fatalf("unborn=%#v", wt)
		}
		if wt.Path == "/repo.git" && !wt.Bare {
			t.Fatalf("bare=%#v", wt)
		}
	}
}

func TestParseWorktreesRejectsMalformed(t *testing.T) {
	sha := "0123456789abcdef0123456789abcdef01234567"
	for name, raw := range map[string]string{
		"empty": "", "truncated": "worktree /repo\x00HEAD " + sha + "\x00branch refs/heads/main\x00",
		"unknown":         "worktree /repo\x00HEAD " + sha + "\x00wat x\x00\x00",
		"duplicate":       "worktree /repo\x00HEAD " + sha + "\x00HEAD " + sha + "\x00branch refs/heads/main\x00\x00",
		"duplicate path":  "worktree /repo\x00HEAD " + sha + "\x00branch refs/heads/main\x00\x00worktree /repo\x00HEAD " + sha + "\x00detached\x00\x00",
		"incomplete":      "worktree /repo\x00HEAD " + sha + "\x00\x00",
		"bare head":       "worktree /repo\x00bare\x00HEAD " + sha + "\x00\x00",
		"missing HEAD":    "worktree /repo\x00branch refs/heads/main\x00\x00",
		"detached branch": "worktree /repo\x00HEAD " + sha + "\x00detached\x00branch refs/heads/main\x00\x00",
		"relative path":   "worktree repo\x00HEAD " + sha + "\x00detached\x00\x00",
		"unclean path":    "worktree /repo/../other\x00HEAD " + sha + "\x00detached\x00\x00",
		"empty separator": "\x00\x00",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parseWorktrees([]byte(raw)); err == nil {
				t.Fatal("malformed worktree accepted")
			}
		})
	}
	if _, err := parseWorktrees([]byte{'w', 'o', 'r', 'k', 't', 'r', 'e', 'e', ' ', '/', 'x', 0xff, 0, 0}); err == nil {
		t.Fatal("invalid UTF-8 accepted")
	}
}

func TestParseWorktreesMaximumRecords(t *testing.T) {
	sha := "0123456789abcdef0123456789abcdef01234567"
	raw := ""
	for i := 0; i < 256; i++ {
		raw += "worktree /repo/" + string(rune('a'+i%26)) + "/" + string(rune('0'+i/26)) + "\x00HEAD " + sha + "\x00detached\x00\x00"
	}
	if got, err := parseWorktrees([]byte(raw)); err != nil || len(got) != 256 {
		t.Fatalf("256 records got=%d err=%v", len(got), err)
	}
	raw += "worktree /overflow\x00HEAD " + sha + "\x00detached\x00\x00"
	if _, err := parseWorktrees([]byte(raw)); err == nil {
		t.Fatal("257 records accepted")
	}
}
