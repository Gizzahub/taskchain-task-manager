package taskflow

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// errGateUndeclared is the gate-evidence refusal when ce-tasks.yaml does not
// name the command or its log. A ready gate treats the same absence as "no
// log to archive" and stays quiet.
var errGateUndeclared = errors.New("ce-tasks.yaml does not declare gate.name and gate.log")

// gateCommand is the declared evidence command. Name is the block's gate
// field; Log is a repository-relative glob for the newest receipt.
type gateCommand struct {
	Name string
	Log  string
}

func loadGateCommand(root string) (gateCommand, error) {
	raw, err := os.ReadFile(filepath.Join(root, "ce-tasks.yaml"))
	if err != nil {
		if os.IsNotExist(err) {
			return gateCommand{}, errGateUndeclared
		}
		return gateCommand{}, fmt.Errorf("read ce-tasks.yaml: %w", err)
	}
	var top map[string]yaml.Node
	if err := yaml.Unmarshal(raw, &top); err != nil {
		return gateCommand{}, fmt.Errorf("ce-tasks.yaml gate: %w", err)
	}
	node, ok := top["gate"]
	if !ok {
		return gateCommand{}, errGateUndeclared
	}
	encoded, err := yaml.Marshal(&node)
	if err != nil {
		return gateCommand{}, fmt.Errorf("ce-tasks.yaml gate: %w", err)
	}
	var decl struct {
		Name string `yaml:"name"`
		Log  string `yaml:"log"`
	}
	decoder := yaml.NewDecoder(strings.NewReader(string(encoded)))
	decoder.KnownFields(true)
	if err := decoder.Decode(&decl); err != nil {
		return gateCommand{}, fmt.Errorf("ce-tasks.yaml gate: %w", err)
	}
	decl.Name = strings.TrimSpace(decl.Name)
	decl.Log = strings.TrimSpace(decl.Log)
	if decl.Name == "" || decl.Log == "" {
		return gateCommand{}, errors.New("ce-tasks.yaml gate: name and log are required")
	}
	if filepath.IsAbs(decl.Log) || strings.Contains(decl.Log, `\`) || strings.Contains(decl.Log, "..") {
		return gateCommand{}, errors.New("ce-tasks.yaml gate.log must be a relative path without ..")
	}
	return gateCommand{Name: decl.Name, Log: filepath.ToSlash(decl.Log)}, nil
}
