package main

import (
	"fmt"
	"io"
	"os"

	"github.com/Gizzahub/taskchain-task-manager/internal/taskflow"
)

const changeIDUsage = "usage: taskchain-task-manager task change-id [--help|-h]"

func taskChangeID(args []string, out io.Writer) error {
	if len(args) == 1 && (args[0] == "--help" || args[0] == "-h") {
		fmt.Fprintln(out, changeIDUsage)
		return nil
	}
	if len(args) != 0 {
		return usagef("unknown argument %s (valid: --help, -h)", args[0])
	}
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	id, err := taskflow.BranchChangeID(root)
	if err != nil {
		return err
	}
	fmt.Fprintln(out, id)
	return nil
}
