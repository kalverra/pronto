package model_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/kalverra/pronto/internal/model"
)

func TestPRKey_String(t *testing.T) {
	t.Parallel()

	k := model.PRKey{Repo: "kalverra/pronto", Number: 42}
	assert.Equal(t, "kalverra/pronto#42", k.String())
}

func TestPullRequest_Key(t *testing.T) {
	t.Parallel()

	pr := model.PullRequest{
		RepoNameWithOwner: "kalverra/pronto",
		Number:            101,
	}
	assert.Equal(t, model.PRKey{Repo: "kalverra/pronto", Number: 101}, pr.Key())
	assert.Equal(t, "kalverra/pronto#101", pr.Key().String())
}
