package roleprotocol

import (
	"errors"
	"fmt"

	"github.com/Gizzahub/taskchain-task-manager/internal/boardpolicy"
)

const (
	SourceLegacy = "legacy-source"
	SourceBound  = "role-bound-v1"

	maxTransportBytes   = 1 << 20
	maxProspectiveBytes = 64 << 10
)

// SourceTuple is the §14.4 source tuple. A legacy source carries explicit
// nulls for SupportDigest and AdoptionDigest; a bound source carries digests.
// Null is never a fallback for a missing field.
type SourceTuple struct {
	Kind                 string
	AuthorityID          string
	PolicyDigest         string
	CurrentBindingDigest string
	HistoryDigest        string
	SupportDigest        *string
	AdoptionDigest       *string
}

func decodeSourceTuple(n *node, what string) (SourceTuple, error) {
	f, err := fields(n, what, "kind", "authorityId", "policyDigest", "currentBindingDigest", "historyDigest",
		"supportDigest", "adoptionDigest")
	if err != nil {
		return SourceTuple{}, err
	}
	var t SourceTuple
	if t.Kind, err = enumValue(f["kind"], what+".kind", SourceLegacy, SourceBound); err != nil {
		return SourceTuple{}, err
	}
	if t.AuthorityID, err = stringValue(f["authorityId"], what+".authorityId"); err != nil {
		return SourceTuple{}, err
	}
	for _, d := range []struct {
		key string
		out *string
	}{{"policyDigest", &t.PolicyDigest}, {"currentBindingDigest", &t.CurrentBindingDigest}, {"historyDigest", &t.HistoryDigest}} {
		if *d.out, err = digestValue(f[d.key], what+"."+d.key); err != nil {
			return SourceTuple{}, err
		}
	}
	if t.SupportDigest, err = nullableDigest(f["supportDigest"], what+".supportDigest"); err != nil {
		return SourceTuple{}, err
	}
	if t.AdoptionDigest, err = nullableDigest(f["adoptionDigest"], what+".adoptionDigest"); err != nil {
		return SourceTuple{}, err
	}
	if err := t.Validate(); err != nil {
		return SourceTuple{}, fmt.Errorf("%s: %w", what, err)
	}
	return t, nil
}

func (t SourceTuple) Validate() error {
	if !isAuthorityID(t.AuthorityID) {
		return errors.New("authorityId must be lowercase hex32")
	}
	if !isDigest(t.PolicyDigest) || !isDigest(t.CurrentBindingDigest) || !isDigest(t.HistoryDigest) {
		return errors.New("source digests must be lowercase hex64")
	}
	switch t.Kind {
	case SourceLegacy:
		if t.SupportDigest != nil || t.AdoptionDigest != nil {
			return errors.New("legacy source requires null support and adoption digests")
		}
	case SourceBound:
		if t.SupportDigest == nil || t.AdoptionDigest == nil || !isDigest(*t.SupportDigest) || !isDigest(*t.AdoptionDigest) {
			return errors.New("role-bound source requires support and adoption digests")
		}
	default:
		return fmt.Errorf("source kind %q is unsupported", t.Kind)
	}
	return nil
}

func (t SourceTuple) node() *node {
	return objectNode(
		member{"kind", stringNode(t.Kind)},
		member{"authorityId", stringNode(t.AuthorityID)},
		member{"policyDigest", stringNode(t.PolicyDigest)},
		member{"currentBindingDigest", stringNode(t.CurrentBindingDigest)},
		member{"historyDigest", stringNode(t.HistoryDigest)},
		member{"supportDigest", nullableNode(t.SupportDigest)},
		member{"adoptionDigest", nullableNode(t.AdoptionDigest)})
}

// Equal compares the full tuple including null versus digest.
func (t SourceTuple) Equal(o SourceTuple) bool {
	return t.Kind == o.Kind && t.AuthorityID == o.AuthorityID && t.PolicyDigest == o.PolicyDigest &&
		t.CurrentBindingDigest == o.CurrentBindingDigest && t.HistoryDigest == o.HistoryDigest &&
		equalNullable(t.SupportDigest, o.SupportDigest) && equalNullable(t.AdoptionDigest, o.AdoptionDigest)
}

func equalNullable(a, b *string) bool {
	return (a == nil) == (b == nil) && (a == nil || *a == *b)
}

// Claims is the explicit claims CAS: Exists=false (SHA256 nil) means the file
// was clearly absent under a no-follow check, which differs from an empty
// file or an empty ledger. Read or permission errors must never become false.
type Claims struct {
	Exists bool
	SHA256 *string
}

// Original holds raw SHA-256 values of the source owner files. Policy is the
// raw policy file hash, distinct from the source's canonical policy digest.
type Original struct {
	Journal    string
	Activation string
	IDs        string
	Policy     string
	Claims     Claims
}

func decodeOriginal(n *node, what string) (Original, error) {
	f, err := fields(n, what, "journal", "activation", "ids", "claims", "policy")
	if err != nil {
		return Original{}, err
	}
	var o Original
	for _, d := range []struct {
		key string
		out *string
	}{{"journal", &o.Journal}, {"activation", &o.Activation}, {"ids", &o.IDs}, {"policy", &o.Policy}} {
		if *d.out, err = digestValue(f[d.key], what+"."+d.key); err != nil {
			return Original{}, err
		}
	}
	cf, err := fields(f["claims"], what+".claims", "exists", "sha256")
	if err != nil {
		return Original{}, err
	}
	if o.Claims.Exists, err = boolValue(cf["exists"], what+".claims.exists"); err != nil {
		return Original{}, err
	}
	if o.Claims.SHA256, err = nullableDigest(cf["sha256"], what+".claims.sha256"); err != nil {
		return Original{}, err
	}
	if err := o.Validate(); err != nil {
		return Original{}, fmt.Errorf("%s: %w", what, err)
	}
	return o, nil
}

func (o Original) Validate() error {
	if !isDigest(o.Journal) || !isDigest(o.Activation) || !isDigest(o.IDs) || !isDigest(o.Policy) {
		return errors.New("original owner hashes must be lowercase hex64")
	}
	if o.Claims.Exists != (o.Claims.SHA256 != nil) {
		return errors.New("claims.sha256 must be a digest exactly when claims.exists is true")
	}
	if o.Claims.SHA256 != nil && !isDigest(*o.Claims.SHA256) {
		return errors.New("claims.sha256 must be lowercase hex64")
	}
	return nil
}

func (o Original) Equal(p Original) bool {
	return o.Journal == p.Journal && o.Activation == p.Activation && o.IDs == p.IDs && o.Policy == p.Policy &&
		o.Claims.Exists == p.Claims.Exists && equalNullable(o.Claims.SHA256, p.Claims.SHA256)
}

func (o Original) node() *node {
	return objectNode(
		member{"journal", stringNode(o.Journal)},
		member{"activation", stringNode(o.Activation)},
		member{"ids", stringNode(o.IDs)},
		member{"claims", objectNode(
			member{"exists", boolNode(o.Claims.Exists)},
			member{"sha256", nullableNode(o.Claims.SHA256)})},
		member{"policy", stringNode(o.Policy)})
}

// Transport is the §14.4 prospective request. It defines no CLI. Its source
// and original are CAS inputs the owner must re-read and compare; caller
// supplied content never becomes authority.
type Transport struct {
	Source    SourceTuple
	Original  Original
	Policy    []byte // prospective raw policy bytes
	Selection []byte // prospective raw selector bytes
	Support   Support
	Ack       Ack

	PolicyDigest string // canonical digest of Policy
	Selector     Selector
}

// ParseTransport strictly decodes a transport and checks that its ack binds
// the same source, original, support and prospective policy/selection.
func ParseTransport(raw []byte) (Transport, error) {
	n, err := parseJSON(raw, maxTransportBytes, "transport")
	if err != nil {
		return Transport{}, err
	}
	f, err := fields(n, "transport", "version", "source", "original", "policy", "selection", "support", "ack")
	if err != nil {
		return Transport{}, err
	}
	if err := exactInt(f["version"], "transport.version", 1); err != nil {
		return Transport{}, err
	}
	var t Transport
	if t.Source, err = decodeSourceTuple(f["source"], "transport.source"); err != nil {
		return Transport{}, err
	}
	if t.Original, err = decodeOriginal(f["original"], "transport.original"); err != nil {
		return Transport{}, err
	}
	for _, b := range []struct {
		key string
		out *[]byte
	}{{"policy", &t.Policy}, {"selection", &t.Selection}} {
		s, err := stringValue(f[b.key], "transport."+b.key)
		if err != nil {
			return Transport{}, err
		}
		if *b.out, err = decodeBase64(s, maxProspectiveBytes, "transport."+b.key); err != nil {
			return Transport{}, err
		}
	}
	if t.Support, err = decodeSupport(f["support"]); err != nil {
		return Transport{}, err
	}
	if t.Ack, err = decodeAck(f["ack"]); err != nil {
		return Transport{}, err
	}
	if err := t.bind(); err != nil {
		return Transport{}, err
	}
	return t, nil
}

func (t *Transport) bind() error {
	policy, err := boardpolicy.Parse(t.Policy)
	if err != nil {
		return fmt.Errorf("transport.policy: %w", err)
	}
	if t.PolicyDigest, err = policy.Digest(); err != nil {
		return fmt.Errorf("transport.policy: %w", err)
	}
	if t.Selector, err = ParseSelector(t.Selection); err != nil {
		return fmt.Errorf("transport.selection: %w", err)
	}
	selection, _ := t.Selector.Digest()
	supportDigest, err := t.Support.Digest()
	if err != nil {
		return err
	}
	a := t.Ack
	switch {
	case !a.Source.Equal(t.Source):
		return errors.New("ack.source differs from transport.source")
	case !a.Original.Equal(t.Original):
		return errors.New("ack.original differs from transport.original")
	case a.SupportDigest != supportDigest:
		return errors.New("ack.supportDigest differs from the transported support")
	case a.Target.PolicyDigest != t.PolicyDigest:
		return errors.New("ack.target.policyDigest differs from the prospective policy")
	case a.Target.SelectionDigest != selection:
		return errors.New("ack.target.selectionDigest differs from the prospective selection")
	case !t.Support.Accepts(t.Selector):
		return fmt.Errorf("support does not accept profile %s", t.Selector.Profile())
	}
	return nil
}
