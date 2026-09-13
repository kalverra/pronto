package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRun_Agent(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{"agent"}, nil, &stdout, &stderr)
	require.NoError(t, err)

	expected, err := os.ReadFile(filepath.Join("..", "..", "docs", "agent-skill.md"))
	require.NoError(t, err)

	assert.Equal(t, string(expected), stdout.String())
}

func TestNewRootCmd_AgentCommand(t *testing.T) {
	t.Parallel()

	cmd := NewRootCmd(nil, nil, nil)
	agentCmd, _, err := cmd.Find([]string{"agent"})
	require.NoError(t, err)
	require.NotNil(t, agentCmd)
	assert.Equal(t, "agent", agentCmd.Name())
}
