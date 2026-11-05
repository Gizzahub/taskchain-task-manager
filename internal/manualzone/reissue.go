package manualzone

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
)

// ReissueRequest is one human request to retire the current entry and bind a
// new generation. The same request bytes always derive the same generation.
// ActorKind is not authentication; the filesystem apply resolves the writer.
type ReissueRequest struct {
	ActorKind          string
	Reason             string
	RequestID          string
	PreviousGeneration string
	PreviousDigest     string
	CurrentDigest      string
}

// ReissuePlan is the card and records a reissue publishes together.
type ReissuePlan struct {
	NewCard string
	Entry   EntryRecord
	Close   CloseRecord
}

// AnchorPaths returns the receipt, entry, and close paths for one generation.
func AnchorPaths(profile Profile, id, generation string) (receipt, entry, close string, err error) {
	if !profile.section11() {
		return "", "", "", errors.New("manual lifecycle is not selected")
	}
	if id == "" || strings.Contains(id, "/") || strings.Contains(id, `\`) {
		return "", "", "", errors.New("manual card id is missing or contains a path separator")
	}
	if !generationRe.MatchString(generation) {
		return "", "", "", errors.New("entry generation must be 32 lowercase hex digits")
	}
	var prefix string
	switch profile.Base {
	case "section-9":
		prefix = "receipts/" + id + "/"
	case "section-10":
		prefix = "docs/task-reviews/" + id + "/"
	default:
		return "", "", "", errors.New("manual lifecycle is not selected")
	}
	stem := "manual-" + generation
	receipt = prefix + stem + ".json"
	return receipt, prefix + stem + ".entry.json", prefix + stem + ".close.json", nil
}

// PlanReissue checks one human reissue and returns the records to publish.
// It does not write and does not authenticate the caller. An unreadable run
// or claim state, an authority or scope mismatch, and a digest mismatch leave
// the plan empty.
func PlanReissue(profile Profile, fields CardFields, card string, entry EntryRecord, authority string, state RuntimeState, req ReissueRequest, digest func(string) (string, error)) (ReissuePlan, error) {
	if !profile.section11() {
		return ReissuePlan{}, errors.New("manual lifecycle is not selected")
	}
	if authority == "" || entry.BoardAuthority != authority || entry.Scope != "card" {
		return ReissuePlan{}, errors.New("entry authority or scope does not match the board")
	}
	if !state.Known {
		return ReissuePlan{}, errors.New("run and claim state is unreadable")
	}
	if state.ActiveRun || state.ActiveClaim {
		return ReissuePlan{}, errors.New("manual reissue requires no active run or claim")
	}
	nextGen, err := deriveReissueGeneration(req)
	if err != nil {
		return ReissuePlan{}, err
	}
	if digest == nil {
		return ReissuePlan{}, errors.New("digest function is required")
	}
	if entry.CardID != fields.ID || entry.EntryGeneration != req.PreviousGeneration || entry.EntrySHA256 != req.PreviousDigest {
		return ReissuePlan{}, errors.New("entry record does not match the reissue request")
	}
	newPath, err := swapGeneration(entry.ReceiptPath, req.PreviousGeneration, nextGen)
	if err != nil {
		return ReissuePlan{}, err
	}
	currentGen, err := generationOf(profile, fields)
	if err != nil {
		return ReissuePlan{}, err
	}
	var newCard string
	switch currentGen {
	case req.PreviousGeneration:
		if fields.ReceiptPath != entry.ReceiptPath {
			return ReissuePlan{}, errors.New("card receipt path does not match the entry record")
		}
		current, err := digest(card)
		if err != nil {
			return ReissuePlan{}, err
		}
		if current != req.CurrentDigest {
			return ReissuePlan{}, errors.New("current digest does not match the card")
		}
		if strings.Count(card, entry.ReceiptPath) != 1 {
			return ReissuePlan{}, errors.New("receipt path is not unique in the card")
		}
		newCard = strings.Replace(card, entry.ReceiptPath, newPath, 1)
	case nextGen:
		if fields.ReceiptPath != newPath || strings.Count(card, newPath) != 1 {
			return ReissuePlan{}, errors.New("resumed card is not the requested reissue")
		}
		restored := strings.Replace(card, newPath, entry.ReceiptPath, 1)
		restoredDigest, err := digest(restored)
		if err != nil {
			return ReissuePlan{}, err
		}
		if restoredDigest != req.CurrentDigest {
			return ReissuePlan{}, errors.New("resumed card is not the requested reissue")
		}
		newCard = card
	default:
		return ReissuePlan{}, errors.New("card generation does not match the reissue request")
	}
	newFields, err := ParseCard(newCard)
	if err != nil {
		return ReissuePlan{}, err
	}
	gotGen, err := generationOf(profile, newFields)
	if err != nil {
		return ReissuePlan{}, err
	}
	if gotGen != nextGen || newFields.ReceiptPath != newPath || newFields.ID != fields.ID {
		return ReissuePlan{}, errors.New("reissued card does not bind the new receipt path")
	}
	post, err := digest(newCard)
	if err != nil {
		return ReissuePlan{}, err
	}
	record, err := NewEntryRecord(profile, newFields, entry.BoardAuthority, newCard, post)
	if err != nil {
		return ReissuePlan{}, err
	}
	return ReissuePlan{
		NewCard: newCard,
		Entry:   record,
		Close: CloseRecord{
			CardID:          fields.ID,
			EntryGeneration: req.PreviousGeneration,
			EntrySHA256:     entry.EntrySHA256,
			RequestID:       req.RequestID,
			Result:          "reissued",
			Destination:     "manual",
			Reason:          req.Reason,
		},
	}, nil
}

func deriveReissueGeneration(req ReissueRequest) (string, error) {
	if strings.TrimSpace(req.Reason) == "" {
		return "", errors.New("manual reissue requires a reason")
	}
	if req.RequestID == "" || strings.TrimSpace(req.RequestID) != req.RequestID {
		return "", errors.New("reissue request id is required")
	}
	if !generationRe.MatchString(req.PreviousGeneration) {
		return "", errors.New("previous entry generation is not 32 lowercase hex digits")
	}
	if !isHex(req.PreviousDigest, 64) || !isHex(req.CurrentDigest, 64) {
		return "", errors.New("reissue digests must be 64 lowercase hex digits")
	}
	sum := sha256.Sum256([]byte(strings.Join([]string{
		"manual-reissue",
		req.RequestID,
		req.PreviousGeneration,
		req.PreviousDigest,
		req.CurrentDigest,
	}, "\n")))
	next := hex.EncodeToString(sum[:16])
	if next == req.PreviousGeneration {
		return "", errors.New("reissue generation collided with the previous generation")
	}
	return next, nil
}

func isHex(value string, width int) bool {
	if len(value) != width {
		return false
	}
	for _, r := range value {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'f':
		default:
			return false
		}
	}
	return true
}

func swapGeneration(receiptPath, fromGen, toGen string) (string, error) {
	from := "manual-" + fromGen + ".json"
	if !strings.HasSuffix(receiptPath, from) {
		return "", errors.New("receipt path does not end with the entry generation")
	}
	return strings.TrimSuffix(receiptPath, from) + "manual-" + toGen + ".json", nil
}
