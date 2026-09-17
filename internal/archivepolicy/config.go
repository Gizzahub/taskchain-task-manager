package archivepolicy

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/Gizzahub/taskchain-task-manager/internal/card"
	"gopkg.in/yaml.v3"
)

const ConfigLimit = 64 * 1024

// Config is an explicit, versioned archive dialect. Every semantic field has
// an explicit top-level metadata name; no board receives an implicit dialect.
type Config struct {
	SchemaVersion int             `json:"schema-version"`
	Admission     AdmissionConfig `json:"archive-admission"`
}

type AdmissionConfig struct {
	Fields          Fields   `json:"fields"`
	AcceptedReviews []string `json:"accepted-reviews"`
}

type Fields struct {
	Review     string `json:"review"`
	Evidence   string `json:"evidence"`
	Resolution string `json:"resolution"`
	PromotedTo string `json:"promoted-to"`
	Children   string `json:"children"`
}

func (f Fields) Mapping() card.ArchiveFieldMapping {
	return card.ArchiveFieldMapping{QualityReview: f.Review, QualityReviewEvidence: f.Evidence, Resolution: f.Resolution, PromotedTo: f.PromotedTo, Children: f.Children}
}

// ParseConfig accepts one strict YAML document (including JSON syntax).
// Canonical returns JSON bytes that ParseConfig can read without losing shape.
func ParseConfig(raw []byte) (Config, error) {
	var out Config
	if len(raw) > ConfigLimit || !utf8.Valid(raw) {
		return out, fmt.Errorf("archive config exceeds size limit or is not UTF-8")
	}
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	var doc, extra yaml.Node
	if err := dec.Decode(&doc); err != nil {
		return out, fmt.Errorf("decode archive config: %w", err)
	}
	if err := dec.Decode(&extra); err != io.EOF {
		return out, fmt.Errorf("archive config requires exactly one document")
	}
	if len(doc.Content) != 1 {
		return out, fmt.Errorf("archive config requires a mapping")
	}
	root, err := configMapping(doc.Content[0], "schema-version", "archive-admission")
	if err != nil {
		return out, err
	}
	v := root["schema-version"]
	if v.Kind != yaml.ScalarNode || v.Tag != "!!int" || v.Value != "1" {
		return out, fmt.Errorf("archive schema-version must be 1")
	}
	a, err := configMapping(root["archive-admission"], "fields", "accepted-reviews")
	if err != nil {
		return out, err
	}
	f, err := configMapping(a["fields"], "review", "evidence", "resolution", "promoted-to", "children")
	if err != nil {
		return out, err
	}
	for _, key := range []string{"review", "evidence", "resolution", "promoted-to", "children"} {
		if f[key].Kind != yaml.ScalarNode || f[key].Tag != "!!str" {
			return out, fmt.Errorf("archive field %s must be a string", key)
		}
	}
	out = Config{SchemaVersion: 1, Admission: AdmissionConfig{Fields: Fields{Review: f["review"].Value, Evidence: f["evidence"].Value, Resolution: f["resolution"].Value, PromotedTo: f["promoted-to"].Value, Children: f["children"].Value}}}
	values := a["accepted-reviews"]
	if values.Kind != yaml.SequenceNode || values.Tag != "!!seq" {
		return Config{}, fmt.Errorf("accepted-reviews must be an array")
	}
	for _, v := range values.Content {
		if v.Kind != yaml.ScalarNode || v.Tag != "!!str" {
			return Config{}, fmt.Errorf("accepted review must be a string")
		}
		out.Admission.AcceptedReviews = append(out.Admission.AcceptedReviews, v.Value)
	}
	if _, err := out.Canonical(); err != nil {
		return Config{}, err
	}
	return out, nil
}

func configMapping(node *yaml.Node, keys ...string) (map[string]*yaml.Node, error) {
	if node.Kind != yaml.MappingNode || node.Tag != "!!map" {
		return nil, fmt.Errorf("archive config requires a mapping")
	}
	allowed := map[string]bool{}
	for _, key := range keys {
		allowed[key] = true
	}
	out := map[string]*yaml.Node{}
	for i := 0; i < len(node.Content); i += 2 {
		key := node.Content[i]
		if key.Kind != yaml.ScalarNode || key.Tag != "!!str" || !allowed[key.Value] || out[key.Value] != nil {
			return nil, fmt.Errorf("unknown or duplicate archive config field %q", key.Value)
		}
		out[key.Value] = node.Content[i+1]
	}
	for _, key := range keys {
		if out[key] == nil {
			return nil, fmt.Errorf("missing archive config field %s", key)
		}
	}
	return out, nil
}

func (c Config) Validate() error {
	if c.SchemaVersion != 1 {
		return fmt.Errorf("archive schema-version must be 1")
	}
	f := c.Admission.Fields
	for _, value := range []string{f.Review, f.Evidence, f.Resolution, f.PromotedTo, f.Children} {
		if value == "" || !utf8.ValidString(value) {
			return fmt.Errorf("archive field mappings must be explicit UTF-8 names")
		}
	}
	doc, err := card.Parse([]byte("---\n{}\n---\n"))
	if err != nil {
		return err
	}
	if _, err := doc.ProjectArchiveMetadata(f.Mapping()); err != nil {
		return err
	}
	_, err = EvaluateNormal(Rules{AcceptedReviews: c.Admission.AcceptedReviews}, Facts{ID: "TASK-0", Kind: "task"}, nil)
	return err
}

// Canonical normalizes only the rule set, never a card. Review values form a
// case-insensitive set; ordering and spelling case cannot change its digest.
func (c Config) Canonical() ([]byte, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	c.Admission.AcceptedReviews = append([]string(nil), c.Admission.AcceptedReviews...)
	for i, v := range c.Admission.AcceptedReviews {
		c.Admission.AcceptedReviews[i] = strings.ToLower(v)
	}
	sort.Strings(c.Admission.AcceptedReviews)
	raw, err := json.Marshal(c)
	if err != nil {
		return nil, err
	}
	if len(raw) > ConfigLimit {
		return nil, fmt.Errorf("canonical archive config exceeds size limit")
	}
	return raw, nil
}

func (c Config) Digest() (string, error) {
	raw, err := c.Canonical()
	if err != nil {
		return "", err
	}
	h := sha256.Sum256(raw)
	return hex.EncodeToString(h[:]), nil
}
