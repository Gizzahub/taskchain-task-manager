package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/Gizzahub/taskchain-task-manager/internal/taskflow"
)

const reviewUsage = "usage: taskchain-task-manager task review-receipt CARD"

func taskReviewReceipt(args []string, out io.Writer) error {
	if len(args) == 1 && (args[0] == "--help" || args[0] == "-h") {
		fmt.Fprintln(out, reviewUsage)
		return nil
	}
	if len(args) != 1 || args[0] == "" || strings.HasPrefix(args[0], "-") {
		return fmt.Errorf("task review-receipt requires exactly one card path")
	}
	path := args[0]
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read review card %s: %w", path, err)
	}
	payload, err := taskflow.NewReviewReceiptJSON(string(raw))
	if err != nil {
		return fmt.Errorf("generate review receipt for %s: %w", path, err)
	}
	_, err = out.Write(payload)
	return err
}
