// Package main provides the entrypoint for the pronto CLI.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalverra/pronto/internal/model"
	"github.com/kalverra/pronto/internal/source"
)

func TestRun_ListJSON(t *testing.T) {
	t.Parallel()

	fixturePath := filepath.Join("..", "..", "testdata", "fixtures", "real_account_prs.json")
	src := source.NewFixtureSource(fixturePath)

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	err := Run(context.Background(), []string{"list", "--json"}, src, &stdout, &stderr)
	require.NoError(t, err)

	var output model.Queue
	err = json.Unmarshal(stdout.Bytes(), &output)
	require.NoError(t, err)

	require.NotEmpty(t, output.Authored)
	assert.Equal(t, 23499, output.Authored[0].Number)
	assert.Equal(t, "smartcontractkit/chainlink", output.Authored[0].RepoNameWithOwner)
	assert.Equal(t, "CI: ✓ 156  ✗ 61", output.Authored[0].Checks.Badge())

	require.NotEmpty(t, output.Inbox)
	assert.Equal(t, 23695, output.Inbox[0].Number)
}

func TestRun_ListWithoutJSONReturnsDistinctError(t *testing.T) {
	t.Parallel()

	fixturePath := filepath.Join("..", "..", "testdata", "fixtures", "real_account_prs.json")
	src := source.NewFixtureSource(fixturePath)

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	err := Run(context.Background(), []string{"list"}, src, &stdout, &stderr)
	require.ErrorIs(t, err, ErrJSONRequired)
	require.NotErrorIs(t, err, ErrUnknownCommand)
}
