package taskruntime

import (
	"bytes"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

type canonicalLifecycleAuthority struct {
	Version    int    `yaml:"version"`
	Writer     string `yaml:"writer"`
	Generation string `yaml:"generation"`
}

type canonicalAuthorityDeclaration struct {
	Config    Config                      `yaml:",inline"`
	Authority canonicalLifecycleAuthority `yaml:"lifecycle-authority"`
}

var canonicalAuthorityGeneration = regexp.MustCompile(`^[0-9a-f]{32}$`)

// Decode only the staged profile. Parsing arbitrary bytes cannot establish
// provenance, filesystem safety, current ownership or authority.
func decodeCanonicalAuthority(data []byte) (canonicalAuthorityDeclaration, error) {
	var zero canonicalAuthorityDeclaration
	invalid := func(reason string) (canonicalAuthorityDeclaration, error) {
		return zero, fmt.Errorf("%w: %s", errCanonicalInvalid, reason)
	}
	if len(data) == 0 || len(data) > canonicalConfigMaxBytes || !utf8.Valid(data) {
		return invalid("input bound or encoding")
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	var doc yaml.Node
	if err := decoder.Decode(&doc); err != nil || len(doc.Content) != 1 {
		return invalid("YAML document")
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); err != io.EOF {
		return invalid("multiple or invalid trailing documents")
	}
	root := doc.Content[0]
	if err := canonicalYAMLTree(root, 0); err != nil {
		return invalid("YAML structure")
	}
	keys, ok := canonicalMapping(root, "schema-version", "repository-id", "worktree-roots", "integration-provider", "integration-network-policy", "execution-administrators", "lifecycle-authority")
	if !ok || len(keys) < 6 || !canonicalScalar(keys["schema-version"], "!!int", "6") {
		return invalid("unsupported schema or fields")
	}
	for _, key := range []string{"repository-id", "integration-provider", "integration-network-policy"} {
		if n := keys[key]; n == nil || n.Kind != yaml.ScalarNode || n.Tag != "!!str" || n.Value == "" {
			return invalid("required string field")
		}
	}
	authority, ok := canonicalMapping(keys["lifecycle-authority"], "version", "writer", "generation")
	if !ok || len(authority) != 3 || !canonicalScalar(authority["version"], "!!int", "1") {
		return invalid("authority version or fields")
	}
	writer, generation := authority["writer"], authority["generation"]
	if writer == nil || writer.Kind != yaml.ScalarNode || writer.Tag != "!!str" || (writer.Value != "ce" && writer.Value != "task-manager" && writer.Value != "none") {
		return invalid("authority writer")
	}
	if generation == nil || generation.Kind != yaml.ScalarNode || generation.Tag != "!!str" || (generation.Style != yaml.SingleQuotedStyle && generation.Style != yaml.DoubleQuotedStyle) || !canonicalAuthorityGeneration.MatchString(generation.Value) {
		return invalid("authority generation")
	}
	roots := keys["worktree-roots"]
	if roots == nil || roots.Kind != yaml.MappingNode || len(roots.Content) == 0 {
		return invalid("required host roots")
	}
	for i := 0; i < len(roots.Content); i += 2 {
		host, path := roots.Content[i], roots.Content[i+1]
		if host.Value == "" || NormalizeIdentity(host.Value) != host.Value || path.Kind != yaml.ScalarNode || path.Tag != "!!str" || !filepath.IsAbs(path.Value) || filepath.Clean(path.Value) != path.Value || filepath.Dir(path.Value) == path.Value || filepath.Base(path.Value) != keys["repository-id"].Value {
			return invalid("host root syntax")
		}
	}
	var decl canonicalAuthorityDeclaration
	if err := root.Decode(&decl); err != nil {
		return invalid("field types")
	}
	cfg := decl.Config
	if NormalizeIdentity(cfg.RepositoryID) != cfg.RepositoryID || cfg.IntegrationProvider != providerGZGit || cfg.IntegrationNetworkPolicy != NetworkNoFetch {
		return invalid("repository, provider or S1 network policy")
	}
	// Reuse the established exact admin identity rules without changing the
	// live parser's schema support or treating the staged profile as legacy.
	adminKeys := make(map[string]yaml.Node)
	if n := keys["execution-administrators"]; n != nil {
		adminKeys["execution-administrators"] = *n
	}
	cfg.SchemaVersion = 3
	if err := validateExecutionAdministrators(cfg, adminKeys); err != nil {
		return invalid("execution administrators")
	}
	return decl, nil
}

func canonicalScalar(n *yaml.Node, tag, value string) bool {
	return n != nil && n.Kind == yaml.ScalarNode && n.Style == 0 && n.Tag == tag && n.Value == value
}

func canonicalMapping(n *yaml.Node, allowed ...string) (map[string]*yaml.Node, bool) {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil, false
	}
	set := make(map[string]bool, len(allowed))
	for _, key := range allowed {
		set[key] = true
	}
	result := make(map[string]*yaml.Node)
	for i := 0; i < len(n.Content); i += 2 {
		key, value := n.Content[i], n.Content[i+1]
		if !set[key.Value] || value.Tag == "!!null" {
			return nil, false
		}
		result[key.Value] = value
	}
	return result, true
}

func canonicalYAMLTree(n *yaml.Node, depth int) error {
	if depth > 16 || n.Anchor != "" || n.Kind == yaml.AliasNode || n.Tag == "!!merge" {
		return errCanonicalInvalid
	}
	if n.Kind == yaml.MappingNode {
		if len(n.Content)%2 != 0 {
			return errCanonicalInvalid
		}
		seen := make(map[string]bool)
		for i := 0; i < len(n.Content); i += 2 {
			key := n.Content[i]
			if key.Kind != yaml.ScalarNode || key.Tag != "!!str" || seen[key.Value] {
				return errCanonicalInvalid
			}
			seen[key.Value] = true
		}
	}
	for _, child := range n.Content {
		if err := canonicalYAMLTree(child, depth+1); err != nil {
			return err
		}
	}
	return nil
}
