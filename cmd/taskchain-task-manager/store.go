package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/Gizzahub/taskchain-task-manager/internal/taskstore"
)

type repeatedString []string

func (r *repeatedString) String() string         { return strings.Join(*r, ",") }
func (r *repeatedString) Set(value string) error { *r = append(*r, value); return nil }

func runStore(args []string, out, errOut io.Writer) int {
	flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
	flags.SetOutput(errOut)
	dir := flags.String("dir", "tasks", "task board directory")
	asJSON := flags.Bool("json", false, "write JSON")
	var title, id, kind *string
	var dependsOn repeatedString
	if args[0] == "create" {
		title = flags.String("title", "", "task title")
		id = flags.String("id", "", "optional canonical task ID")
		kind = flags.String("kind", "", "card kind: task, plan, issue, backlog (default task or explicit ID kind)")
		flags.Var(&dependsOn, "depends-on", "canonical prerequisite task ID (repeatable)")
	}
	if err := flags.Parse(args[1:]); err != nil {
		return 2
	}
	if flags.NArg() != 0 || !*asJSON || *dir == "" {
		fmt.Fprintln(errOut, "expected --dir <board> --json and no positional arguments")
		return 2
	}
	var result any
	var err error
	switch args[0] {
	case "init":
		err = taskstore.Init(*dir)
		result = struct {
			Directory string `json:"directory"`
		}{*dir}
	case "list":
		result, err = taskstore.List(*dir)
	case "ready":
		result, err = taskstore.Ready(*dir)
	case "create":
		if *title == "" {
			fmt.Fprintln(errOut, "create requires --title")
			return 2
		}
		result, err = taskstore.Create(*dir, taskstore.CreateRequest{ID: *id, Title: *title, DependsOn: dependsOn, Kind: *kind})
	}
	if err != nil {
		fmt.Fprintln(errOut, args[0]+":", err)
		return 1
	}
	if err := json.NewEncoder(out).Encode(result); err != nil {
		fmt.Fprintln(errOut, "write result:", err)
		return 1
	}
	return 0
}
