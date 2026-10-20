package roleprotocol

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// maxJSONDepth bounds recursion; the deepest closed schema (role → adoption →
// original → claims) needs five levels.
const maxJSONDepth = 16

type jsonKind uint8

const (
	jsonNull jsonKind = iota
	jsonBool
	jsonNumber
	jsonString
	jsonArray
	jsonObject
)

// node is one strictly decoded JSON value. Numbers keep their literal so a
// schema decides which spellings it accepts; objects keep key order so a
// schema that fixes the order can enforce it.
type node struct {
	kind  jsonKind
	b     bool
	s     string
	items []*node
	keys  []string
	obj   map[string]*node
	// size is the decoded spelling's byte length, so an embedded document's
	// own limit applies to its outer JSON too; built nodes leave it zero.
	size int
}

// parseJSON accepts exactly one JSON value of at most limit bytes. It rejects
// invalid UTF-8, unpaired surrogate escapes, duplicate object keys (compared
// after unescaping) and any non-whitespace after the value.
func parseJSON(raw []byte, limit int, what string) (*node, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("%s is empty", what)
	}
	if len(raw) > limit {
		return nil, fmt.Errorf("%s exceeds %d bytes", what, limit)
	}
	if !utf8.Valid(raw) {
		return nil, fmt.Errorf("%s is not valid UTF-8", what)
	}
	p := jsonParser{raw: raw}
	value, err := p.value(0)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", what, err)
	}
	p.space()
	if p.pos != len(raw) {
		return nil, fmt.Errorf("%s: trailing data at byte %d", what, p.pos)
	}
	return value, nil
}

type jsonParser struct {
	raw []byte
	pos int
}

func (p *jsonParser) space() {
	for p.pos < len(p.raw) {
		switch p.raw[p.pos] {
		case ' ', '\t', '\n', '\r':
			p.pos++
		default:
			return
		}
	}
}

func (p *jsonParser) value(depth int) (*node, error) {
	if depth > maxJSONDepth {
		return nil, errors.New("JSON nesting too deep")
	}
	p.space()
	start := p.pos
	n, err := p.scalarOrComposite(depth)
	if err != nil {
		return nil, err
	}
	n.size = p.pos - start
	return n, nil
}

func (p *jsonParser) scalarOrComposite(depth int) (*node, error) {
	if p.pos >= len(p.raw) {
		return nil, errors.New("unexpected end of JSON")
	}
	switch c := p.raw[p.pos]; {
	case c == '{':
		return p.object(depth)
	case c == '[':
		return p.array(depth)
	case c == '"':
		s, err := p.str()
		if err != nil {
			return nil, err
		}
		return &node{kind: jsonString, s: s}, nil
	case c == 't':
		return p.literal("true", &node{kind: jsonBool, b: true})
	case c == 'f':
		return p.literal("false", &node{kind: jsonBool})
	case c == 'n':
		return p.literal("null", &node{kind: jsonNull})
	case c == '-' || (c >= '0' && c <= '9'):
		return p.number()
	default:
		return nil, fmt.Errorf("unexpected byte %q at %d", c, p.pos)
	}
}

func (p *jsonParser) literal(word string, out *node) (*node, error) {
	if !strings.HasPrefix(string(p.raw[p.pos:]), word) {
		return nil, fmt.Errorf("invalid literal at byte %d", p.pos)
	}
	p.pos += len(word)
	return out, nil
}

func (p *jsonParser) object(depth int) (*node, error) {
	p.pos++
	out := &node{kind: jsonObject, obj: map[string]*node{}}
	p.space()
	if p.pos < len(p.raw) && p.raw[p.pos] == '}' {
		p.pos++
		return out, nil
	}
	for {
		p.space()
		if p.pos >= len(p.raw) || p.raw[p.pos] != '"' {
			return nil, fmt.Errorf("object key must be a string at byte %d", p.pos)
		}
		key, err := p.str()
		if err != nil {
			return nil, err
		}
		if _, dup := out.obj[key]; dup {
			return nil, fmt.Errorf("duplicate key %q", key)
		}
		p.space()
		if p.pos >= len(p.raw) || p.raw[p.pos] != ':' {
			return nil, fmt.Errorf("expected ':' at byte %d", p.pos)
		}
		p.pos++
		value, err := p.value(depth + 1)
		if err != nil {
			return nil, err
		}
		out.keys = append(out.keys, key)
		out.obj[key] = value
		p.space()
		if p.pos >= len(p.raw) {
			return nil, errors.New("unterminated object")
		}
		switch p.raw[p.pos] {
		case ',':
			p.pos++
		case '}':
			p.pos++
			return out, nil
		default:
			return nil, fmt.Errorf("expected ',' or '}' at byte %d", p.pos)
		}
	}
}

func (p *jsonParser) array(depth int) (*node, error) {
	p.pos++
	out := &node{kind: jsonArray}
	p.space()
	if p.pos < len(p.raw) && p.raw[p.pos] == ']' {
		p.pos++
		return out, nil
	}
	for {
		value, err := p.value(depth + 1)
		if err != nil {
			return nil, err
		}
		out.items = append(out.items, value)
		p.space()
		if p.pos >= len(p.raw) {
			return nil, errors.New("unterminated array")
		}
		switch p.raw[p.pos] {
		case ',':
			p.pos++
		case ']':
			p.pos++
			return out, nil
		default:
			return nil, fmt.Errorf("expected ',' or ']' at byte %d", p.pos)
		}
	}
}

// number accepts the RFC 8259 grammar and keeps the literal; schemas narrow it.
func (p *jsonParser) number() (*node, error) {
	start := p.pos
	digits := func() int {
		n := 0
		for p.pos < len(p.raw) && p.raw[p.pos] >= '0' && p.raw[p.pos] <= '9' {
			p.pos++
			n++
		}
		return n
	}
	if p.raw[p.pos] == '-' {
		p.pos++
	}
	if p.pos < len(p.raw) && p.raw[p.pos] == '0' {
		p.pos++
	} else if digits() == 0 {
		return nil, fmt.Errorf("invalid number at byte %d", start)
	}
	if p.pos < len(p.raw) && p.raw[p.pos] == '.' {
		p.pos++
		if digits() == 0 {
			return nil, fmt.Errorf("invalid number at byte %d", start)
		}
	}
	if p.pos < len(p.raw) && (p.raw[p.pos] == 'e' || p.raw[p.pos] == 'E') {
		p.pos++
		if p.pos < len(p.raw) && (p.raw[p.pos] == '+' || p.raw[p.pos] == '-') {
			p.pos++
		}
		if digits() == 0 {
			return nil, fmt.Errorf("invalid number at byte %d", start)
		}
	}
	return &node{kind: jsonNumber, s: string(p.raw[start:p.pos])}, nil
}

func (p *jsonParser) str() (string, error) {
	p.pos++
	var b strings.Builder
	for {
		if p.pos >= len(p.raw) {
			return "", errors.New("unterminated string")
		}
		c := p.raw[p.pos]
		switch {
		case c == '"':
			p.pos++
			return b.String(), nil
		case c < 0x20:
			return "", fmt.Errorf("control byte in string at %d", p.pos)
		case c != '\\':
			b.WriteByte(c)
			p.pos++
			continue
		}
		p.pos++
		if p.pos >= len(p.raw) {
			return "", errors.New("unterminated escape")
		}
		e := p.raw[p.pos]
		p.pos++
		switch e {
		case '"', '\\', '/':
			b.WriteByte(e)
		case 'b':
			b.WriteByte('\b')
		case 'f':
			b.WriteByte('\f')
		case 'n':
			b.WriteByte('\n')
		case 'r':
			b.WriteByte('\r')
		case 't':
			b.WriteByte('\t')
		case 'u':
			r, err := p.hex4()
			if err != nil {
				return "", err
			}
			if utf16.IsSurrogate(r) {
				if r >= 0xdc00 || p.pos+1 >= len(p.raw) || p.raw[p.pos] != '\\' || p.raw[p.pos+1] != 'u' {
					return "", errors.New("unpaired surrogate escape")
				}
				p.pos += 2
				low, err := p.hex4()
				if err != nil {
					return "", err
				}
				if r = utf16.DecodeRune(r, low); r == utf8.RuneError {
					return "", errors.New("unpaired surrogate escape")
				}
			}
			b.WriteRune(r)
		default:
			return "", fmt.Errorf("invalid escape \\%c", e)
		}
	}
}

func (p *jsonParser) hex4() (rune, error) {
	if p.pos+4 > len(p.raw) {
		return 0, errors.New("short unicode escape")
	}
	var r rune
	for _, c := range p.raw[p.pos : p.pos+4] {
		r <<= 4
		switch {
		case c >= '0' && c <= '9':
			r |= rune(c - '0')
		case c >= 'a' && c <= 'f':
			r |= rune(c-'a') + 10
		case c >= 'A' && c <= 'F':
			r |= rune(c-'A') + 10
		default:
			return 0, errors.New("invalid unicode escape")
		}
	}
	p.pos += 4
	return r, nil
}
