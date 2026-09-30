package taskflow

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// AdvisoryNoExecTier is the advisory a card without an exec-tier earns: the
// queue still runs, at the standard tier, and the caller is told so.
const AdvisoryNoExecTier = "no-exec-tier"

// AdvisoryMessage spells one advisory code for the text render.
func AdvisoryMessage(code string) string {
	if code == AdvisoryNoExecTier {
		return "no exec-tier; falls back to standard"
	}
	return code
}

// PreflightCard is one card's readiness verdict.
type PreflightCard struct {
	Path     string
	Zone     string
	Title    string
	Priority string
	Runnable bool
	Blocking []string
	Advisory []string
	Criteria int
	Bound    int
	Bytes    int64
}

// PreflightReport is the queue's readiness verdict and the per-card facts it
// was read from.
type PreflightReport struct {
	Verdict    string
	Total      int
	Runnable   int
	Unrunnable int
	Blocking   map[string]int
	Advisory   map[string]int
	Cards      []PreflightCard
	Lint       *LintCensus
}

// CollectPreflight reads every live card's metadata and decides whether the
// board is a queue that can run. No card body is executed and no card content
// beyond metadata is read: preflight is the read-before-run, not the run.
func CollectPreflight(root string) (*PreflightReport, error) {
	cards, err := FindCards(root, true)
	if err != nil {
		return nil, err
	}
	lint, err := CollectLint(root)
	if err != nil {
		return nil, err
	}
	statusByID := map[string]Status{}
	for _, card := range cards {
		statusByID[card.ID] = card.Status
	}
	report := &PreflightReport{
		Verdict:  "NO_QUEUE",
		Blocking: map[string]int{},
		Advisory: map[string]int{},
		Lint:     lint,
	}
	for _, card := range cards {
		pc := PreflightCard{
			Path:     card.RepoRel(),
			Zone:     card.Zone,
			Title:    card.Title,
			Priority: card.Priority,
			Blocking: []string{},
			Advisory: []string{},
		}
		if info, err := os.Stat(filepath.Join(root, TasksDir, filepath.FromSlash(card.TasksRel))); err == nil {
			pc.Bytes = info.Size()
		}
		criteria := card.Criteria()
		pc.Criteria = len(criteria)
		for _, criterion := range criteria {
			if verifyBindingValid(criterion.Text) {
				pc.Bound++
			}
		}
		pc.Runnable = len(criteria) > 0 && pc.Bound == len(criteria)
		for _, dep := range frontmatterList(card.Raw, "depends-on") {
			if st, known := statusByID[dep]; !known || st != StatusDone {
				pc.Blocking = append(pc.Blocking, dep)
				report.Blocking[dep]++
			}
		}
		if strings.TrimSpace(card.Frontmatter["exec-tier"]) == "" {
			pc.Advisory = append(pc.Advisory, AdvisoryNoExecTier)
			report.Advisory[AdvisoryNoExecTier]++
		}
		if pc.Runnable {
			report.Runnable++
		} else {
			report.Unrunnable++
		}
		report.Cards = append(report.Cards, pc)
	}
	report.Total = len(report.Cards)
	switch {
	case report.Total == 0:
		report.Verdict = "NO_QUEUE"
	case report.Unrunnable == 0 && len(report.Blocking) == 0:
		report.Verdict = "READY"
	default:
		report.Verdict = "NOT_READY"
	}
	return report, nil
}

// frontmatterList reads a frontmatter key as a list of words, from either a
// YAML sequence or a comma/space separated scalar.
func frontmatterList(raw []byte, key string) []string {
	doc := parseCanonicalDocument(raw)
	node, ok := doc.Fields[key]
	if !ok {
		return nil
	}
	var out []string
	if node.Kind == yaml.ScalarNode {
		if node.Tag == "!!null" {
			return nil
		}
		for _, word := range strings.FieldsFunc(node.Value, func(r rune) bool { return r == ',' || isSpaceRune(r) }) {
			if word != "" {
				out = append(out, word)
			}
		}
		return out
	}
	for _, item := range node.Content {
		if item.Kind == yaml.ScalarNode && item.Tag != "!!null" {
			out = append(out, strings.TrimSpace(item.Value))
		}
	}
	return out
}

// RenderPreflightText writes the queue verdict. READY and NOT_READY name their
// runnable and blocked cards; NO_QUEUE is the one-line answer for a board with
// nothing to run.
func RenderPreflightText(w io.Writer, report *PreflightReport) {
	switch report.Verdict {
	case "NO_QUEUE":
		fmt.Fprintln(w, "TASK PREFLIGHT — NO_QUEUE (valid cleared/reference-only board; no execution queue)")
		return
	case "READY":
		fmt.Fprintln(w, "TASK PREFLIGHT — READY")
	default:
		fmt.Fprintln(w, "TASK PREFLIGHT — NOT_READY")
	}
	fmt.Fprintf(w, "zones: all    dialect: ce    total %d    runnable %d    unrunnable %d\n",
		report.Total, report.Runnable, report.Unrunnable)
	fmt.Fprintln(w)
	if n := report.Runnable; n > 0 {
		fmt.Fprintln(w, "RUNNABLE")
		for _, card := range report.Cards {
			if card.Runnable {
				fmt.Fprintf(w, "  %-5s%s (%d/%d bound)\n", card.Priority, card.Path, card.Bound, card.Criteria)
			}
		}
		fmt.Fprintln(w)
	}
	if n := report.Unrunnable; n > 0 {
		fmt.Fprintf(w, "UNRUNNABLE (%d)\n", n)
		for _, card := range report.Cards {
			if !card.Runnable {
				fmt.Fprintf(w, "  %-5s%s (%d/%d bound)\n", card.Priority, card.Path, card.Bound, card.Criteria)
			}
		}
		fmt.Fprintln(w)
	}
	if len(report.Blocking) > 0 {
		fmt.Fprintln(w, "BLOCKED")
		for _, card := range report.Cards {
			for _, dep := range card.Blocking {
				fmt.Fprintf(w, "  %s waits on %s\n", card.Path, dep)
			}
		}
		fmt.Fprintln(w)
	}
	if len(report.Advisory) > 0 {
		fmt.Fprintln(w, "ADVISORY (degrades a run, does not stop it)")
		for code, count := range report.Advisory {
			fmt.Fprintf(w, "    %d  %-25s%s\n", count, code, AdvisoryMessage(code))
		}
		fmt.Fprintln(w)
	}
	fmt.Fprintln(w, "NOT DONE: no card was read past its metadata, moved, or modified.")
}

// preflightJSON is the machine-readable preflight. Field order is the byte
// contract the fixtures pin.
type preflightJSON struct {
	Verdict    string              `json:"verdict"`
	Zones      []string            `json:"zones"`
	Total      int                 `json:"total"`
	Runnable   int                 `json:"runnable"`
	Unrunnable int                 `json:"unrunnable"`
	Blocking   map[string]int      `json:"blocking"`
	Advisory   map[string]int      `json:"advisory"`
	Cards      []preflightCardJSON `json:"cards"`
	Strays     preflightStraysJSON `json:"strays"`
}

type preflightCardJSON struct {
	Path     string   `json:"path"`
	Zone     string   `json:"zone"`
	Title    string   `json:"title"`
	Priority string   `json:"priority"`
	Runnable bool     `json:"runnable"`
	Blocking []string `json:"blocking"`
	Advisory []string `json:"advisory"`
	Criteria int      `json:"criteria"`
	Bound    int      `json:"bound"`
	Bytes    int64    `json:"bytes"`
}

type preflightStraysJSON struct {
	Clean            bool               `json:"clean"`
	HiddenWIP        int                `json:"hidden_wip"`
	Counts           map[string]int     `json:"counts"`
	Dirs             []preflightDirJSON `json:"dirs"`
	Cards            []string           `json:"cards"`
	Warnings         []string           `json:"warnings"`
	Structure        []string           `json:"structure"`
	StageDiagnostics []string           `json:"stage_diagnostics"`
	IDParity         []string           `json:"id_parity"`
}

type preflightDirJSON struct {
	Name  string `json:"name"`
	Kind  string `json:"kind"`
	Cards int    `json:"cards"`
}

// RenderPreflightJSON writes the machine-readable preflight verdict.
func RenderPreflightJSON(w io.Writer, report *PreflightReport) error {
	payload := preflightJSON{
		Verdict:    report.Verdict,
		Zones:      []string{},
		Total:      report.Total,
		Runnable:   report.Runnable,
		Unrunnable: report.Unrunnable,
		Blocking:   report.Blocking,
		Advisory:   report.Advisory,
		Cards:      make([]preflightCardJSON, 0, len(report.Cards)),
	}
	if payload.Blocking == nil {
		payload.Blocking = map[string]int{}
	}
	if payload.Advisory == nil {
		payload.Advisory = map[string]int{}
	}
	for _, card := range report.Cards {
		payload.Cards = append(payload.Cards, preflightCardJSON{
			Path: card.Path, Zone: card.Zone, Title: card.Title, Priority: card.Priority,
			Runnable: card.Runnable, Blocking: card.Blocking, Advisory: card.Advisory,
			Criteria: card.Criteria, Bound: card.Bound, Bytes: card.Bytes,
		})
	}
	strays := preflightStraysJSON{
		Clean:            report.Lint.Clean(),
		HiddenWIP:        len(report.Lint.HiddenWIP),
		Counts:           map[string]int{},
		Dirs:             make([]preflightDirJSON, 0, len(report.Lint.Dirs)),
		Cards:            []string{},
		Warnings:         []string{},
		Structure:        []string{},
		StageDiagnostics: []string{},
		IDParity:         []string{},
	}
	for _, dir := range report.Lint.Dirs {
		strays.Dirs = append(strays.Dirs, preflightDirJSON{Name: dir.Name, Kind: dir.Kind, Cards: dir.Cards})
	}
	payload.Strays = strays
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	fmt.Fprintln(w, string(data))
	return nil
}
