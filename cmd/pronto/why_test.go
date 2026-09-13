package main

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalverra/pronto/internal/score"
	"github.com/kalverra/pronto/internal/source"
)

func TestRun_WhyJSON(t *testing.T) {
	t.Parallel()

	fixturePath := filepath.Join("..", "..", "testdata", "fixtures", "real_account_prs.json")
	src := source.NewFixtureSource(fixturePath)

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	args := []string{"why", "smartcontractkit/chainlink#23499", "--json"}
	err := Run(context.Background(), args, src, &stdout, &stderr)
	require.NoError(t, err)

	var output score.Breakdown
	err = json.Unmarshal(stdout.Bytes(), &output)
	require.NoError(t, err)

	assert.NotEmpty(t, output.Terms)
}

func TestRun_WhyPlain(t *testing.T) {
	t.Parallel()

	fixturePath := filepath.Join("..", "..", "testdata", "fixtures", "real_account_prs.json")
	src := source.NewFixtureSource(fixturePath)

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	args := []string{"why", "smartcontractkit/chainlink#23499"}
	err := Run(context.Background(), args, src, &stdout, &stderr)
	require.NoError(t, err)

	assert.Contains(t, stdout.String(), "Score:")
}

func TestRun_WhyMissingRef(t *testing.T) {
	t.Parallel()

	fixturePath := filepath.Join("..", "..", "testdata", "fixtures", "real_account_prs.json")
	src := source.NewFixtureSource(fixturePath)

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	args := []string{"why"}
	err := Run(context.Background(), args, src, &stdout, &stderr)
	require.ErrorIs(t, err, ErrRefRequired)
}
