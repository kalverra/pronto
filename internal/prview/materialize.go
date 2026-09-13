// Package prview provides pull request viewer implementations and diff materialization.
package prview

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/cli/go-gh/v2/pkg/api"

	"github.com/kalverra/pronto/internal/model"
)

// MaxDiffFiles is the maximum number of changed files permitted for materialization.
const MaxDiffFiles = 50

// MaterializedDiff represents the base and head trees materialized into
// stable cache directories, reused across views of the same PR.
type MaterializedDiff struct {
	BaseDir string
	HeadDir string
	Files   []string
}

// Materializer fetches changed files for a PR and extracts them into local temp directories.
type Materializer interface {
	Materialize(ctx context.Context, pr model.PullRequest) (*MaterializedDiff, error)
}

// HTTPMaterializer implements Materializer using GitHub HTTP REST API calls.
type HTTPMaterializer struct {
	client  *http.Client
	baseURL string
}

// MaterializerOption configures HTTPMaterializer.
type MaterializerOption func(*HTTPMaterializer)

// WithHTTPClient sets a custom http.Client for HTTPMaterializer.
func WithHTTPClient(client *http.Client) MaterializerOption {
	return func(m *HTTPMaterializer) {
		m.client = client
	}
}

// WithBaseURL sets a custom base API URL (e.g. for httptest.Server).
func WithBaseURL(rawURL string) MaterializerOption {
	return func(m *HTTPMaterializer) {
		m.baseURL = strings.TrimRight(rawURL, "/")
	}
}

func defaultHTTPClient() *http.Client {
	if c, err := api.DefaultHTTPClient(); err == nil && c != nil {
		return c
	}
	return http.DefaultClient
}

// NewMaterializer creates an HTTPMaterializer.
func NewMaterializer(opts ...MaterializerOption) *HTTPMaterializer {
	m := &HTTPMaterializer{
		baseURL: "https://api.github.com",
	}
	for _, opt := range opts {
		opt(m)
	}
	if m.client == nil {
		m.client = defaultHTTPClient()
	}
	return m
}

type compareResponse struct {
	MergeBaseCommit struct {
		SHA string `json:"sha"`
	} `json:"merge_base_commit"`
	Files []struct {
		Filename string `json:"filename"`
		Status   string `json:"status"`
	} `json:"files"`
}

// Materialize downloads base and head tarballs and extracts only changed files into temp directories.
func (m *HTTPMaterializer) Materialize(ctx context.Context, pr model.PullRequest) (*MaterializedDiff, error) {
	owner := pr.RepoOwner
	repo := pr.RepoName
	if (owner == "" || repo == "") && pr.RepoNameWithOwner != "" {
		parts := strings.SplitN(pr.RepoNameWithOwner, "/", 2)
		if len(parts) == 2 {
			owner = parts[0]
			repo = parts[1]
		}
	}
	if owner == "" || repo == "" {
		return nil, errors.New("cannot materialize diff: missing repository info")
	}

	baseRef := pr.BaseRefName
	if baseRef == "" {
		baseRef = "main"
	}
	headOID := pr.HeadRefOID
	if headOID == "" {
		return nil, errors.New("cannot materialize diff: missing head ref OID")
	}

	compareURL := fmt.Sprintf("%s/repos/%s/%s/compare/%s...%s", m.baseURL, owner, repo, baseRef, headOID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, compareURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create compare request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github.v3+json")

	resp, err := m.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("execute compare request: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("compare API returned %s: %s", resp.Status, string(body))
	}

	var comp compareResponse
	if err := json.NewDecoder(resp.Body).Decode(&comp); err != nil {
		return nil, fmt.Errorf("decode compare response: %w", err)
	}

	if len(comp.Files) > MaxDiffFiles {
		return nil, fmt.Errorf("PR has %d changed files (exceeds cap of %d)", len(comp.Files), MaxDiffFiles)
	}

	changedFiles := make([]string, 0, len(comp.Files))
	changedSet := make(map[string]bool, len(comp.Files))
	for _, f := range comp.Files {
		if f.Filename != "" {
			changedFiles = append(changedFiles, f.Filename)
			changedSet[f.Filename] = true
		}
	}
	if len(changedFiles) == 0 {
		return nil, errors.New("cannot materialize diff: PR has no changed files")
	}

	baseSHA := comp.MergeBaseCommit.SHA
	if baseSHA == "" {
		baseSHA = baseRef
	}

	// Stable cache location so tools that read files lazily after their CLI
	// exits (e.g. zed RPCing into a running instance) always find the files.
	root := diffDir(pr)
	if err := os.RemoveAll(root); err != nil {
		return nil, fmt.Errorf("reset diff dir: %w", err)
	}
	baseDir := filepath.Join(root, "base")
	headDir := filepath.Join(root, "head")
	if err := os.MkdirAll(baseDir, 0o700); err != nil {
		return nil, fmt.Errorf("create base dir: %w", err)
	}
	if err := os.MkdirAll(headDir, 0o700); err != nil {
		return nil, fmt.Errorf("create head dir: %w", err)
	}

	// Stream base tarball
	if err := m.extractTarball(ctx, owner, repo, baseSHA, baseDir, changedSet); err != nil {
		return nil, fmt.Errorf("extract base tarball: %w", err)
	}

	// Stream head tarball
	if err := m.extractTarball(ctx, owner, repo, headOID, headDir, changedSet); err != nil {
		return nil, fmt.Errorf("extract head tarball: %w", err)
	}

	// Create empty counterpart files for added or deleted files
	for _, file := range changedFiles {
		baseFile := filepath.Join(baseDir, file)
		if _, err := os.Stat(baseFile); errors.Is(err, os.ErrNotExist) {
			if err := ensureEmptyFile(baseFile); err != nil {
				return nil, fmt.Errorf("create base empty counterpart %q: %w", file, err)
			}
		}
		headFile := filepath.Join(headDir, file)
		if _, err := os.Stat(headFile); errors.Is(err, os.ErrNotExist) {
			if err := ensureEmptyFile(headFile); err != nil {
				return nil, fmt.Errorf("create head empty counterpart %q: %w", file, err)
			}
		}
	}

	return &MaterializedDiff{
		BaseDir: baseDir,
		HeadDir: headDir,
		Files:   changedFiles,
	}, nil
}

// diffDir returns the stable cache directory for a PR's materialized trees,
// e.g. <cache>/pronto/diffs/org-repo-101. Re-views of the same PR overwrite it.
func diffDir(pr model.PullRequest) string {
	repo := pr.RepoNameWithOwner
	if repo == "" {
		repo = "pr"
	}
	name := fmt.Sprintf("%s-%d", strings.ReplaceAll(repo, "/", "-"), pr.Number)
	return filepath.Join(cacheRoot(), "diffs", name)
}

// cacheRoot returns the root cache directory for materialized artifacts.
func cacheRoot() string {
	if dir := os.Getenv("PRONTO_CACHE_DIR"); dir != "" {
		return dir
	}
	base, err := os.UserCacheDir()
	if err != nil || base == "" {
		return os.TempDir()
	}
	return filepath.Join(base, "pronto")
}

func (m *HTTPMaterializer) extractTarball(
	ctx context.Context,
	owner, repo, sha, destDir string,
	filter map[string]bool,
) error {
	url := fmt.Sprintf("%s/repos/%s/%s/tarball/%s", m.baseURL, owner, repo, sha)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/x-gzip, application/vnd.github.v3+json")

	resp, err := m.client.Do(req)
	if err != nil {
		return err
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("tarball download returned %s: %s", resp.Status, string(body))
	}

	return extractTarGz(resp.Body, destDir, filter)
}

func extractTarGz(r io.Reader, destDir string, filter map[string]bool) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("open gzip reader: %w", err)
	}
	defer func() {
		_ = gz.Close()
	}()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("read tar archive: %w", err)
		}

		if hdr.Typeflag != tar.TypeReg && hdr.Typeflag != 0 {
			continue
		}

		relPath := stripTopLevel(hdr.Name)
		if relPath == "" || !filter[relPath] {
			continue
		}

		destPath := filepath.Join(destDir, relPath)
		cleanDest := filepath.Clean(destPath)
		cleanDestDir := filepath.Clean(destDir)
		if !strings.HasPrefix(cleanDest, cleanDestDir+string(filepath.Separator)) && cleanDest != cleanDestDir {
			return fmt.Errorf("invalid path traversal in archive: %q", hdr.Name)
		}

		if err := os.MkdirAll(filepath.Dir(destPath), 0o700); err != nil {
			return fmt.Errorf("create parent dir for %q: %w", destPath, err)
		}

		// #nosec G304 -- destPath is verified to stay within destDir.
		f, err := os.OpenFile(destPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
		if err != nil {
			return fmt.Errorf("create file %q: %w", destPath, err)
		}

		// #nosec G110 -- archive size is bounded by selective file extraction.
		if _, err := io.Copy(f, tr); err != nil {
			_ = f.Close()
			return fmt.Errorf("write file %q: %w", destPath, err)
		}
		if err := f.Close(); err != nil {
			return fmt.Errorf("close file %q: %w", destPath, err)
		}
	}

	return nil
}

func stripTopLevel(name string) string {
	name = strings.TrimPrefix(name, "/")
	idx := strings.IndexByte(name, '/')
	if idx < 0 {
		return ""
	}
	return name[idx+1:]
}

func ensureEmptyFile(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	// #nosec G304 -- path is constructed internally in temp dir.
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	return f.Close()
}
