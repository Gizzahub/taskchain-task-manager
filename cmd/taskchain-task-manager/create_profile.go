package main

import (
	"flag"

	"github.com/Gizzahub/taskchain-task-manager/internal/card"
	"github.com/Gizzahub/taskchain-task-manager/internal/taskstore"
)

type createProfileFlags struct {
	config, typ, priority, summary *string
	criteria                       repeatedString
}

func (p *createProfileFlags) register(flags *flag.FlagSet) {
	p.config = flags.String("config", "", "explicit card validation rules for this creation only")
	p.typ = flags.String("type", "", "work-card type (requires --config)")
	p.priority = flags.String("priority", "", "work-card priority (requires --config)")
	p.summary = flags.String("summary", "", "single-line summary (requires --config)")
	flags.Var(&p.criteria, "criterion", "single-line acceptance criterion (repeatable, requires --config)")
}

func (p createProfileFlags) mentioned(flags *flag.FlagSet) bool {
	used := false
	flags.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "config", "type", "priority", "summary", "criterion":
			used = true
		}
	})
	return used
}

func (p createProfileFlags) load() (*taskstore.CreateTemplate, error) {
	if *p.config == "" {
		return nil, nil
	}
	raw, err := readValidationInput(*p.config, 64<<10)
	if err != nil {
		return nil, err
	}
	rules, err := card.ParseValidationConfig(raw)
	if err != nil {
		return nil, err
	}
	return &taskstore.CreateTemplate{Rules: rules, Type: *p.typ, Priority: *p.priority, Summary: *p.summary, Criteria: append([]string(nil), p.criteria...)}, nil
}
