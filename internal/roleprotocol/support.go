package roleprotocol

import (
	"encoding/base64"
	"errors"
	"fmt"
	"slices"
	"unicode/utf8"
)

const (
	maxSupportBytes       = 256 << 10
	maxEvidenceEntryBytes = 64 << 10
	maxEvidenceTotalBytes = 128 << 10
	maxEvidenceEntries    = 256
	maxCommandBytes       = 1024
)

// routeEntries is the closed §14.3 verification-path group set, byte sorted.
// The identifiers are path groups, not CLI spellings.
var routeEntries = sortedStrings(
	"store.create", "store.transition", "store.recover", "store.claim", "store.release",
	"store.reserve-ids", "store.bundle", "store.repair", "store.relocate", "store.archive",
	"store.legacy-archive", "store.archive-capacity", "store.context", "store.evidence",
	"task.new", "task.promote", "task.move", "task.archive", "task.resolve", "task.change-id",
	"task.gate", "task.gate-evidence", "runtime.start", "runtime.finish", "runtime.abort",
	"runtime.close", "runtime.settle", "runtime.discard", "runtime.recover", "runtime.maintenance",
	"runtime.reconcile", "runtime.cleanup", "runtime.finalize", "policy.upgrade", "policy.revise",
	"policy.shared-enable", "policy.rejoin")

// v1RefusedEntries are the entries §14.3 unambiguously names as bundle or
// shared paths; v1 support must refuse them. Which further store.* groups
// count as "storage" is not enumerated by the specification.
var v1RefusedEntries = map[string]bool{"store.bundle": true, "policy.shared-enable": true, "policy.rejoin": true}

var supportProfiles = sortedStrings("section-9/none", "section-10/none", "section-9/section-11", "section-10/section-11")

// RouteEntries returns the closed route entry set in byte order.
func RouteEntries() []string { return slices.Clone(routeEntries) }

type Route struct {
	Entry          string
	Decision       string // allow | refuse
	EvidenceSHA256 string
}

// Acceptance records an observation; Command is never executed.
type Acceptance struct {
	Command      string
	ReportSHA256 string
	ReviewSHA256 string
}

// Support is §14.3 support evidence. Verifying its shape and digests binds the
// recorded bytes together; it does not authenticate that the gate or review
// happened. An operator still accepts it explicitly through ack.supportAccepted.
type Support struct {
	SourceCommit   string
	SourceTree     string
	ArtifactSHA256 string
	Profiles       []string
	Routes         []Route
	Acceptance     Acceptance
	Evidence       map[string][]byte // raw SHA-256 → decoded bytes
}

// ParseSupport strictly decodes and fully verifies a support document.
func ParseSupport(raw []byte) (Support, error) {
	n, err := parseJSON(raw, maxSupportBytes, "support")
	if err != nil {
		return Support{}, err
	}
	return decodeSupport(n)
}

func decodeSupport(n *node) (Support, error) {
	if n.size > maxSupportBytes {
		return Support{}, fmt.Errorf("support exceeds %d bytes", maxSupportBytes)
	}
	f, err := fields(n, "support", "version", "protocol", "sourceCommit", "sourceTree", "artifactSHA256",
		"profiles", "routes", "acceptance", "evidence")
	if err != nil {
		return Support{}, err
	}
	if err := exactInt(f["version"], "support.version", 1); err != nil {
		return Support{}, err
	}
	if err := exactInt(f["protocol"], "support.protocol", 1); err != nil {
		return Support{}, err
	}
	var s Support
	if s.SourceCommit, s.SourceTree, s.ArtifactSHA256, err = sourceIdentity(f, "support"); err != nil {
		return Support{}, err
	}
	profiles, err := arrayValue(f["profiles"], "support.profiles")
	if err != nil {
		return Support{}, err
	}
	for _, p := range profiles {
		profile, err := stringValue(p, "support.profiles[]")
		if err != nil {
			return Support{}, err
		}
		s.Profiles = append(s.Profiles, profile)
	}
	routes, err := arrayValue(f["routes"], "support.routes")
	if err != nil {
		return Support{}, err
	}
	for _, r := range routes {
		rf, err := fields(r, "support.routes[]", "entry", "decision", "evidenceSHA256")
		if err != nil {
			return Support{}, err
		}
		var route Route
		if route.Entry, err = stringValue(rf["entry"], "route.entry"); err != nil {
			return Support{}, err
		}
		if route.Decision, err = enumValue(rf["decision"], "route.decision", "allow", "refuse"); err != nil {
			return Support{}, err
		}
		if route.EvidenceSHA256, err = digestValue(rf["evidenceSHA256"], "route.evidenceSHA256"); err != nil {
			return Support{}, err
		}
		s.Routes = append(s.Routes, route)
	}
	af, err := fields(f["acceptance"], "support.acceptance", "command", "reportSHA256", "reviewSHA256")
	if err != nil {
		return Support{}, err
	}
	if s.Acceptance.Command, err = stringValue(af["command"], "acceptance.command"); err != nil {
		return Support{}, err
	}
	if s.Acceptance.ReportSHA256, err = digestValue(af["reportSHA256"], "acceptance.reportSHA256"); err != nil {
		return Support{}, err
	}
	if s.Acceptance.ReviewSHA256, err = digestValue(af["reviewSHA256"], "acceptance.reviewSHA256"); err != nil {
		return Support{}, err
	}
	evidence := f["evidence"]
	if evidence.kind != jsonObject {
		return Support{}, errors.New("support.evidence must be an object")
	}
	s.Evidence = map[string][]byte{}
	for _, key := range evidence.keys {
		what := "support.evidence[" + key + "]"
		value, err := stringValue(evidence.obj[key], what)
		if err != nil {
			return Support{}, err
		}
		if s.Evidence[key], err = decodeBase64(value, maxEvidenceEntryBytes, what); err != nil {
			return Support{}, err
		}
	}
	if err := s.Validate(); err != nil {
		return Support{}, err
	}
	return s, nil
}

func sourceIdentity(f map[string]*node, what string) (commit, tree, artifact string, err error) {
	if commit, err = oidValue(f["sourceCommit"], what+".sourceCommit"); err != nil {
		return
	}
	if tree, err = oidValue(f["sourceTree"], what+".sourceTree"); err != nil {
		return
	}
	artifact, err = digestValue(f["artifactSHA256"], what+".artifactSHA256")
	return
}

// Validate checks the closed support contract, including every evidence
// reference and the exact report, review and route evidence documents.
func (s Support) Validate() error {
	if !isGitOID(s.SourceCommit) || !isGitOID(s.SourceTree) || !isDigest(s.ArtifactSHA256) {
		return errors.New("support source identity is invalid")
	}
	if len(s.Profiles) == 0 {
		return errors.New("support.profiles must not be empty")
	}
	for i, p := range s.Profiles {
		if !slices.Contains(supportProfiles, p) {
			return fmt.Errorf("support profile %q is not a v1 profile", p)
		}
		if i > 0 && s.Profiles[i-1] >= p {
			return errors.New("support.profiles must be byte sorted and unique")
		}
	}
	if len(s.Routes) != len(routeEntries) {
		return fmt.Errorf("support.routes must list all %d entries exactly once", len(routeEntries))
	}
	for i, r := range s.Routes {
		if r.Entry != routeEntries[i] {
			return fmt.Errorf("support.routes[%d] must be %q in byte order, got %q", i, routeEntries[i], r.Entry)
		}
		if r.Decision != "allow" && r.Decision != "refuse" {
			return fmt.Errorf("route %s has unsupported decision %q", r.Entry, r.Decision)
		}
		if v1RefusedEntries[r.Entry] && r.Decision != "refuse" {
			return fmt.Errorf("route %s must be refuse in v1", r.Entry)
		}
		if !isDigest(r.EvidenceSHA256) {
			return fmt.Errorf("route %s evidence digest is invalid", r.Entry)
		}
	}
	a := s.Acceptance
	if a.Command == "" || len(a.Command) > maxCommandBytes || !utf8.ValidString(a.Command) {
		return fmt.Errorf("acceptance.command must be 1..%d bytes of UTF-8", maxCommandBytes)
	}
	if !isDigest(a.ReportSHA256) || !isDigest(a.ReviewSHA256) {
		return errors.New("acceptance digests are invalid")
	}
	if err := s.validateEvidenceMap(); err != nil {
		return err
	}
	return s.validateEvidenceDocuments()
}

func (s Support) validateEvidenceMap() error {
	if len(s.Evidence) > maxEvidenceEntries {
		return fmt.Errorf("support.evidence holds more than %d entries", maxEvidenceEntries)
	}
	total := 0
	for _, key := range sortedKeys(s.Evidence) {
		raw := s.Evidence[key]
		total += len(raw)
		if !isDigest(key) || len(raw) > maxEvidenceEntryBytes || rawDigest(raw) != key {
			return fmt.Errorf("support evidence %s does not match its key or size", key)
		}
	}
	if total > maxEvidenceTotalBytes {
		return fmt.Errorf("support.evidence exceeds %d decoded bytes", maxEvidenceTotalBytes)
	}
	referenced := map[string]bool{s.Acceptance.ReportSHA256: true, s.Acceptance.ReviewSHA256: true}
	for _, r := range s.Routes {
		referenced[r.EvidenceSHA256] = true
	}
	for digest := range referenced {
		if _, ok := s.Evidence[digest]; !ok {
			return fmt.Errorf("support evidence %s is referenced but missing", digest)
		}
	}
	// Unreferenced bytes are bound to nothing the operator accepts; refuse
	// them rather than carry unexplained payload.
	if len(referenced) != len(s.Evidence) {
		return errors.New("support.evidence holds an unreferenced entry")
	}
	return nil
}

// Accepts reports whether the support accepted the selector's profile.
func (s Support) Accepts(sel Selector) bool { return slices.Contains(s.Profiles, sel.Profile()) }

func (s Support) node() *node {
	routes := &node{kind: jsonArray}
	for _, r := range s.Routes {
		routes.items = append(routes.items, objectNode(
			member{"entry", stringNode(r.Entry)},
			member{"decision", stringNode(r.Decision)},
			member{"evidenceSHA256", stringNode(r.EvidenceSHA256)}))
	}
	evidence := &node{kind: jsonObject, obj: map[string]*node{}}
	for _, key := range sortedKeys(s.Evidence) {
		evidence.keys = append(evidence.keys, key)
		evidence.obj[key] = stringNode(base64.StdEncoding.EncodeToString(s.Evidence[key]))
	}
	return objectNode(
		member{"version", intNode(1)},
		member{"protocol", intNode(1)},
		member{"sourceCommit", stringNode(s.SourceCommit)},
		member{"sourceTree", stringNode(s.SourceTree)},
		member{"artifactSHA256", stringNode(s.ArtifactSHA256)},
		member{"profiles", stringsNode(s.Profiles)},
		member{"routes", routes},
		member{"acceptance", objectNode(
			member{"command", stringNode(s.Acceptance.Command)},
			member{"reportSHA256", stringNode(s.Acceptance.ReportSHA256)},
			member{"reviewSHA256", stringNode(s.Acceptance.ReviewSHA256)})},
		member{"evidence", evidence})
}

// Digest is D(task-manager:role-support:v1, E(support)).
func (s Support) Digest() (string, error) {
	if err := s.Validate(); err != nil {
		return "", err
	}
	e, err := encodeE(nil, s.node())
	if err != nil {
		return "", err
	}
	return domainDigest(domainSupport, e), nil
}

func sortedStrings(values ...string) []string {
	slices.Sort(values)
	return values
}
