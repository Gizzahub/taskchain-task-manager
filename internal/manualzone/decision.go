package manualzone

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// RuntimeState is the caller's observation of the card's run and claim.
// Unknown is fail-closed: a missing observation is not an idle card.
type RuntimeState struct {
	Known       bool
	ActiveRun   bool
	ActiveClaim bool
}

// PlanEntry reports the receipt paths for a legal entry. It does not write.
func PlanEntry(profile Profile, fromZone string, fields CardFields, state RuntimeState) (receipt, entry, close string, err error) {
	if !profile.section11() {
		return "", "", "", errors.New("manual lifecycle is not selected")
	}
	switch fromZone {
	case "todo", "doing", "blocked":
	case "manual":
		return "", "", "", errors.New("manual to manual is not a reissue")
	default:
		return "", "", "", fmt.Errorf("manual entry from %q is refused", fromZone)
	}
	if !state.Known {
		return "", "", "", errors.New("run and claim state is unreadable")
	}
	if state.ActiveRun || state.ActiveClaim {
		return "", "", "", errors.New("manual entry requires no active run or claim")
	}
	return ReceiptNames(profile, fields)
}

// Operator is the human operator bound into a receipt and an entry record.
type Operator struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

// Receipt is the strict section-11 exit receipt.
type Receipt struct {
	Version         int      `json:"version"`
	CardID          string   `json:"card-id"`
	EntryGeneration string   `json:"entry-generation"`
	EntrySHA256     string   `json:"entry-sha256"`
	Action          string   `json:"action"`
	Operator        Operator `json:"operator"`
	ActorKind       string   `json:"actor-kind"`
	PerformedAt     string   `json:"performed-at"`
	Result          string   `json:"result"`
	Facts           string   `json:"facts"`
	ExpectedFacts   string   `json:"expected-facts"`
}

// EntryRecord is the immutable entry evidence written beside the receipt.
type EntryRecord struct {
	Version         int      `json:"version"`
	CardID          string   `json:"card-id"`
	Scope           string   `json:"scope"`
	BoardAuthority  string   `json:"board-authority"`
	EntryGeneration string   `json:"entry-generation"`
	EntrySHA256     string   `json:"entry-sha256"`
	Action          string   `json:"action"`
	Operator        Operator `json:"operator"`
	ExpectedFacts   string   `json:"expected-facts"`
	ReceiptPath     string   `json:"receipt-path"`
	Subject         string   `json:"subject"`
}

// CloseRecord binds one consumption or reissue retirement of an entry.
type CloseRecord struct {
	CardID          string `json:"card-id"`
	EntryGeneration string `json:"entry-generation"`
	EntrySHA256     string `json:"entry-sha256"`
	RequestID       string `json:"request-id"`
	Result          string `json:"result"`
	Destination     string `json:"destination"`
	Reason          string `json:"reason,omitempty"`
}

// NewEntryRecord builds the entry evidence for the exact post-entry card bytes.
func NewEntryRecord(profile Profile, fields CardFields, authority, subject, digest string) (EntryRecord, error) {
	receipt, _, _, err := ReceiptNames(profile, fields)
	if err != nil {
		return EntryRecord{}, err
	}
	generation, err := generationOf(profile, fields)
	if err != nil {
		return EntryRecord{}, err
	}
	if digest == "" || subject == "" {
		return EntryRecord{}, errors.New("entry subject and digest are required")
	}
	if authority == "" {
		return EntryRecord{}, errors.New("board authority is required")
	}
	return EntryRecord{
		Version:         1,
		CardID:          fields.ID,
		Scope:           "card",
		BoardAuthority:  authority,
		EntryGeneration: generation,
		EntrySHA256:     digest,
		Action:          fields.ManualAction,
		Operator:        Operator{Kind: fields.OperatorKind, ID: fields.OperatorID},
		ExpectedFacts:   fields.ReceiptFacts,
		ReceiptPath:     receipt,
		Subject:         subject,
	}, nil
}

// PlanExit checks one unused receipt against the current card. cancelled is
// refused. A matching plan does not consume the receipt by itself.
func PlanExit(profile Profile, fields CardFields, card string, receipt Receipt, entry EntryRecord, authority, to, reason, blockedBy string, closeExists bool, digest func(string) (string, error)) error {
	if !profile.section11() {
		return errors.New("manual lifecycle is not selected")
	}
	if closeExists {
		return errors.New("entry generation is already closed")
	}
	if authority == "" || entry.BoardAuthority != authority || entry.Scope != "card" {
		return errors.New("entry authority or scope does not match the board")
	}
	if to == "cancelled" {
		return errors.New("rejected to cancelled is outside this support range")
	}
	receiptPath, _, _, err := ReceiptNames(profile, fields)
	if err != nil {
		return err
	}
	generation, err := generationOf(profile, fields)
	if err != nil {
		return err
	}
	if digest == nil {
		return errors.New("digest function is required")
	}
	current, err := digest(card)
	if err != nil {
		return err
	}
	subjectDigest, err := digest(entry.Subject)
	if err != nil {
		return err
	}
	// The four review-verdict fields stay outside the digest, so the stored
	// subject and the current card may differ without blocking the receipt.
	if subjectDigest != entry.EntrySHA256 || current != entry.EntrySHA256 {
		return errors.New("manual card digest does not match the entry record")
	}
	if err := validateReceipt(receipt, fields, generation, receiptPath, current); err != nil {
		return err
	}
	switch receipt.Result {
	case "done":
		if to != "todo" {
			return errors.New("result done exits only to todo")
		}
	case "rejected":
		if to != "blocked" {
			return errors.New("result rejected exits only to blocked")
		}
		if strings.TrimSpace(reason) == "" || strings.TrimSpace(blockedBy) == "" {
			return errors.New("rejected exit requires a reason and blocked-by")
		}
	default:
		return fmt.Errorf("receipt result %q is refused", receipt.Result)
	}
	if entry.CardID != fields.ID || entry.EntryGeneration != generation || entry.ReceiptPath != receiptPath || entry.Action != fields.ManualAction || entry.ExpectedFacts != fields.ReceiptFacts {
		return errors.New("entry record does not match the card")
	}
	if entry.Operator != (Operator{Kind: "human", ID: fields.OperatorID}) {
		return errors.New("entry operator does not match the card")
	}
	return nil
}

func validateReceipt(receipt Receipt, fields CardFields, generation, receiptPath, digest string) error {
	if receipt.Version != 1 {
		return errors.New("receipt version must be 1")
	}
	if receipt.CardID != fields.ID || receipt.EntryGeneration != generation || receipt.EntrySHA256 != digest {
		return errors.New("receipt does not bind the card id, generation, and digest")
	}
	if receipt.Action != fields.ManualAction || receipt.ExpectedFacts != fields.ReceiptFacts {
		return errors.New("receipt action or expected facts do not match the entry")
	}
	if strings.TrimSpace(receipt.Facts) == "" {
		return errors.New("receipt facts are required")
	}
	if receipt.ActorKind != "human" || receipt.Operator.Kind != "human" || receipt.Operator.ID != fields.OperatorID {
		return errors.New("receipt operator must be the card's human operator")
	}
	if !strings.HasSuffix(receipt.PerformedAt, "Z") {
		return errors.New("performed-at must be UTC RFC3339")
	}
	if _, err := time.Parse(time.RFC3339, receipt.PerformedAt); err != nil {
		return errors.New("performed-at must be UTC RFC3339")
	}
	if receiptPath == "" {
		return errors.New("receipt path is required")
	}
	return nil
}

// Encode strict-encodes one receipt, entry, or close record.
func Encode(value any) ([]byte, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return append(raw, '\n'), nil
}

// DecodeReceipt rejects unknown fields, duplicate keys, and a trailing extra value.
func DecodeReceipt(raw []byte) (Receipt, error) {
	var receipt Receipt
	if err := decodeStrict(raw, &receipt); err != nil {
		return Receipt{}, err
	}
	return receipt, nil
}

// DecodeEntry rejects unknown fields and duplicate keys.
func DecodeEntry(raw []byte) (EntryRecord, error) {
	var entry EntryRecord
	if err := decodeStrict(raw, &entry); err != nil {
		return EntryRecord{}, err
	}
	return entry, nil
}

func decodeStrict(raw []byte, dest any) error {
	if err := rejectDuplicateKeys(raw); err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dest); err != nil {
		return err
	}
	if dec.More() {
		return errors.New("receipt json has trailing data")
	}
	return nil
}

func rejectDuplicateKeys(raw []byte) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	if err := walkJSON(dec); err != nil {
		return err
	}
	if dec.More() {
		return errors.New("receipt json has trailing data")
	}
	return nil
}

func walkJSON(dec *json.Decoder) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	delim, ok := tok.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := map[string]struct{}{}
		for dec.More() {
			keyTok, err := dec.Token()
			if err != nil {
				return err
			}
			key, ok := keyTok.(string)
			if !ok {
				return errors.New("json object key must be a string")
			}
			if _, exists := seen[key]; exists {
				return fmt.Errorf("duplicate json key %q", key)
			}
			seen[key] = struct{}{}
			if err := walkJSON(dec); err != nil {
				return err
			}
		}
		end, err := dec.Token()
		if err != nil {
			return err
		}
		if end != json.Delim('}') {
			return errors.New("json object did not close")
		}
		return nil
	case '[':
		for dec.More() {
			if err := walkJSON(dec); err != nil {
				return err
			}
		}
		end, err := dec.Token()
		if err != nil {
			return err
		}
		if end != json.Delim(']') {
			return errors.New("json array did not close")
		}
		return nil
	default:
		return errors.New("unexpected json delimiter")
	}
}
