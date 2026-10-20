package roleprotocol

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	maxAckBytes      = 64 << 10
	maxOperatorBytes = 128
	maxAckLifetime   = 24 * time.Hour
	ackTimeLayout    = "2006-01-02T15:04:05Z"
)

// ErrUnavailable means the clock cannot be trusted or read; the ack window is
// then undecidable and must not pass.
var ErrUnavailable = errors.New("trusted clock unavailable")

// Target is the prepared target the operator accepts. It deliberately holds
// no target journal/activation raw hash, so the ack never hashes itself.
type Target struct {
	PolicyDigest    string
	SelectionDigest string
	BindingDigest   string
	HistoryDigest   string
}

// Ack is the §14.3 operator cutover acknowledgement. A valid ack is a record
// of an owner-run explicit acceptance; the JSON alone neither authenticates
// the operator nor proves unsupported processes were excluded.
type Ack struct {
	Operator      string
	IssuedAt      time.Time
	ExpiresAt     time.Time
	Root          string
	Board         string
	Head          string
	Snapshot      string
	Source        SourceTuple
	Original      Original
	Target        Target
	SupportDigest string
	Participants  []string
}

// ParseAck strictly decodes and validates a standalone ack.
func ParseAck(raw []byte) (Ack, error) {
	n, err := parseJSON(raw, maxAckBytes, "ack")
	if err != nil {
		return Ack{}, err
	}
	return decodeAck(n)
}

func decodeAck(n *node) (Ack, error) {
	if n.size > maxAckBytes {
		return Ack{}, fmt.Errorf("ack exceeds %d bytes", maxAckBytes)
	}
	f, err := fields(n, "ack", "version", "operator", "issuedAt", "expiresAt", "root", "board", "head", "snapshot",
		"source", "original", "target", "supportDigest", "participants", "unsupportedStopped", "inflightDrained",
		"supportAccepted")
	if err != nil {
		return Ack{}, err
	}
	if err := exactInt(f["version"], "ack.version", 1); err != nil {
		return Ack{}, err
	}
	var a Ack
	for _, s := range []struct {
		key string
		out *string
	}{{"operator", &a.Operator}, {"root", &a.Root}, {"board", &a.Board}} {
		if *s.out, err = stringValue(f[s.key], "ack."+s.key); err != nil {
			return Ack{}, err
		}
	}
	if a.IssuedAt, err = ackTime(f["issuedAt"], "ack.issuedAt"); err != nil {
		return Ack{}, err
	}
	if a.ExpiresAt, err = ackTime(f["expiresAt"], "ack.expiresAt"); err != nil {
		return Ack{}, err
	}
	if a.Head, err = oidValue(f["head"], "ack.head"); err != nil {
		return Ack{}, err
	}
	if a.Snapshot, err = digestValue(f["snapshot"], "ack.snapshot"); err != nil {
		return Ack{}, err
	}
	if a.Source, err = decodeSourceTuple(f["source"], "ack.source"); err != nil {
		return Ack{}, err
	}
	if a.Original, err = decodeOriginal(f["original"], "ack.original"); err != nil {
		return Ack{}, err
	}
	tf, err := fields(f["target"], "ack.target", "policyDigest", "selectionDigest", "bindingDigest", "historyDigest")
	if err != nil {
		return Ack{}, err
	}
	for _, d := range []struct {
		key string
		out *string
	}{{"policyDigest", &a.Target.PolicyDigest}, {"selectionDigest", &a.Target.SelectionDigest},
		{"bindingDigest", &a.Target.BindingDigest}, {"historyDigest", &a.Target.HistoryDigest}} {
		if *d.out, err = digestValue(tf[d.key], "ack.target."+d.key); err != nil {
			return Ack{}, err
		}
	}
	if a.SupportDigest, err = digestValue(f["supportDigest"], "ack.supportDigest"); err != nil {
		return Ack{}, err
	}
	participants, err := arrayValue(f["participants"], "ack.participants")
	if err != nil {
		return Ack{}, err
	}
	for _, p := range participants {
		participant, err := stringValue(p, "ack.participants[]")
		if err != nil {
			return Ack{}, err
		}
		a.Participants = append(a.Participants, participant)
	}
	for _, key := range []string{"unsupportedStopped", "inflightDrained", "supportAccepted"} {
		if err := trueValue(f[key], "ack."+key); err != nil {
			return Ack{}, err
		}
	}
	if err := a.Validate(); err != nil {
		return Ack{}, err
	}
	return a, nil
}

// ackTime accepts only the exact UTC spelling, so one instant has one form.
func ackTime(n *node, what string) (time.Time, error) {
	s, err := stringValue(n, what)
	if err != nil {
		return time.Time{}, err
	}
	t, err := time.Parse(ackTimeLayout, s)
	if err != nil || t.Format(ackTimeLayout) != s {
		return time.Time{}, fmt.Errorf("%s must be exact UTC YYYY-MM-DDTHH:MM:SSZ", what)
	}
	return t, nil
}

// Validate checks every closed-shape rule that does not need the clock.
func (a Ack) Validate() error {
	if a.Operator == "" || len(a.Operator) > maxOperatorBytes || !utf8.ValidString(a.Operator) || hasControl(a.Operator) {
		return fmt.Errorf("ack.operator must be 1..%d bytes without control characters", maxOperatorBytes)
	}
	if a.IssuedAt.Location() != time.UTC || a.ExpiresAt.Location() != time.UTC {
		return errors.New("ack times must be UTC")
	}
	if !a.ExpiresAt.After(a.IssuedAt) || a.ExpiresAt.Sub(a.IssuedAt) > maxAckLifetime {
		return errors.New("ack lifetime must be positive and at most 24h")
	}
	if err := validRoot(a.Root); err != nil {
		return err
	}
	if err := validBoard(a.Board); err != nil {
		return err
	}
	if !isGitOID(a.Head) || !isDigest(a.Snapshot) || !isDigest(a.SupportDigest) {
		return errors.New("ack head, snapshot or supportDigest is invalid")
	}
	if err := a.Source.Validate(); err != nil {
		return fmt.Errorf("ack.source: %w", err)
	}
	if err := a.Original.Validate(); err != nil {
		return fmt.Errorf("ack.original: %w", err)
	}
	t := a.Target
	if !isDigest(t.PolicyDigest) || !isDigest(t.SelectionDigest) || !isDigest(t.HistoryDigest) {
		return errors.New("ack.target digests must be lowercase hex64")
	}
	if want, _ := SelectedBinding(t.PolicyDigest, t.SelectionDigest).Digest(); t.BindingDigest != want {
		return errors.New("ack.target.bindingDigest is not the selected binding of its policy and selection")
	}
	if len(a.Participants) != 1 || a.Participants[0] != a.Root {
		return errors.New("ack.participants must be exactly [root] in v1")
	}
	return nil
}

func validRoot(root string) error {
	if !utf8.ValidString(root) || hasControl(root) || !filepath.IsAbs(root) || filepath.Clean(root) != root {
		return errors.New("ack.root must be a normalized absolute path")
	}
	return nil
}

// validBoard requires a canonical, safe, relative slash path inside root.
func validBoard(board string) error {
	if board == "." || !fs.ValidPath(board) || strings.ContainsRune(board, '\\') || hasControl(board) {
		return errors.New("ack.board must be a canonical safe relative path")
	}
	for _, part := range strings.Split(board, "/") {
		if strings.EqualFold(part, ".git") {
			return errors.New("ack.board must not enter .git")
		}
	}
	return nil
}

func hasControl(s string) bool {
	return strings.ContainsFunc(s, func(r rune) bool { return r < 0x20 || r == 0x7f })
}

// CheckWindow requires issuedAt ≤ now < expiresAt. A zero now means the
// caller has no trusted clock reading, which is unavailable, not a pass.
// Callers check it only before the first pending publication.
func (a Ack) CheckWindow(now time.Time) error {
	if now.IsZero() {
		return ErrUnavailable
	}
	if now.Before(a.IssuedAt) || !now.Before(a.ExpiresAt) {
		return errors.New("ack is outside its validity window")
	}
	return nil
}

// BoardRoot joins root and board; it is the Plan.root the ack binds.
func (a Ack) BoardRoot() string { return filepath.Join(a.Root, filepath.FromSlash(a.Board)) }

func (a Ack) node() *node {
	return objectNode(
		member{"version", intNode(1)},
		member{"operator", stringNode(a.Operator)},
		member{"issuedAt", stringNode(a.IssuedAt.Format(ackTimeLayout))},
		member{"expiresAt", stringNode(a.ExpiresAt.Format(ackTimeLayout))},
		member{"root", stringNode(a.Root)},
		member{"board", stringNode(a.Board)},
		member{"head", stringNode(a.Head)},
		member{"snapshot", stringNode(a.Snapshot)},
		member{"source", a.Source.node()},
		member{"original", a.Original.node()},
		member{"target", objectNode(
			member{"policyDigest", stringNode(a.Target.PolicyDigest)},
			member{"selectionDigest", stringNode(a.Target.SelectionDigest)},
			member{"bindingDigest", stringNode(a.Target.BindingDigest)},
			member{"historyDigest", stringNode(a.Target.HistoryDigest)})},
		member{"supportDigest", stringNode(a.SupportDigest)},
		member{"participants", stringsNode(a.Participants)},
		member{"unsupportedStopped", boolNode(true)},
		member{"inflightDrained", boolNode(true)},
		member{"supportAccepted", boolNode(true)})
}

// Digest is D(task-manager:role-cutover:v1, E(ack)).
func (a Ack) Digest() (string, error) {
	if err := a.Validate(); err != nil {
		return "", err
	}
	e, err := encodeE(nil, a.node())
	if err != nil {
		return "", err
	}
	return domainDigest(domainCutover, e), nil
}
