// Package mode decides whether the CLI runs for a human (interactive) or for
// an agent, script or CI job (automation).
package mode

import "strings"

// Mode is the CLI's operating mode.
type Mode int

const (
	// Interactive targets humans: tables, prompts, the TUI.
	Interactive Mode = iota
	// Automation targets agents and scripts: JSON only, never prompts.
	Automation
)

func (m Mode) String() string {
	if m == Automation {
		return "agent"
	}
	return "interactive"
}

// Env is the subset of the environment Resolve looks at.
type Env func(key string) string

// Resolve picks the mode. First match wins:
//  1. --agent flag, or SEVENTHINGS_MODE=agent|interactive
//  2. CI=true
//  3. stdin or stdout is not a terminal
//  4. interactive
func Resolve(agentFlag bool, getenv Env, stdinTTY, stdoutTTY bool) Mode {
	if agentFlag {
		return Automation
	}
	switch strings.ToLower(getenv("SEVENTHINGS_MODE")) {
	case "agent", "automation":
		return Automation
	case "interactive", "human":
		return Interactive
	}
	if ci := strings.ToLower(getenv("CI")); ci == "true" || ci == "1" {
		return Automation
	}
	if !stdinTTY || !stdoutTTY {
		return Automation
	}
	return Interactive
}
