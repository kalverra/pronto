// Package pronto provides root-level assets and documentation for pronto.
package pronto

import _ "embed"

// AgentSkill is the verbatim content of docs/agent-skill.md.
//
//go:embed docs/agent-skill.md
var AgentSkill string
