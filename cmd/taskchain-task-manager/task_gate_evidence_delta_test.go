package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

func TestTaskGateEvidenceRejectsEqualsLogFlag(t *testing.T) {
	for _, arg := range []string{"--log=ci.log", "--log="} {
		t.Run(arg, func(t *testing.T) {
			var out bytes.Buffer
			err := taskGateEvidence(context.Background(), []string{arg}, &out)
			var usage *taskUsageError
			if !errors.As(err, &usage) {
				t.Fatalf("error %T %[1]v is not usage", err)
			}
			if !strings.Contains(err.Error(), "unknown argument "+arg) {
				t.Fatalf("error %q", err)
			}
		})
	}
}
