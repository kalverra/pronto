package main

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalverra/pronto/internal/source"
)

func TestNewRootCmd_Structure(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	cmd := NewRootCmd(nil, &stdout, &stderr)

	require.NotNil(t, cmd)
	assert.Equal(t, "pronto", cmd.Use)

	subcommands := make(map[string]bool)
	for _, c := range cmd.Commands() {
		subcommands[c.Name()] = true
	}
	assert.True(t, subcommands["list"], "missing list subcommand")
	assert.True(t, subcommands["why"], "missing why subcommand")
	assert.True(t, subcommands["agent"], "missing agent subcommand")

	listCmd, _, err := cmd.Find([]string{"list"})
	require.NoError(t, err)
	assert.NotNil(t, listCmd.Flags().Lookup("json"))

	whyCmd, _, err := cmd.Find([]string{"why"})
	require.NoError(t, err)
	assert.NotNil(t, whyCmd.Flags().Lookup("json"))

	assert.NotNil(t, cmd.PersistentFlags().Lookup("socket"), "missing persistent --socket flag")
}

func TestRun_RootHelp(t *testing.T) {
	t.Parallel()

	fixturePath := filepath.Join("..", "..", "testdata", "fixtures", "real_account_prs.json")
	src := source.NewFixtureSource(fixturePath)

	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{"--help"}, src, &stdout, &stderr)
	require.NoError(t, err)
	assert.Contains(t, stdout.String(), "list")
	assert.Contains(t, stdout.String(), "why")
}

func TestRun_UnknownCommand(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{"nonexistent-cmd"}, nil, &stdout, &stderr)
	require.ErrorContains(t, err, "unknown command")
}

func TestRun_FailurePrintsLogLocation(t *testing.T) {
	customLog := filepath.Join(t.TempDir(), "failure_test.log")
	t.Setenv("PRONTO_LOG_FILE", customLog)

	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{"nonexistent-cmd"}, nil, &stdout, &stderr)
	require.Error(t, err)
	assert.Contains(t, stderr.String(), "View logs at:")
	assert.Contains(t, stderr.String(), customLog)
}
