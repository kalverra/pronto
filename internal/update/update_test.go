package update_test

import (
	"context"
	"encoding/json"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalverra/pronto/internal/update"
)

// fakeRESTClient stands in for *api.RESTClient, round-tripping a canned
// release payload through JSON so it works regardless of the response type's
// (unexported) shape.
type fakeRESTClient struct {
	calls    int
	lastPath string
	tagName  string
	htmlURL  string
	err      error
}

func (f *fakeRESTClient) DoWithContext(_ context.Context, method, path string, _ io.Reader, response any) error {
	f.calls++
	f.lastPath = path
	if method != "GET" {
		return assert.AnError
	}
	if f.err != nil {
		return f.err
	}
	payload, err := json.Marshal(map[string]string{
		"tag_name": f.tagName,
		"html_url": f.htmlURL,
	})
	if err != nil {
		return err
	}
	return json.Unmarshal(payload, response)
}

func TestCheck_NewerReleaseAvailable(t *testing.T) {
	t.Parallel()

	client := &fakeRESTClient{tagName: "v0.2.0", htmlURL: "https://github.com/kalverra/pronto/releases/tag/v0.2.0"}
	info, err := update.Check(context.Background(), client, "0.1.0")
	require.NoError(t, err)
	assert.True(t, info.Available)
	assert.Equal(t, "v0.2.0", info.Latest)
	assert.Equal(t, "https://github.com/kalverra/pronto/releases/tag/v0.2.0", info.URL)
	assert.Equal(t, "repos/kalverra/pronto/releases/latest", client.lastPath)
}

func TestCheck_UpToDate(t *testing.T) {
	t.Parallel()

	client := &fakeRESTClient{tagName: "v0.1.0"}
	info, err := update.Check(context.Background(), client, "v0.1.0")
	require.NoError(t, err)
	assert.False(t, info.Available)
}

func TestCheck_RunningNewerThanLatest(t *testing.T) {
	t.Parallel()

	client := &fakeRESTClient{tagName: "v0.1.0"}
	info, err := update.Check(context.Background(), client, "v0.2.0")
	require.NoError(t, err)
	assert.False(t, info.Available)
}

func TestCheck_MissingVPrefixNormalizes(t *testing.T) {
	t.Parallel()

	client := &fakeRESTClient{tagName: "0.2.0"}
	info, err := update.Check(context.Background(), client, "0.1.0")
	require.NoError(t, err)
	assert.True(t, info.Available)
}

func TestCheck_NonSemverCurrentSkipsNetworkCall(t *testing.T) {
	t.Parallel()

	client := &fakeRESTClient{tagName: "v0.2.0"}
	info, err := update.Check(context.Background(), client, "dev")
	require.NoError(t, err)
	assert.False(t, info.Available)
	assert.Zero(t, client.calls, "non-semver current version must skip the network call")
}

func TestCheck_ClientErrorPropagates(t *testing.T) {
	t.Parallel()

	client := &fakeRESTClient{err: assert.AnError}
	_, err := update.Check(context.Background(), client, "0.1.0")
	require.Error(t, err)
}

func TestCheck_NonSemverLatestIsIgnored(t *testing.T) {
	t.Parallel()

	client := &fakeRESTClient{tagName: "not-a-version"}
	info, err := update.Check(context.Background(), client, "0.1.0")
	require.NoError(t, err)
	assert.False(t, info.Available)
}
