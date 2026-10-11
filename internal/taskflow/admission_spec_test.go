package taskflow

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAdmitQuotedSeparatorsAreExamined(t *testing.T) {
	dir := admitRepo(t)
	for _, command := range []string{`test -f "a;b"`, `test -f 'a|b'`} {
		report := admitOne(dir, false, "tree", command)
		if report.UnexecutedCompound != 0 || report.Unsupported != 0 || report.Unavailable != 0 || report.Examined != 1 || report.AlreadyPassing != 0 {
			t.Fatalf("%s %#v", command, report)
		}
	}
}

func TestAdmitUnquotedMetacharactersAreCompound(t *testing.T) {
	commands := []string{
		"test -f a; test -f b",
		"test -f a && test -f b",
		"test -f a | test -f b",
		"test -f a < b",
		"test -f a > b",
		"test -f $HOME",
		"test -f `pwd`",
		"test -f (x)",
		"ls a\nb",
		"ls *.md",
		"ls ?.md",
		`test -f a\b`,
	}
	for _, command := range commands {
		report := admitOne(t.TempDir(), false, "tree", command)
		if report.UnexecutedCompound != 1 || report.Unsupported != 0 || report.Examined != 0 || report.Eligible != 1 {
			t.Fatalf("%q %#v", command, report)
		}
		if len(report.Warnings) != 1 || report.Warnings[0].Field != "criterion_admission" || !strings.Contains(report.Warnings[0].Message, "shell syntax") {
			t.Fatalf("%q warnings %#v", command, report.Warnings)
		}
	}
}

func TestAdmitProbeOutcomes(t *testing.T) {
	dir := admitRepo(t)
	cases := []struct {
		name        string
		command     string
		examined    int
		already     int
		unavailable int
		unsupported int
		wantField   string
		wantMessage string
	}{
		{name: "negated passing", command: "! test -d .", examined: 1},
		{name: "negated failing is vacuous", command: "! test -f missing-probe-target", examined: 1, already: 1, wantField: "vacuous_criterion", wantMessage: "current tree"},
		{name: "stuck negation is unsupported", command: "!test -f x", unsupported: 1, wantField: "criterion_admission", wantMessage: "unsupported"},
		{name: "grep exit other than 0 or 1", command: "grep -q needle missing-grep-target.txt", unavailable: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			report := admitOne(dir, false, "tree", tc.command)
			if report.Examined != tc.examined || report.AlreadyPassing != tc.already || report.Unavailable != tc.unavailable || report.Unsupported != tc.unsupported {
				t.Fatalf("%#v", report)
			}
			if tc.wantField == "" {
				if len(report.Errors) != 0 || len(report.Warnings) != 0 {
					t.Fatalf("findings errors=%#v warnings=%#v", report.Errors, report.Warnings)
				}
				return
			}
			findings := append(append([]Finding{}, report.Errors...), report.Warnings...)
			if len(findings) != 1 || findings[0].Field != tc.wantField || !strings.Contains(findings[0].Message, tc.wantMessage) {
				t.Fatalf("findings %#v", findings)
			}
		})
	}
}

func TestAdmitProbeTimeoutIsUnavailable(t *testing.T) {
	dir := admitRepo(t)
	start := time.Now()
	report := admitOne(dir, false, "tree", "grep -a -q needle /dev/zero")
	if elapsed := time.Since(start); elapsed < probeTimeout-time.Second {
		t.Fatalf("returned in %s, before the probe deadline", elapsed)
	}
	if report.Unavailable != 1 || report.Examined != 0 || report.AlreadyPassing != 0 || len(report.Errors) != 0 {
		t.Fatalf("%#v", report)
	}
}

func TestAdmitWithoutGitIsUnavailable(t *testing.T) {
	report := admitOne(t.TempDir(), false, "tree", "test -d .")
	if report.Unavailable != 1 || report.Examined != 0 || len(report.Errors) != 0 {
		t.Fatalf("%#v", report)
	}
}

func TestAdmitSnapshotOperandBoundary(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "marker"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("marker", filepath.Join(dir, "inside")); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(outside, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "escape")); err != nil {
		t.Fatal(err)
	}
	tree := "commit abc (tracked snapshot; repository /repo)"
	inside := admitOne(dir, true, tree, "test -f inside")
	if inside.Examined != 1 || inside.AlreadyPassing != 1 || len(inside.Errors) != 1 {
		t.Fatalf("%#v", inside)
	}
	if inside.Errors[0].Field != "vacuous_criterion" || !strings.Contains(inside.Errors[0].Message, tree) || strings.Contains(inside.Errors[0].Message, "current tree") {
		t.Fatalf("message %q", inside.Errors[0].Message)
	}
	for _, command := range []string{"test -f /etc/passwd", "test -f escape", "test -f ../secret"} {
		report := admitOne(dir, true, tree, command)
		if report.Unavailable != 1 || report.Examined != 0 || report.AlreadyPassing != 0 || len(report.Errors) != 0 {
			t.Fatalf("%s %#v", command, report)
		}
		if len(report.Warnings) != 1 || !strings.Contains(report.Warnings[0].Message, "operands are not contained by the selected tree") {
			t.Fatalf("%s warnings %#v", command, report.Warnings)
		}
	}
}

func admitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".git"), []byte("gitdir: unused\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func admitOne(dir string, selected bool, tree, command string) Admission {
	return AdmitCriteria(context.Background(), dir, "todo", tree, selected, []Criterion{{Text: "criterion text", Command: command}})
}
