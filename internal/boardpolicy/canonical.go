package boardpolicy

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
)

type canonicalRoot struct {
	SchemaVersion int            `json:"schema-version"`
	BoardPolicy   canonicalBlock `json:"board-policy"`
}
type canonicalTransition struct {
	From string   `json:"from"`
	To   []string `json:"to"`
}
type canonicalBlock struct {
	Zones       []string               `json:"zones"`
	ZoneStatus  map[string]string      `json:"zone-status"`
	Transitions []canonicalTransition  `json:"transitions"`
	Relocations *[]canonicalTransition `json:"relocations,omitempty"`
	KindStatus  *map[string]string     `json:"kind-status,omitempty"`
}

func (p Policy) Canonical() ([]byte, error) {
	if p.workflow == nil || p.edges == nil || p.known == nil {
		return nil, errors.New("zero board policy has no canonical form")
	}
	zones := make([]string, 0, len(p.parked))
	for zone := range p.parked {
		zones = append(zones, zone)
	}
	sort.Strings(zones)
	status := map[string]string{}
	for _, zone := range zones {
		if value := p.parked[zone]; value != "" {
			status[zone] = value
		}
	}
	froms := make([]string, 0, len(p.edges))
	for from := range p.edges {
		froms = append(froms, from)
	}
	sort.Strings(froms)
	transitions := make([]canonicalTransition, 0, len(froms))
	for _, from := range froms {
		targets := make([]string, 0, len(p.edges[from]))
		for to := range p.edges[from] {
			targets = append(targets, to)
		}
		sort.Strings(targets)
		transitions = append(transitions, canonicalTransition{From: from, To: targets})
	}
	version := p.schemaVersion
	if version == 0 {
		version = 1
	}
	block := canonicalBlock{Zones: zones, ZoneStatus: status, Transitions: transitions}
	if version == 2 {
		froms = froms[:0]
		for from := range p.relocations {
			froms = append(froms, from)
		}
		sort.Strings(froms)
		rows := []canonicalTransition{}
		for _, from := range froms {
			targets := make([]string, 0, len(p.relocations[from]))
			for to := range p.relocations[from] {
				targets = append(targets, to)
			}
			sort.Strings(targets)
			rows = append(rows, canonicalTransition{From: from, To: targets})
		}
		block.Relocations = &rows
		kindStatus := map[string]string{}
		for kind, value := range p.kindStatus {
			kindStatus[kind] = value
		}
		block.KindStatus = &kindStatus
	}
	raw, err := json.Marshal(canonicalRoot{SchemaVersion: version, BoardPolicy: block})
	if err != nil {
		return nil, err
	}
	if len(raw) > maxPolicyBytes {
		return nil, errors.New("canonical board-policy exceeds 64 KiB")
	}
	return raw, nil
}

func (p Policy) Digest() (string, error) {
	raw, err := p.Canonical()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}
