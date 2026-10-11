package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Gizzahub/taskchain-task-manager/internal/taskflow"
)

func taskArchive(args []string, out io.Writer) error {
	force := false
	var positional []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "--help", "-h":
			fmt.Fprintln(out, taskUsage)
			return nil
		case "--force":
			force = true
		case "--":
			positional = append(positional, args[i+1:]...)
			i = len(args)
		default:
			if strings.HasPrefix(arg, "-") {
				return fmt.Errorf("unknown flag: %s (valid: --force, --help, -h)", arg)
			}
			positional = append(positional, arg)
		}
	}
	if len(positional) == 0 {
		return errors.New("task archive takes one path")
	}
	if len(positional) > 1 {
		return fmt.Errorf("unexpected argument: %s (task archive takes one path)", positional[1])
	}
	taskFile := positional[0]
	root, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get working directory: %w", err)
	}
	fmt.Fprintln(out, "📦 Archiving task...")
	card, err := taskflow.ReadCard(root, cardTasksRel(root, taskFile))
	if err != nil {
		return fmt.Errorf("archive failed: %s", archiveReadNote(root, taskFile, err))
	}
	if card.Status != taskflow.StatusDone && !force {
		return errors.New("Task is not marked as done\nUse --force to archive anyway")
	}
	if !force {
		if note := qualityReviewNote(card); note != "" {
			return fmt.Errorf("%s\nUse --force to archive anyway", note)
		}
	}
	newRel, links, err := taskflow.ArchiveCard(root, taskFile)
	if err != nil {
		return fmt.Errorf("archive failed: %s", err)
	}
	fmt.Fprintf(out, "✅ Archived: %s → %s\n", taskFile, filepath.Join(taskflow.TasksDirName(), newRel))
	printTaskLinkUpdate(out, "   ", links)
	return nil
}

func archiveReadNote(root, taskFile string, err error) string {
	if _, statErr := os.Stat(filepath.Join(root, taskFile)); statErr != nil {
		return "file not found: " + filepath.Join(root, taskFile)
	}
	return err.Error()
}

func qualityReviewNote(card *taskflow.Card) string {
	verdict := strings.TrimSpace(card.Frontmatter["quality-review"])
	evidence := strings.TrimSpace(card.Frontmatter["quality-review-evidence"])
	approved := verdict == "pass" || verdict == "conditional"
	waived := verdict == "waived"
	if (approved || waived) && evidence != "" {
		return ""
	}
	switch {
	case verdict == "":
		return "quality-review missing (need pass|conditional|waived before archive)"
	case waived:
		return "quality-review=waived without quality-review-evidence (evidence is what makes waived a verdict, not a bypass)"
	case approved:
		return fmt.Sprintf("quality-review=%s without quality-review-evidence", verdict)
	default:
		return fmt.Sprintf("quality-review=%q (need pass|conditional|waived before archive)", verdict)
	}
}
