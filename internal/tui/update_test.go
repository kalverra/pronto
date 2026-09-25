package tui_test

import (
	"context"
	"encoding/json"
	"io"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalverra/pronto/internal/model"
	"github.com/kalverra/pronto/internal/tui"
	"github.com/kalverra/pronto/internal/update"
)

// fakeUpdateClient stands in for a real GitHub REST client in tests,
// round-tripping a canned payload through JSON since update.Check's response
// type is unexported.
type fakeUpdateClient struct {
	calls   int
	tagName string
	htmlURL string
	err     error
}

func (f *fakeUpdateClient) DoWithContext(_ context.Context, _, _ string, _ io.Reader, response any) error {
	f.calls++
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

// drainUpdateCheck runs cmd, unwrapping tea batches, and returns the first
// UpdateCheckMsg produced.
func drainUpdateCheck(t *testing.T, cmd tea.Cmd) (tui.UpdateCheckMsg, bool) {
	t.Helper()
	pending := []tea.Cmd{cmd}
	for len(pending) > 0 {
		next := pending[0]
		pending = pending[1:]
		if next == nil {
			continue
		}
		msg := next()
		if msg == nil {
			continue
		}
		if u, ok := msg.(tui.UpdateCheckMsg); ok {
			return u, true
		}
		if batch, ok := msg.(tea.BatchMsg); ok {
			pending = append(pending, batch...)
		}
	}
	return tui.UpdateCheckMsg{}, false
}

func TestModel_View_UpdateHintWhenNewerReleaseAvailable(t *testing.T) {
	t.Parallel()

	m := tui.New(model.Queue{}, tui.WithoutNotifications())
	updated, _ := m.Update(tui.UpdateCheckMsg{
		Info: update.Info{
			Available: true,
			Latest:    "v9.9.9",
			URL:       "https://github.com/kalverra/pronto/releases/tag/v9.9.9",
		},
	})
	updated, _ = updated.(tui.Model).Update(tea.WindowSizeMsg{Width: 160, Height: 40})
	assert.Contains(t, updated.(tui.Model).View(), "v9.9.9")
}

func TestModel_View_NoUpdateHintWhenUpToDate(t *testing.T) {
	t.Parallel()

	m := tui.New(model.Queue{}, tui.WithoutNotifications())
	updated, _ := m.Update(tui.UpdateCheckMsg{Info: update.Info{Available: false}})
	updated, _ = updated.(tui.Model).Update(tea.WindowSizeMsg{Width: 160, Height: 40})
	assert.NotContains(t, updated.(tui.Model).View(), "available")
}

func TestModel_Init_ChecksForUpdatesWhenVersionAndClientSet(t *testing.T) {
	t.Parallel()

	client := &fakeUpdateClient{tagName: "v9.9.9", htmlURL: "https://example.com/v9.9.9"}
	m := tui.New(model.Queue{}, tui.WithoutNotifications(), tui.WithVersion("0.1.0"), tui.WithUpdateClient(client))

	msg, ok := drainUpdateCheck(t, m.Init())
	require.True(t, ok, "expected Init to fire an update check")
	assert.True(t, msg.Info.Available)
	assert.Equal(t, "v9.9.9", msg.Info.Latest)
	assert.Equal(t, 1, client.calls)
}

func TestModel_Init_SkipsUpdateCheckForDevBuilds(t *testing.T) {
	t.Parallel()

	client := &fakeUpdateClient{tagName: "v9.9.9"}
	m := tui.New(model.Queue{}, tui.WithoutNotifications(), tui.WithVersion("dev"), tui.WithUpdateClient(client))

	msg, ok := drainUpdateCheck(t, m.Init())
	require.True(t, ok)
	assert.False(t, msg.Info.Available)
	assert.Zero(t, client.calls, "dev builds must not trigger a network call")
}

func TestModel_Init_SkipsUpdateCheckWithoutVersion(t *testing.T) {
	t.Parallel()

	client := &fakeUpdateClient{tagName: "v9.9.9"}
	m := tui.New(model.Queue{}, tui.WithoutNotifications(), tui.WithUpdateClient(client))

	_, ok := drainUpdateCheck(t, m.Init())
	assert.False(t, ok, "no version means no update check")
}
