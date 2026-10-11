package taskflow

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// ReviewReceipt is the canonical JSON a caller stores beside an accepted card.
type ReviewReceipt struct {
	ReviewedCardSHA256 string `json:"reviewed-card-sha256"`
	ToolVersion        string `json:"tool_version"`
	ToolRevision       string `json:"tool_revision"`
}

// reviewExcluded are verdict fields the digest must not cover. Evidence stays
// inside the subject so it cannot be rewritten after the digest is taken.
var reviewExcluded = map[string]bool{
	"quality-review":         true,
	"quality-reviewed-at":    true,
	"quality-review-receipt": true,
	"review_status":          true,
}

// CanonicalReviewDigest hashes frontmatter (minus verdict keys) as JSON with
// sorted keys, then a separator, then the body verbatim.
func CanonicalReviewDigest(content string) (string, error) {
	fields, body, detected, err := parseCanonicalDocument(content)
	if !detected || err != nil {
		return "", fmt.Errorf("cannot compute canonical review digest: card has invalid or missing frontmatter")
	}
	subject := make(map[string]any, len(fields))
	for key, node := range fields {
		if reviewExcluded[key] {
			continue
		}
		var value any
		if err := node.Decode(&value); err != nil {
			return "", fmt.Errorf("cannot compute canonical review digest: card has invalid or missing frontmatter")
		}
		subject[key] = value
	}
	encoded, err := json.Marshal(subject)
	if err != nil {
		return "", fmt.Errorf("cannot compute canonical review digest: card has invalid or missing frontmatter")
	}
	sum := sha256.New()
	sum.Write(encoded)
	sum.Write([]byte("\n---\n"))
	sum.Write([]byte(body))
	return hex.EncodeToString(sum.Sum(nil)), nil
}

// NewReviewReceiptJSON returns the indented receipt plus a trailing newline.
func NewReviewReceiptJSON(content string) ([]byte, error) {
	digest, err := CanonicalReviewDigest(content)
	if err != nil {
		return nil, err
	}
	data, err := json.MarshalIndent(ReviewReceipt{
		ReviewedCardSHA256: digest,
		ToolVersion:        ReferenceStampVersion,
		ToolRevision:       ReferenceStampRevision,
	}, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}
