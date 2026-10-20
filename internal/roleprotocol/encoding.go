package roleprotocol

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
)

// Domain names for D(domain, bytes). Policy digests alone stay raw SHA-256.
const (
	domainSelection = "task-manager:role-selection:v1"
	domainBinding   = "task-manager:role-binding:v1"
	domainHistory   = "task-manager:role-history:v1"
	domainSupport   = "task-manager:role-support:v1"
	domainCutover   = "task-manager:role-cutover:v1"
)

// domainDigest is D(domain,bytes) = SHA256(ASCII(domain) || NUL || bytes).
func domainDigest(domain string, raw []byte) string {
	h := sha256.New()
	h.Write([]byte(domain))
	h.Write([]byte{0})
	h.Write(raw)
	return hex.EncodeToString(h.Sum(nil))
}

func rawDigest(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func appendU32(out []byte, n int) []byte {
	return binary.BigEndian.AppendUint32(out, uint32(n))
}

// appendL is L(bytes) = U32(len) || bytes.
func appendL(out, raw []byte) []byte {
	return append(appendU32(out, len(raw)), raw...)
}

// encodeE appends the §14.1 E encoding of a schema-validated value. Object
// keys are sorted by UTF-8 bytes, so the result is independent of JSON key
// order and spelling. Numbers must already be canonical unsigned 32-bit
// integers; anything else is refused rather than coerced.
func encodeE(out []byte, n *node) ([]byte, error) {
	switch n.kind {
	case jsonNull:
		return append(out, 'n'), nil
	case jsonBool:
		if n.b {
			return append(out, 't'), nil
		}
		return append(out, 'f'), nil
	case jsonNumber:
		if _, err := uint32Literal(n.s); err != nil {
			return nil, err
		}
		return appendL(append(out, 'i'), []byte(n.s)), nil
	case jsonString:
		return appendL(append(out, 's'), []byte(n.s)), nil
	case jsonArray:
		out = appendU32(append(out, 'a'), len(n.items))
		for _, item := range n.items {
			var err error
			if out, err = encodeE(out, item); err != nil {
				return nil, err
			}
		}
		return out, nil
	case jsonObject:
		keys := append([]string(nil), n.keys...)
		sort.Strings(keys)
		out = appendU32(append(out, 'o'), len(keys))
		for _, key := range keys {
			out = appendL(append(out, 's'), []byte(key))
			var err error
			if out, err = encodeE(out, n.obj[key]); err != nil {
				return nil, err
			}
		}
		return out, nil
	}
	return nil, errors.New("unsupported value in E encoding")
}

// uint32Literal accepts only 0..2^32-1 written without sign, fraction,
// exponent or leading zero.
func uint32Literal(s string) (uint32, error) {
	if s == "" || (len(s) > 1 && s[0] == '0') {
		return 0, fmt.Errorf("integer %q is not canonical", s)
	}
	for _, c := range []byte(s) {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("integer %q is not a non-negative integer", s)
		}
	}
	v, err := strconv.ParseUint(s, 10, 64)
	if err != nil || v > math.MaxUint32 {
		return 0, fmt.Errorf("integer %q out of range", s)
	}
	return uint32(v), nil
}

func isLowerHex(s string, n int) bool {
	if len(s) != n {
		return false
	}
	for _, c := range []byte(s) {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func isDigest(s string) bool      { return isLowerHex(s, 64) }
func isAuthorityID(s string) bool { return isLowerHex(s, 32) }
func isGitOID(s string) bool      { return isLowerHex(s, 40) || isLowerHex(s, 64) }

// decodeBase64 accepts padded standard base64 whose decode→re-encode is the
// identical string, then applies the decoded byte limit.
func decodeBase64(s string, limit int, what string) ([]byte, error) {
	raw, err := base64.StdEncoding.Strict().DecodeString(s)
	if err != nil || base64.StdEncoding.EncodeToString(raw) != s {
		return nil, fmt.Errorf("%s is not canonical padded base64", what)
	}
	if len(raw) > limit {
		return nil, fmt.Errorf("%s exceeds %d decoded bytes", what, limit)
	}
	return raw, nil
}
