package prview_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalverra/pronto/internal/model"
	"github.com/kalverra/pronto/internal/prview"
)

func createTarGz(t *testing.T, prefix string, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	for name, content := range files {
		fullName := prefix + "/" + name
		hdr := &tar.Header{
			Name:     fullName,
			Mode:     0o644,
			Size:     int64(len(content)),
			Typeflag: tar.TypeReg,
		}
		require.NoError(t, tw.WriteHeader(hdr))
		_, err := tw.Write([]byte(content))
		require.NoError(t, err)
	}

	require.NoError(t, tw.Close())
	require.NoError(t, gw.Close())
	return buf.Bytes()
}

func TestMaterialize_CompareAndExtract(t *testing.T) {
	t.Setenv("PRONTO_CACHE_DIR", t.TempDir())

	baseTarGz := createTarGz(t, "org-repo-base123", map[string]string{
		"file1.go":     "package main\n// old file1\n",
		"file2.txt":    "old file2 content\n",
		"unchanged.go": "package main\n// unchanged\n",
	})
	headTarGz := createTarGz(t, "org-repo-head456", map[string]string{
		"file1.go":     "package main\n// new file1\n",
		"file2.txt":    "new file2 content\n",
		"unchanged.go": "package main\n// unchanged\n",
	})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/org/repo/compare/main...head456":
			resp := map[string]any{
				"merge_base_commit": map[string]any{"sha": "base123"},
				"files": []map[string]any{
					{"filename": "file1.go", "status": "modified"},
					{"filename": "file2.txt", "status": "modified"},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
		case "/repos/org/repo/tarball/base123":
			w.Header().Set("Content-Type", "application/x-gzip")
			_, _ = w.Write(baseTarGz)
		case "/repos/org/repo/tarball/head456":
			w.Header().Set("Content-Type", "application/x-gzip")
			_, _ = w.Write(headTarGz)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	m := prview.NewMaterializer(
		prview.WithHTTPClient(server.Client()),
		prview.WithBaseURL(server.URL),
	)

	pr := model.PullRequest{
		Number:            101,
		RepoOwner:         "org",
		RepoName:          "repo",
		RepoNameWithOwner: "org/repo",
		BaseRefName:       "main",
		HeadRefOID:        "head456",
	}

	diff, err := m.Materialize(context.Background(), pr)
	require.NoError(t, err)

	// Verify changed files extracted
	baseFile1, err := os.ReadFile(filepath.Join(diff.BaseDir, "file1.go"))
	require.NoError(t, err)
	assert.Equal(t, "package main\n// old file1\n", string(baseFile1))

	headFile1, err := os.ReadFile(filepath.Join(diff.HeadDir, "file1.go"))
	require.NoError(t, err)
	assert.Equal(t, "package main\n// new file1\n", string(headFile1))

	// Verify unchanged files were NOT extracted
	_, err = os.Stat(filepath.Join(diff.BaseDir, "unchanged.go"))
	assert.True(t, os.IsNotExist(err))
	_, err = os.Stat(filepath.Join(diff.HeadDir, "unchanged.go"))
	assert.True(t, os.IsNotExist(err))
}

func TestMaterialize_AddedAndDeletedFiles(t *testing.T) {
	t.Setenv("PRONTO_CACHE_DIR", t.TempDir())

	baseTarGz := createTarGz(t, "org-repo-base123", map[string]string{
		"deleted.go": "deleted content\n",
	})
	headTarGz := createTarGz(t, "org-repo-head456", map[string]string{
		"added.go": "newly added content\n",
	})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/org/repo/compare/main...head456":
			resp := map[string]any{
				"merge_base_commit": map[string]any{"sha": "base123"},
				"files": []map[string]any{
					{"filename": "added.go", "status": "added"},
					{"filename": "deleted.go", "status": "removed"},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
		case "/repos/org/repo/tarball/base123":
			w.Header().Set("Content-Type", "application/x-gzip")
			_, _ = w.Write(baseTarGz)
		case "/repos/org/repo/tarball/head456":
			w.Header().Set("Content-Type", "application/x-gzip")
			_, _ = w.Write(headTarGz)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	m := prview.NewMaterializer(
		prview.WithHTTPClient(server.Client()),
		prview.WithBaseURL(server.URL),
	)

	pr := model.PullRequest{
		Number:            101,
		RepoOwner:         "org",
		RepoName:          "repo",
		RepoNameWithOwner: "org/repo",
		BaseRefName:       "main",
		HeadRefOID:        "head456",
	}

	diff, err := m.Materialize(context.Background(), pr)
	require.NoError(t, err)

	// added.go: base must have empty file, head must have content
	baseAdded, err := os.ReadFile(filepath.Join(diff.BaseDir, "added.go"))
	require.NoError(t, err)
	assert.Empty(t, string(baseAdded))

	headAdded, err := os.ReadFile(filepath.Join(diff.HeadDir, "added.go"))
	require.NoError(t, err)
	assert.Equal(t, "newly added content\n", string(headAdded))

	// deleted.go: base must have content, head must have empty file
	baseDeleted, err := os.ReadFile(filepath.Join(diff.BaseDir, "deleted.go"))
	require.NoError(t, err)
	assert.Equal(t, "deleted content\n", string(baseDeleted))

	headDeleted, err := os.ReadFile(filepath.Join(diff.HeadDir, "deleted.go"))
	require.NoError(t, err)
	assert.Empty(t, string(headDeleted))
}

func TestMaterialize_ExceedsCap(t *testing.T) {
	t.Parallel()

	files := make([]map[string]any, 51)
	for i := range files {
		files[i] = map[string]any{
			"filename": fmt.Sprintf("file%d.go", i),
			"status":   "modified",
		}
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		resp := map[string]any{
			"merge_base_commit": map[string]any{"sha": "base123"},
			"files":             files,
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	m := prview.NewMaterializer(
		prview.WithHTTPClient(server.Client()),
		prview.WithBaseURL(server.URL),
	)

	pr := model.PullRequest{
		Number:            101,
		RepoOwner:         "org",
		RepoName:          "repo",
		RepoNameWithOwner: "org/repo",
		BaseRefName:       "main",
		HeadRefOID:        "head456",
	}

	_, err := m.Materialize(context.Background(), pr)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "51 changed files")
}

func TestMaterialize_NoChangedFiles(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		resp := map[string]any{
			"merge_base_commit": map[string]any{"sha": "base123"},
			"files":             []map[string]any{},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	m := prview.NewMaterializer(
		prview.WithHTTPClient(server.Client()),
		prview.WithBaseURL(server.URL),
	)

	pr := model.PullRequest{
		Number:            101,
		RepoOwner:         "org",
		RepoName:          "repo",
		RepoNameWithOwner: "org/repo",
		BaseRefName:       "main",
		HeadRefOID:        "head456",
	}

	_, err := m.Materialize(context.Background(), pr)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no changed files")
}

func TestMaterialize_StableDiffDir(t *testing.T) {
	cacheDir := t.TempDir()
	t.Setenv("PRONTO_CACHE_DIR", cacheDir)

	baseTarGz := createTarGz(t, "org-repo-base123", map[string]string{
		"file1.go": "package main\n// old\n",
	})
	headTarGz := createTarGz(t, "org-repo-head456", map[string]string{
		"file1.go": "package main\n// new\n",
	})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/org/repo/compare/main...head456":
			resp := map[string]any{
				"merge_base_commit": map[string]any{"sha": "base123"},
				"files": []map[string]any{
					{"filename": "file1.go", "status": "modified"},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
		case "/repos/org/repo/tarball/base123":
			w.Header().Set("Content-Type", "application/x-gzip")
			_, _ = w.Write(baseTarGz)
		case "/repos/org/repo/tarball/head456":
			w.Header().Set("Content-Type", "application/x-gzip")
			_, _ = w.Write(headTarGz)
		}
	}))
	defer server.Close()

	m := prview.NewMaterializer(
		prview.WithHTTPClient(server.Client()),
		prview.WithBaseURL(server.URL),
	)

	pr := model.PullRequest{
		Number:            101,
		RepoOwner:         "org",
		RepoName:          "repo",
		RepoNameWithOwner: "org/repo",
		BaseRefName:       "main",
		HeadRefOID:        "head456",
	}

	diff, err := m.Materialize(context.Background(), pr)
	require.NoError(t, err)

	expectedBase := filepath.Join(cacheDir, "diffs", "org-repo-101", "base")
	expectedHead := filepath.Join(cacheDir, "diffs", "org-repo-101", "head")
	assert.Equal(t, expectedBase, diff.BaseDir)
	assert.Equal(t, expectedHead, diff.HeadDir)

	baseFile1, err := os.ReadFile(filepath.Join(diff.BaseDir, "file1.go"))
	require.NoError(t, err)
	assert.Equal(t, "package main\n// old\n", string(baseFile1))
	headFile1, err := os.ReadFile(filepath.Join(diff.HeadDir, "file1.go"))
	require.NoError(t, err)
	assert.Equal(t, "package main\n// new\n", string(headFile1))

	// Re-materializing the same PR reuses the same stable paths.
	diff2, err := m.Materialize(context.Background(), pr)
	require.NoError(t, err)
	assert.Equal(t, diff.BaseDir, diff2.BaseDir)
	assert.Equal(t, diff.HeadDir, diff2.HeadDir)
}
