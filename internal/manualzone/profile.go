package manualzone

import (
	"errors"
	"fmt"
	"path"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Profile is an explicit role selection. The zero value and any amendment
// other than section-11 are not a manual lifecycle.
type Profile struct {
	Base      string
	Amendment string
}

func (p Profile) section11() bool {
	switch p.Base {
	case "section-9", "section-10":
	default:
		return false
	}
	return p.Amendment == "section-11"
}

// CardFields is the manual admission view of one card. OperatorScalar is set
// when the card still carries a scalar operator; that is a migration finding,
// not a value this package rewrites into a structure.
type CardFields struct {
	ID             string
	ManualAction   string
	OperatorKind   string
	OperatorID     string
	OperatorScalar bool
	ReceiptPath    string
	ReceiptFacts   string
}

var generationRe = regexp.MustCompile(`^[0-9a-f]{32}$`)

// ReceiptNames returns the board-or-repo relative receipt, entry, and close
// paths bound to this card. Section 9 paths are relative to the board root.
// Section 10 paths are relative to the repository root.
func ReceiptNames(profile Profile, fields CardFields) (receipt, entry, close string, err error) {
	if !profile.section11() {
		return "", "", "", errors.New("manual lifecycle is not selected")
	}
	if err := validateFields(fields); err != nil {
		return "", "", "", err
	}
	generation, err := generationOf(profile, fields)
	if err != nil {
		return "", "", "", err
	}
	receipt = fields.ReceiptPath
	stem := strings.TrimSuffix(path.Base(receipt), ".json")
	if stem != "manual-"+generation {
		return "", "", "", errors.New("receipt file name does not bind the entry generation")
	}
	dir := path.Dir(receipt)
	return receipt, dir + "/" + stem + ".entry.json", dir + "/" + stem + ".close.json", nil
}

func validateFields(fields CardFields) error {
	if fields.ID == "" || strings.Contains(fields.ID, "/") || strings.Contains(fields.ID, `\`) {
		return errors.New("manual card id is missing or contains a path separator")
	}
	if strings.TrimSpace(fields.ManualAction) == "" {
		return errors.New("manual-action is required")
	}
	if fields.OperatorScalar {
		return errors.New("scalar operator is a migration finding and is not rewritten")
	}
	if fields.OperatorKind != "human" {
		return errors.New("operator.kind must be human")
	}
	if err := validateOperatorID(fields.OperatorID); err != nil {
		return err
	}
	if strings.TrimSpace(fields.ReceiptFacts) == "" {
		return errors.New("expected-receipt facts are required")
	}
	if fields.ReceiptPath == "" {
		return errors.New("expected-receipt path is required")
	}
	return nil
}

func validateOperatorID(id string) error {
	if id == "" || strings.TrimSpace(id) != id {
		return errors.New("operator.id must be non-empty and have no surrounding space")
	}
	if !utf8.ValidString(id) {
		return errors.New("operator.id must be valid UTF-8")
	}
	for _, r := range id {
		if unicode.IsControl(r) {
			return errors.New("operator.id must not contain a control character")
		}
	}
	return nil
}

func generationOf(profile Profile, fields CardFields) (string, error) {
	rel := path.Clean("/" + fields.ReceiptPath)
	if rel == "/" || strings.HasPrefix(fields.ReceiptPath, "/") || strings.Contains(fields.ReceiptPath, `\`) {
		return "", errors.New("receipt path must be a relative POSIX path")
	}
	if path.Clean(fields.ReceiptPath) != fields.ReceiptPath || strings.HasPrefix(fields.ReceiptPath, "../") || strings.Contains("/"+fields.ReceiptPath+"/", "/../") || strings.Contains("/"+fields.ReceiptPath+"/", "/./") {
		return "", errors.New("receipt path must not contain . or .. segments")
	}
	var wantPrefix string
	switch profile.Base {
	case "section-9":
		wantPrefix = "receipts/" + fields.ID + "/manual-"
	case "section-10":
		wantPrefix = "docs/task-reviews/" + fields.ID + "/manual-"
	default:
		return "", fmt.Errorf("base profile %q is not a manual lifecycle", profile.Base)
	}
	if !strings.HasPrefix(fields.ReceiptPath, wantPrefix) || !strings.HasSuffix(fields.ReceiptPath, ".json") {
		return "", errors.New("receipt path does not match the selected profile")
	}
	generation := strings.TrimSuffix(strings.TrimPrefix(fields.ReceiptPath, wantPrefix), ".json")
	if !generationRe.MatchString(generation) {
		return "", errors.New("entry generation must be 32 lowercase hex digits")
	}
	return generation, nil
}
