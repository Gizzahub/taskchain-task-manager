package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Gizzahub/taskchain-task-manager/internal/card"
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, out, errOut io.Writer) int {
	if len(args) > 0 && (args[0] == "init" || args[0] == "create" || args[0] == "list" || args[0] == "ready") {
		return runStore(args, out, errOut)
	}
	if len(args) == 1 && (args[0] == "--help" || args[0] == "help") {
		fmt.Fprintln(out, "Usage: taskchain-task-manager <show|validate> <file> --json")
		fmt.Fprintln(out, "       taskchain-task-manager <init|list|ready> --dir <board> --json")
		fmt.Fprintln(out, "       taskchain-task-manager create --dir <board> --title <title> [--id TASK-N] [--depends-on TASK-N ...] --json")
		return 0
	}
	if len(args) != 3 || args[2] != "--json" || (args[0] != "show" && args[0] != "validate") {
		fmt.Fprintln(errOut, "usage: taskchain-task-manager <show|validate> <file> --json")
		return 2
	}
	data, err := os.ReadFile(args[1])
	if err != nil {
		fmt.Fprintln(errOut, "read card:", err)
		return 1
	}
	doc, err := card.Parse(data)
	if err != nil {
		fmt.Fprintln(errOut, "parse card:", err)
		return 1
	}
	var result any = doc.Snapshot(cardRelativePath(args[1]))
	if args[0] == "validate" {
		result = struct {
			Valid bool `json:"valid"`
		}{true}
	}
	if err := json.NewEncoder(out).Encode(result); err != nil {
		fmt.Fprintln(errOut, "write result:", err)
		return 1
	}
	return 0
}

// Only a tasks/ boundary makes directory names workflow metadata. An unrelated
// ancestor named done or review must not change a standalone document's view.
func cardRelativePath(path string) string {
	parts := strings.Split(filepath.ToSlash(filepath.Clean(path)), "/")
	for i := len(parts) - 2; i >= 0; i-- {
		if parts[i] == "tasks" {
			return strings.Join(parts[i+1:], "/")
		}
	}
	return filepath.Base(path)
}
