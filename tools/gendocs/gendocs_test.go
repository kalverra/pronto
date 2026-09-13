package gendocs_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalverra/pronto/tools/gendocs"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	abs, err := filepath.Abs("../..")
	require.NoError(t, err)
	_, err = os.Stat(filepath.Join(abs, "go.mod"))
	require.NoError(t, err, "expected repo root at %s", abs)
	return abs
}

func TestGenerate_WritesAllDocs(t *testing.T) {
	t.Parallel()

	out := t.TempDir()
	require.NoError(t, gendocs.Generate(repoRoot(t), out))

	docs := map[string][]byte{}
	for _, name := range gendocs.Files {
		data, err := os.ReadFile(filepath.Join(out, name)) // #nosec G304 — generated doc names in TempDir.
		require.NoError(t, err, "Generate should write %s", name)
		docs[name] = data
		assert.Contains(t, string(data), "Code generated", "%s must carry the generated header", name)
	}

	assert.Contains(t, string(docs["events.md"]), "ci_passed")
	assert.Contains(t, string(docs["events.md"]), "queue_refreshed")
	assert.Contains(t, string(docs["events.md"]), "ReviewPayload")
	assert.Contains(t, string(docs["events.md"]), "Subscription")

	assert.Contains(t, string(docs["config.md"]), "pr_view")
	assert.Contains(t, string(docs["config.md"]), "PRONTO_PR_VIEW")
	assert.Contains(t, string(docs["config.md"]), "notifications.popups")

	assert.Contains(t, string(docs["model.md"]), "PullRequest")
	assert.Contains(t, string(docs["model.md"]), "repo_name_with_owner")
}

func TestGenerate_Deterministic(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	out1, out2 := t.TempDir(), t.TempDir()
	require.NoError(t, gendocs.Generate(root, out1))
	require.NoError(t, gendocs.Generate(root, out2))

	for _, name := range gendocs.Files {
		first, err := os.ReadFile(filepath.Join(out1, name)) // #nosec G304 — generated doc names in TempDir.
		require.NoError(t, err)
		second, err := os.ReadFile(filepath.Join(out2, name)) // #nosec G304 — generated doc names in TempDir.
		require.NoError(t, err)
		assert.Equal(t, string(first), string(second), "%s must be byte-identical across runs", name)
	}
}

func TestCommittedDocs_NotStale(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	out := t.TempDir()
	require.NoError(t, gendocs.Generate(root, out))

	for _, name := range gendocs.Files {
		generated, err := os.ReadFile(filepath.Join(out, name)) // #nosec G304 — generated doc names in TempDir.
		require.NoError(t, err)

		committed, err := os.ReadFile(
			filepath.Join(root, "docs", name),
		) // #nosec G304 — committed doc paths under the repo.
		if os.IsNotExist(err) {
			t.Fatalf("docs/%s is missing; run `mise run generate` to generate it", name)
		}
		require.NoError(t, err)
		assert.Equal(t, string(generated), string(committed),
			"docs/%s is stale; run `mise run generate` and commit the result", name)
	}
}
