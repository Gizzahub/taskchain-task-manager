// Package cardid separates a card's numeric identity from its original spelling.
package cardid

import (
	"fmt"
	"strconv"
	"strings"
)

type ID struct {
	Prefix string
	Number uint64
}

func Parse(raw string) (ID, error) {
	prefix, digits, ok := strings.Cut(raw, "-")
	if !ok || digits == "" {
		return ID{}, fmt.Errorf("invalid card ID %q", raw)
	}
	switch prefix {
	case "TASK", "PLAN", "ISSUE", "BACKLOG":
	default:
		return ID{}, fmt.Errorf("invalid card ID prefix %q", prefix)
	}
	for _, c := range digits {
		if c < '0' || c > '9' {
			return ID{}, fmt.Errorf("invalid card ID number %q", digits)
		}
	}
	n, err := strconv.ParseUint(digits, 10, 64)
	if err != nil {
		return ID{}, fmt.Errorf("card ID overflow: %w", err)
	}
	return ID{Prefix: prefix, Number: n}, nil
}

func (id ID) Key() string { return id.Prefix + "-" + strconv.FormatUint(id.Number, 10) }

func PrefixForKind(kind string) (string, error) {
	switch kind {
	case "", "task":
		return "TASK", nil
	case "plan":
		return "PLAN", nil
	case "issue":
		return "ISSUE", nil
	case "backlog":
		return "BACKLOG", nil
	}
	return "", fmt.Errorf("unsupported card kind %q", kind)
}
