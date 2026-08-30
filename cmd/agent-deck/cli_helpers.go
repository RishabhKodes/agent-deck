package main

import (
	"fmt"
	"strings"

	"github.com/RishabhKodes/agent-deck/internal/session"
)

// envVarFlags implements flag.Value for repeatable --env KEY=VALUE flags.
type envVarFlags map[string]string

func (e *envVarFlags) String() string { return "" }

func (e *envVarFlags) Set(value string) error {
	parts := strings.SplitN(value, "=", 2)
	if len(parts) != 2 || parts[0] == "" {
		return fmt.Errorf("invalid env format %q, expected KEY=VALUE", value)
	}
	if !session.IsValidEnvKey(parts[0]) {
		return fmt.Errorf("invalid environment variable name %q", parts[0])
	}
	(*e)[parts[0]] = parts[1]
	return nil
}

func isYesConfirmation(line string) bool {
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes"
}

func localInstancesOnly(instances []*session.Instance) []*session.Instance {
	visible := make([]*session.Instance, 0, len(instances))
	for _, inst := range instances {
		if inst != nil && !inst.IsSSH() {
			visible = append(visible, inst)
		}
	}
	return visible
}
