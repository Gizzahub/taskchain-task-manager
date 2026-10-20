package roleprotocol

import (
	"fmt"
	"strconv"
)

// fields returns the members of a closed object: every listed key must be
// present and no other key may appear.
func fields(n *node, what string, names ...string) (map[string]*node, error) {
	if n == nil || n.kind != jsonObject {
		return nil, fmt.Errorf("%s must be an object", what)
	}
	allowed := make(map[string]bool, len(names))
	for _, name := range names {
		allowed[name] = true
		if _, ok := n.obj[name]; !ok {
			return nil, fmt.Errorf("%s is missing field %q", what, name)
		}
	}
	for _, key := range n.keys {
		if !allowed[key] {
			return nil, fmt.Errorf("%s has unknown field %q", what, key)
		}
	}
	return n.obj, nil
}

func stringValue(n *node, what string) (string, error) {
	if n.kind != jsonString {
		return "", fmt.Errorf("%s must be a string", what)
	}
	return n.s, nil
}

func enumValue(n *node, what string, allowed ...string) (string, error) {
	s, err := stringValue(n, what)
	if err != nil {
		return "", err
	}
	for _, a := range allowed {
		if s == a {
			return s, nil
		}
	}
	return "", fmt.Errorf("%s has unsupported value %q", what, s)
}

func digestValue(n *node, what string) (string, error) {
	s, err := stringValue(n, what)
	if err != nil {
		return "", err
	}
	if !isDigest(s) {
		return "", fmt.Errorf("%s must be lowercase hex64", what)
	}
	return s, nil
}

// nullableDigest distinguishes an explicit null from a digest; absence is
// already refused by fields.
func nullableDigest(n *node, what string) (*string, error) {
	if n.kind == jsonNull {
		return nil, nil
	}
	s, err := digestValue(n, what)
	if err != nil {
		return nil, err
	}
	return &s, nil
}

func oidValue(n *node, what string) (string, error) {
	s, err := stringValue(n, what)
	if err != nil {
		return "", err
	}
	if !isGitOID(s) {
		return "", fmt.Errorf("%s must be a lowercase hex40/64 Git oid", what)
	}
	return s, nil
}

func boolValue(n *node, what string) (bool, error) {
	if n.kind != jsonBool {
		return false, fmt.Errorf("%s must be a boolean", what)
	}
	return n.b, nil
}

func trueValue(n *node, what string) error {
	b, err := boolValue(n, what)
	if err != nil {
		return err
	}
	if !b {
		return fmt.Errorf("%s must be true", what)
	}
	return nil
}

// exactInt accepts only the canonical literal of want.
func exactInt(n *node, what string, want uint32) error {
	if n.kind != jsonNumber {
		return fmt.Errorf("%s must be an integer", what)
	}
	v, err := uint32Literal(n.s)
	if err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}
	if v != want {
		return fmt.Errorf("%s must be %d", what, want)
	}
	return nil
}

func arrayValue(n *node, what string) ([]*node, error) {
	if n.kind != jsonArray {
		return nil, fmt.Errorf("%s must be an array", what)
	}
	return n.items, nil
}

// Builders turn validated typed values back into nodes, so a digest is always
// computed from the same closed shape the decoder accepted.

type member struct {
	key   string
	value *node
}

func objectNode(members ...member) *node {
	n := &node{kind: jsonObject, obj: make(map[string]*node, len(members))}
	for _, m := range members {
		n.keys = append(n.keys, m.key)
		n.obj[m.key] = m.value
	}
	return n
}

func stringNode(s string) *node { return &node{kind: jsonString, s: s} }
func boolNode(b bool) *node     { return &node{kind: jsonBool, b: b} }
func intNode(v uint32) *node    { return &node{kind: jsonNumber, s: strconv.FormatUint(uint64(v), 10)} }
func nullNode() *node           { return &node{kind: jsonNull} }

func nullableNode(s *string) *node {
	if s == nil {
		return nullNode()
	}
	return stringNode(*s)
}

func stringsNode(values []string) *node {
	n := &node{kind: jsonArray}
	for _, v := range values {
		n.items = append(n.items, stringNode(v))
	}
	return n
}
