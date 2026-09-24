// Package tui provides the terminal user interface for pronto.
package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/cli/go-gh/v2"
	"github.com/cli/go-gh/v2/pkg/browser"
	"github.com/rs/zerolog"

	"github.com/kalverra/pronto/internal/cache"
	"github.com/kalverra/pronto/internal/config"
	"github.com/kalverra/pronto/internal/events"
	"github.com/kalverra/pronto/internal/model"
	"github.com/kalverra/pronto/internal/notify"
	"github.com/kalverra/pronto/internal/score"
	"github.com/kalverra/pronto/internal/server"
	"github.com/kalverra/pronto/internal/source"
)

// Tab represents the active pane in the TUI.
type Tab int

const (
	// TabFocus displays focused PRs.
	TabFocus Tab = iota
	// TabMine displays authored PRs.
	TabMine
	// TabInbox displays incoming review-requested PRs.
	TabInbox
)

// Model represents the pronto Bubbletea terminal interface.
type Model struct {
	queue        model.Queue
	viewer       string
	teams        []string
	now          time.Time
	nowInjected  bool
	weights      score.Weights
	index        score.ExpertiseIndex
	width        int
	height       int
	activeTab    Tab
	cursorFocus  int
	cursorMine   int
	cursorInbox  int
	scrollFocus  int
	scrollMine   int
	scrollInbox  int
	modalOpen    bool
	detailsOpen  bool
	focusedKeys  map[model.PRKey]bool
	focusItems   []score.Scored
	inboxItems   []score.Scored
	mineItems    []score.Scored
	spinnerFrame int

	// Refresh machinery; zero-valued for statically constructed models.
	src                   source.Source
	store                 cache.Store
	ctx                   context.Context
	loading               bool
	refreshing            bool
	staleSnapshot         bool
	fetchErr              error
	lastFetch             time.Time
	loadingLoaded         int
	loadingTotal          int
	openURL               func(string) error
	confirmClosePR        *model.PullRequest
	closingPR             *model.PullRequest
	closeErr              error
	closeErrPR            *model.PullRequest
	lastClosedPR          *model.PullRequest
	closePR               ClosePRFunc
	notifier              notify.Notifier
	notifierFactory       NotifierFactory
	detector              *notify.Detector
	notifCfg              *config.NotificationConfig
	notificationsDisabled bool
	detectionDisabled     bool
	logger                zerolog.Logger
	notifications         []notify.Notification
	notificationCursor    int
	prevQueue             model.Queue
	notificationFocused   bool
	hideNotifs            bool
	viewPR                ViewPRFunc
	viewErr               error
	viewErrPR             *model.PullRequest
	eventsCh              <-chan events.Event

	stacksCollapsed bool
	expandedStacks  map[string]bool
	sortStrategy    SortStrategy
}

// Option configures a Model.
type Option func(*Model)

// WithViewer sets the current authenticated user's login.
func WithViewer(viewer string) Option {
	return func(m *Model) {
		m.viewer = viewer
	}
}

// WithTeams sets the viewer's org-qualified teams for scoring attribution.
func WithTeams(teams []string) Option {
	return func(m *Model) {
		m.teams = teams
	}
}

// WithNow sets the reference time for calculations. The injected clock stays
// fixed across refreshes; models without it age against the real clock.
func WithNow(now time.Time) Option {
	return func(m *Model) {
		m.now = now
		m.nowInjected = true
	}
}

// WithWeights sets the scoring weights.
func WithWeights(w score.Weights) Option {
	return func(m *Model) {
		m.weights = w
	}
}

// WithExpertise sets the expertise index.
func WithExpertise(idx score.ExpertiseIndex) Option {
	return func(m *Model) {
		m.index = idx
	}
}

// WithDimensions sets initial terminal dimensions.
func WithDimensions(width, height int) Option {
	return func(m *Model) {
		m.width = width
		m.height = height
	}
}

// WithCollapsedStacks sets whether PR stacks default to collapsed.
func WithCollapsedStacks(collapsed bool) Option {
	return func(m *Model) {
		m.stacksCollapsed = collapsed
	}
}

// OpenURLMsg reports the outcome of opening a URL in a browser.
type OpenURLMsg struct {
	URL string
	Err error
}

// WithOpener sets a custom opener for launching URLs in a browser.
func WithOpener(opener func(string) error) Option {
	return func(m *Model) {
		m.openURL = opener
	}
}

func defaultOpenURL(rawURL string) error {
	return browser.New("", nil, nil).Browse(rawURL)
}

func openURLCmd(opener func(string) error, rawURL string) tea.Cmd {
	return func() tea.Msg {
		return OpenURLMsg{
			URL: rawURL,
			Err: opener(rawURL),
		}
	}
}

// ClosePRFunc defines a function to close a pull request with a comment.
type ClosePRFunc func(ctx context.Context, pr model.PullRequest, comment string) error

// ClosePRMsg reports the outcome of closing a pull request.
type ClosePRMsg struct {
	PR  model.PullRequest
	Err error
}

// WithCloser sets a custom closer function for closing pull requests.
func WithCloser(closer ClosePRFunc) Option {
	return func(m *Model) {
		m.closePR = closer
	}
}

// WithNotifier sets a custom notifier for delivery.
func WithNotifier(n notify.Notifier) Option {
	return func(m *Model) {
		m.notifier = n
	}
}

// WithDetector sets a custom detector for mine PR transitions.
func WithDetector(d *notify.Detector) Option {
	return func(m *Model) {
		m.detector = d
	}
}

// WithNotificationConfig configures notifications from user configuration.
func WithNotificationConfig(cfg config.NotificationConfig) Option {
	return func(m *Model) {
		m.notifCfg = &cfg
	}
}

// WithoutNotifications disables background change detection and desktop notifications.
func WithoutNotifications() Option {
	return func(m *Model) {
		m.notificationsDisabled = true
	}
}

// WithoutChangeDetection disables local change detection while keeping desktop
// delivery. Daemon-backed models use this: the daemon detects transitions and
// emits events, and the TUI is the only component that can reach the desktop.
func WithoutChangeDetection() Option {
	return func(m *Model) {
		m.detectionDisabled = true
	}
}

// WithNotifierFactory overrides how the desktop notifier is built from user
// notification config.
func WithNotifierFactory(factory NotifierFactory) Option {
	return func(m *Model) {
		m.notifierFactory = factory
	}
}

// WithLogger sets the logger used to report notification delivery failures.
func WithLogger(logger zerolog.Logger) Option {
	return func(m *Model) {
		m.logger = logger
	}
}

// WithSortStrategy sets the initial sorting strategy.
func WithSortStrategy(s SortStrategy) Option {
	return func(m *Model) {
		m.sortStrategy = s
	}
}

// NotifierFactory builds the desktop notifier from user notification config.
type NotifierFactory func(config.NotificationConfig) notify.Notifier

// DefaultNotifierFactory builds a notifier for the configured channels. It
// returns nil when popups are disabled, so an empty notifier can never
// masquerade as a working one. Sound has no separate channel: it is a
// property of the popup notifier (notifications.sound gates whether it plays
// a sound alongside the banner it already has to show).
func DefaultNotifierFactory(cfg config.NotificationConfig) notify.Notifier {
	if !cfg.Popups {
		return nil
	}
	return notify.NewNotifier(notify.Options{Mode: cfg.Mode, Sound: cfg.Sound})
}

// notifyAssets builds the per-trigger image and sound overrides from user
// notification config, expanding "~" in image paths (sound values are system
// sound names, never paths).
func notifyAssets(cfg *config.NotificationConfig) notify.Assets {
	if cfg == nil {
		return notify.Assets{}
	}
	assets := notify.Assets{}
	if len(cfg.Images) > 0 {
		assets.Images = make(map[notify.Trigger]string, len(cfg.Images))
		for k, v := range cfg.Images {
			assets.Images[notify.Trigger(k)] = config.ExpandPath(v)
		}
	}
	if len(cfg.Sounds) > 0 {
		assets.Sounds = make(map[notify.Trigger]string, len(cfg.Sounds))
		for k, v := range cfg.Sounds {
			assets.Sounds[notify.Trigger(k)] = v
		}
	}
	return assets
}

// detectorOptions builds detector options from user notification config.
func detectorOptions(cfg *config.NotificationConfig) []notify.DetectorOption {
	return []notify.DetectorOption{
		notify.WithBotFilter(true),
		notify.WithAssets(notifyAssets(cfg)),
	}
}

// WithEvents configures an event channel for the model to listen on.
func WithEvents(ch <-chan events.Event) Option {
	return func(m *Model) {
		m.eventsCh = ch
	}
}

// ViewPRFunc defines a function to view a pull request interactively.
type ViewPRFunc func(pr model.PullRequest) tea.Cmd

// ViewPRMsg reports the outcome of viewing a pull request.
type ViewPRMsg struct {
	PR  model.PullRequest
	Err error
}

// SpinnerTickMsg advances the CI spinner animation frame.
type SpinnerTickMsg struct{}

var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

const spinnerInterval = 200 * time.Millisecond

// SpinnerFrame returns the current spinner frame index.
func (m Model) SpinnerFrame() int {
	return m.spinnerFrame
}

func (m Model) refTime() time.Time {
	if !m.now.IsZero() {
		return m.now
	}
	return time.Now()
}

func (m Model) hasRunningCI() bool {
	for _, it := range m.inboxItems {
		if it.PR.Checks.IsRunning() {
			return true
		}
	}
	for _, it := range m.mineItems {
		if it.PR.Checks.IsRunning() {
			return true
		}
	}
	return false
}

// hasPartialPRs reports whether any visible PR is discovery-only data waiting
// for hydration, whose loading spinner must animate.
func (m Model) hasPartialPRs() bool {
	for _, it := range m.inboxItems {
		if it.PR.Partial {
			return true
		}
	}
	for _, it := range m.mineItems {
		if it.PR.Partial {
			return true
		}
	}
	return false
}

// WithPRViewer sets a custom viewer for interactive PR details inspection.
func WithPRViewer(viewer ViewPRFunc) Option {
	return func(m *Model) {
		m.viewPR = viewer
	}
}

func defaultViewPR(pr model.PullRequest) tea.Cmd {
	target := pr.URL
	if target == "" {
		if pr.RepoNameWithOwner != "" && pr.Number > 0 {
			target = pr.Key().String()
		} else if pr.Number > 0 {
			target = strconv.Itoa(pr.Number)
		}
	}
	if target == "" {
		return func() tea.Msg {
			return ViewPRMsg{
				PR:  pr,
				Err: errors.New("cannot view PR: missing URL and repo info"),
			}
		}
	}
	// #nosec G204 -- target is PR URL or repo#number.
	//nolint:noctx // Interactive terminal pager process managed by Bubbletea ExecProcess.
	c := exec.Command("gh", "pr", "view", target)
	c.Env = ViewPREnv()
	return tea.ExecProcess(c, func(err error) tea.Msg {
		return ViewPRMsg{
			PR:  pr,
			Err: err,
		}
	})
}

// ViewPREnv returns the environment configured for interactive PR viewing,
// forcing an interactive pager (less -R) without the -F (quit-if-one-screen) flag
// so the view stays open until dismissed with 'q'.
func ViewPREnv() []string {
	pager := os.Getenv("PRONTO_PAGER")
	if pager == "" {
		pager = "less -R"
	}
	base := os.Environ()
	env := make([]string, 0, len(base)+2)
	for _, kv := range base {
		if strings.HasPrefix(kv, "GH_PAGER=") || strings.HasPrefix(kv, "PAGER=") {
			continue
		}
		env = append(env, kv)
	}
	return append(env, "GH_PAGER="+pager, "PAGER="+pager)
}

func defaultClosePR(ctx context.Context, pr model.PullRequest, comment string) error {
	target := pr.URL
	if target == "" {
		if pr.RepoNameWithOwner != "" && pr.Number > 0 {
			target = pr.Key().String()
		} else {
			return errors.New("cannot close PR: missing URL and repo info")
		}
	}
	_, stderr, err := gh.ExecContext(ctx, "pr", "close", target, "--comment", comment)
	if err != nil {
		if stderr.Len() > 0 {
			return fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
		}
		return err
	}
	return nil
}

func closePRCmd(ctx context.Context, closer ClosePRFunc, pr model.PullRequest, comment string) tea.Cmd {
	return func() tea.Msg {
		var err error
		if closer != nil {
			err = closer(ctx, pr, comment)
		}
		return ClosePRMsg{
			PR:  pr,
			Err: err,
		}
	}
}

// SaveFocusMsg reports the outcome of saving focus state to cache store.
type SaveFocusMsg struct {
	Err error
}

func saveFocusCmd(ctx context.Context, store cache.Store, keys []model.PRKey) tea.Cmd {
	return func() tea.Msg {
		if store == nil {
			return SaveFocusMsg{}
		}
		if ctx == nil {
			ctx = context.Background()
		}
		err := store.SaveFocus(ctx, keys)
		return SaveFocusMsg{Err: err}
	}
}

// WithActiveTab sets the initial active tab.
func WithActiveTab(tab Tab) Option {
	return func(m *Model) {
		m.activeTab = tab
	}
}

// WithStore sets the cache store for persistence.
func WithStore(store cache.Store) Option {
	return func(m *Model) {
		m.store = store
	}
}

// WithFocusedPRs sets the initially focused PR keys.
func WithFocusedPRs(keys []model.PRKey) Option {
	return func(m *Model) {
		if m.focusedKeys == nil {
			m.focusedKeys = make(map[model.PRKey]bool)
		}
		for _, k := range keys {
			m.focusedKeys[k] = true
		}
	}
}

// New creates an initialized Model with the given queue and options.
func New(q model.Queue, opts ...Option) Model {
	m := Model{
		weights:         score.DefaultWeights(),
		activeTab:       TabFocus,
		width:           80,
		height:          24,
		stacksCollapsed: true,
		expandedStacks:  make(map[string]bool),
		focusedKeys:     make(map[model.PRKey]bool),
		logger:          zerolog.Nop(),
	}

	for _, opt := range opts {
		opt(&m)
	}

	if m.openURL == nil {
		m.openURL = defaultOpenURL
	}
	if m.closePR == nil {
		m.closePR = defaultClosePR
	}
	if m.now.IsZero() {
		m.now = time.Now()
	}
	if m.viewer == "" {
		m.viewer = q.Viewer
	}
	if m.teams == nil {
		m.teams = q.Teams
	}
	if m.notifierFactory == nil {
		m.notifierFactory = DefaultNotifierFactory
	}
	if !m.notificationsDisabled {
		if m.notifier == nil {
			if m.notifCfg != nil {
				m.notifier = m.notifierFactory(*m.notifCfg)
			} else {
				// No config loaded: build the notifier from the same defaults
				// LoadFile would produce (popups on, native with terminal
				// fallback), so a bare Model still notifies.
				m.notifier = m.notifierFactory(config.NotificationConfig{Popups: true, Mode: config.NotifyNative})
			}
		}
		if !m.detectionDisabled {
			if m.detector == nil {
				m.detector = notify.NewDetector(notify.DefaultPRStatusChecker, detectorOptions(m.notifCfg)...)
			}
			m.detector.Seed(q.Authored)
		}
	}
	if m.viewPR == nil {
		m.viewPR = defaultViewPR
	}

	return m.applyQueue(q)
}

// applyQueue adopts a freshly fetched (or snapshot) queue, re-ranking items
// and clamping cursors to the new list bounds.
func (m Model) applyQueue(q model.Queue) Model {
	m.prevQueue = m.queue
	if q.Viewer != "" {
		m.viewer = q.Viewer
	}
	if q.Teams != nil {
		m.teams = q.Teams
	}
	if m.now.IsZero() {
		m.now = time.Now()
	}

	// Ensure merge status computed, then rank descending by score.
	inboxPRs := make([]model.PullRequest, len(q.Inbox))
	for i, pr := range q.Inbox {
		if pr.MergeStatus.Badge() == "" && (pr.Mergeable != "" || pr.MergeStateStatus != "" || pr.IsInMergeQueue) {
			pr.MergeStatus = model.ComputeMergeStatus(pr.Mergeable, pr.MergeStateStatus, pr.IsDraft)
			pr.MergeStatus.IsInMergeQueue = pr.IsInMergeQueue
		}
		inboxPRs[i] = pr
	}
	q.Inbox = inboxPRs
	m.inboxItems = score.Rank(inboxPRs, m.now, m.viewer, m.teams, m.index, m.weights)

	authoredPRs := make([]model.PullRequest, len(q.Authored))
	for i, pr := range q.Authored {
		if pr.MergeStatus.Badge() == "" && (pr.Mergeable != "" || pr.MergeStateStatus != "" || pr.IsInMergeQueue) {
			pr.MergeStatus = model.ComputeMergeStatus(pr.Mergeable, pr.MergeStateStatus, pr.IsDraft)
			pr.MergeStatus.IsInMergeQueue = pr.IsInMergeQueue
		}
		authoredPRs[i] = pr
	}
	q.Authored = authoredPRs
	m.queue = q
	m.mineItems = score.RankMine(authoredPRs, m.now, score.DefaultMineWeights())

	m = m.rebuildFocusItems()

	m = m.clampTabView(TabFocus)
	m = m.clampTabView(TabMine)
	m = m.clampTabView(TabInbox)

	m = m.syncNotificationsWithQueue()
	m = m.pruneStaleNotifications()
	m = m.clampNotificationCursor()

	return m
}

func (m Model) rebuildFocusItems() Model {
	if len(m.inboxItems) == 0 && len(m.mineItems) == 0 {
		if len(m.focusItems) > 0 {
			var filtered []score.Scored
			for _, it := range m.focusItems {
				if m.focusedKeys[it.PR.Key()] {
					filtered = append(filtered, it)
				}
			}
			m.focusItems = filtered
		}
		return m
	}
	var items []score.Scored
	seen := make(map[model.PRKey]bool)
	for _, it := range m.inboxItems {
		k := it.PR.Key()
		if m.focusedKeys[k] && !seen[k] {
			items = append(items, it)
			seen[k] = true
		}
	}
	for _, it := range m.mineItems {
		k := it.PR.Key()
		if m.focusedKeys[k] && !seen[k] {
			items = append(items, it)
			seen[k] = true
		}
	}
	m.focusItems = items
	return m
}

func (m Model) focusedKeysList() []model.PRKey {
	keys := make([]model.PRKey, 0, len(m.focusedKeys))
	for k, focused := range m.focusedKeys {
		if focused {
			keys = append(keys, k)
		}
	}
	slices.SortFunc(keys, func(a, b model.PRKey) int {
		if a.Repo != b.Repo {
			return strings.Compare(a.Repo, b.Repo)
		}
		return a.Number - b.Number
	})
	return keys
}

// Init initializes the Bubbletea model.
func (m Model) Init() tea.Cmd {
	var cmds []tea.Cmd
	if m.eventsCh != nil {
		cmds = append(cmds, WaitForEventCmd(m.eventsCh))
	}
	if m.src != nil && (m.loading || m.staleSnapshot) {
		// Startup fetches run under the cold-start budget: hydrating a large
		// queue from an empty cache cannot fit the refresh-tick deadline.
		cmds = append(cmds, FetchQueueColdCmd(m.ctx, m.src))
	}
	return tea.Batch(cmds...)
}

// Update handles incoming Bubbletea messages and transitions state.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m = m.clampTabView(TabFocus)
		m = m.clampTabView(TabMine)
		m = m.clampTabView(TabInbox)
		return m, nil

	case SpinnerTickMsg:
		if !m.hasRunningCI() && !m.hasPartialPRs() && !m.isRefreshingVisual() && !m.loading {
			return m, nil
		}
		if !m.nowInjected {
			m.now = time.Now()
		}
		m.spinnerFrame++
		return m, nil

	case FetchProgressMsg:
		m.loadingLoaded = msg.Loaded
		m.loadingTotal = msg.Total
		return m, waitForFetchMsg(msg.next)

	case QueueLoadedMsg:
		return m.handleQueueLoaded(msg)

	case EventMsg:
		ev := msg.Event
		var cmds []tea.Cmd
		if m.eventsCh != nil {
			cmds = append(cmds, WaitForEventCmd(m.eventsCh))
		}
		switch ev.Type {
		case events.TypeFetchProgress:
			if p, ok := ev.Payload.(events.FetchProgressPayload); ok {
				m.loadingLoaded = p.Loaded
				m.loadingTotal = p.Total
			}
		case events.TypeQueueRefreshed:
			if m.src != nil && !m.loading && !m.staleSnapshot {
				m.refreshing = true
				cmds = append(cmds, FetchQueueCmd(m.ctx, m.src))
			}
		default:
			if isTriggerEvent(ev.Type) {
				n := notificationFromEvent(ev, m.notifCfg)
				cmds = append(cmds, notifySingleCmd(m.ctx, m.notifier, m.logger, n))
			}
		}
		return m, tea.Batch(cmds...)

	case OpenURLMsg:
		return m, nil

	case ViewPRMsg:
		if msg.Err != nil {
			m.viewErr = msg.Err
			m.viewErrPR = &msg.PR
			return m, nil
		}
		m.viewErr = nil
		m.viewErrPR = nil
		return m, nil

	case ClosePRMsg:
		return m.handleClosePRMsg(msg)

	case SaveFocusMsg:
		if msg.Err != nil {
			m.logger.Warn().Err(msg.Err).Msg("failed to persist focus state")
		}
		return m, nil

	case NotificationMsg:
		for _, n := range msg.Notifications {
			m = m.addNotification(n)
		}
		m.notificationFocused = false
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)
	}

	return m, nil
}

func (m Model) handleClosePRMsg(msg ClosePRMsg) (tea.Model, tea.Cmd) {
	m.closingPR = nil
	if msg.Err != nil {
		m.closeErr = msg.Err
		m.closeErrPR = &msg.PR
		return m, nil
	}
	m.closeErr = nil
	m.closeErrPR = nil
	wasFocused := m.IsFocused(msg.PR.Key())
	m = m.removePR(msg.PR)
	m.lastClosedPR = &msg.PR
	if wasFocused && m.store != nil {
		return m, saveFocusCmd(m.ctx, m.store, m.focusedKeysList())
	}
	return m, nil
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "ctrl+c" {
		return m, tea.Quit
	}

	if m.modalOpen {
		return m.handleScoreModalKey(msg.String())
	}

	if m.detailsOpen {
		return m.handleDetailsKey(msg.String())
	}

	if m.confirmClosePR != nil {
		return m.handleConfirmCloseKey(msg)
	}

	if updated, handled := m.handleNavKey(msg.String()); handled {
		// Navigation acknowledges transient close-status banners.
		updated.lastClosedPR = nil
		updated.closeErr = nil
		updated.closeErrPR = nil
		updated.viewErr = nil
		updated.viewErrPR = nil
		return updated, nil
	}

	switch msg.String() {
	case "enter":
		return m.handleEnterKey()
	case "v":
		return m.handleViewKey()
	case "f":
		return m.handleToggleFocusKey()
	case "q":
		return m.teaQuit()
	case "n":
		if len(m.notifications) == 0 {
			return m, nil
		}
		if m.hideNotifs {
			m.hideNotifs = false
			m.notificationFocused = true
			m = m.clampNotificationCursor()
			return m, nil
		}
		if !m.notificationFocused {
			m.notificationFocused = true
			m = m.clampNotificationCursor()
			return m, nil
		}
		m.notificationFocused = false
		m.hideNotifs = true
		return m, nil
	case "esc":
		if m.notificationFocused {
			m.notificationFocused = false
			return m, nil
		}
	case "tab", "shift+tab", "1", "2", "3":
		return m.handleTabKey(msg.String())
	case "r":
		return m.handleRefreshKey()
	case "o":
		return m.handleOpenKey()
	case "x":
		return m.handleDismissNotificationKey()
	case " ", "e":
		return m.handleToggleStackKey()
	case "E":
		return m.handleToggleAllStacksKey()
	case "s":
		return m.handleCycleSortKey(1)
	case "S":
		return m.handleCycleSortKey(-1)
	case "?":
		if len(m.activeList()) > 0 {
			m.modalOpen = true
		}
		return m, nil
	}

	return m, nil
}

func (m Model) handleCycleSortKey(delta int) (tea.Model, tea.Cmd) {
	if m.IsNotificationFocused() {
		return m, nil
	}
	m.sortStrategy = m.sortStrategy.Next(delta)
	m = m.clampTabView(m.activeTab)
	return m, nil
}

func (m Model) handleToggleFocusKey() (tea.Model, tea.Cmd) {
	if m.IsNotificationFocused() {
		return m, nil
	}
	selected := m.SelectedPR()
	if selected == nil {
		return m, nil
	}
	key := selected.Key()
	newKeys := make(map[model.PRKey]bool, len(m.focusedKeys))
	for k, v := range m.focusedKeys {
		if v {
			newKeys[k] = true
		}
	}
	if newKeys[key] {
		delete(newKeys, key)
	} else {
		newKeys[key] = true
	}
	m.focusedKeys = newKeys
	m = m.rebuildFocusItems()
	m = m.clampTabView(TabFocus)
	m = m.clampTabView(TabMine)
	m = m.clampTabView(TabInbox)

	var cmd tea.Cmd
	if m.store != nil {
		cmd = saveFocusCmd(m.ctx, m.store, m.focusedKeysList())
	}
	return m, cmd
}

func (m Model) handleTabKey(key string) (tea.Model, tea.Cmd) {
	m.notificationFocused = false
	switch key {
	case "1":
		m.activeTab = TabFocus
	case "2":
		m.activeTab = TabMine
	case "3":
		m.activeTab = TabInbox
	case "shift+tab":
		switch m.activeTab {
		case TabFocus:
			m.activeTab = TabInbox
		case TabMine:
			m.activeTab = TabFocus
		case TabInbox:
			m.activeTab = TabMine
		}
	default:
		switch m.activeTab {
		case TabFocus:
			m.activeTab = TabMine
		case TabMine:
			m.activeTab = TabInbox
		case TabInbox:
			m.activeTab = TabFocus
		}
	}
	return m, nil
}

func (m Model) handleRefreshKey() (tea.Model, tea.Cmd) {
	if m.src == nil || m.loading || m.refreshing || m.staleSnapshot {
		return m, nil
	}
	m.refreshing = true
	if refresher, ok := m.src.(interface {
		Refresh(context.Context) (server.Snapshot, error)
	}); ok {
		return m, refreshQueueCmd(m.ctx, refresher)
	}
	if refresher, ok := m.src.(interface{ Refresh(context.Context) error }); ok {
		return m, refreshQueueCmd(m.ctx, refresher)
	}
	return m, FetchQueueCmd(m.ctx, m.src)
}

func (m Model) handleScoreModalKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "q", "?", "esc":
		m.modalOpen = false
	}
	return m, nil
}

func (m Model) handleDetailsKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "esc", "q", "enter":
		m.detailsOpen = false
		return m, nil
	case "v":
		return m.handleViewKey()
	case "o":
		return m.handleOpenKey()
	case "?":
		if len(m.activeList()) > 0 {
			m.modalOpen = true
		}
		return m, nil
	}
	return m, nil
}

func (m Model) handleCloseStaleKey() (tea.Model, tea.Cmd) {
	if m.closingPR != nil {
		return m, nil
	}
	selected := m.SelectedPR()
	if selected == nil || !selected.IsStale(m.now) {
		return m, nil
	}
	prCopy := *selected
	m.confirmClosePR = &prCopy
	m.closeErr = nil
	m.closeErrPR = nil
	m.lastClosedPR = nil
	return m, nil
}

func (m Model) handleDismissNotificationKey() (tea.Model, tea.Cmd) {
	if !m.IsNotificationFocused() {
		return m.handleCloseStaleKey()
	}
	if len(m.notifications) == 0 {
		m.notificationFocused = false
		return m, nil
	}
	m = m.clampNotificationCursor()
	m.notifications = slices.Delete(m.notifications, m.notificationCursor, m.notificationCursor+1)
	if len(m.notifications) == 0 {
		m.notificationFocused = false
		m.notificationCursor = 0
		return m, nil
	}
	m = m.clampNotificationCursor()
	return m, nil
}

func (m Model) openNotificationAtCursor() (tea.Model, tea.Cmd) {
	if len(m.notifications) == 0 {
		return m, nil
	}
	m = m.clampNotificationCursor()
	targetURL := notificationURL(m.notifications[m.notificationCursor])
	if targetURL == "" {
		return m, nil
	}
	opener := m.openURL
	if opener == nil {
		opener = defaultOpenURL
	}
	return m, openURLCmd(opener, targetURL)
}

func (m Model) teaQuit() (tea.Model, tea.Cmd) {
	return m, tea.Quit
}

func (m Model) handleEnterKey() (tea.Model, tea.Cmd) {
	if m.IsNotificationFocused() {
		return m.openNotificationAtCursor()
	}
	if m.detailsOpen {
		m.detailsOpen = false
		return m, nil
	}
	selected := m.SelectedPR()
	if selected == nil {
		return m, nil
	}
	m.detailsOpen = true
	return m, nil
}

func (m Model) handleViewKey() (tea.Model, tea.Cmd) {
	selected := m.SelectedPR()
	if selected == nil {
		return m, nil
	}
	viewer := m.viewPR
	if viewer == nil {
		viewer = defaultViewPR
	}
	return m, viewer(*selected)
}

func (m Model) handleOpenKey() (tea.Model, tea.Cmd) {
	if m.IsNotificationFocused() {
		return m.openNotificationAtCursor()
	}
	selected := m.SelectedPR()
	if selected == nil || selected.URL == "" {
		return m, nil
	}
	opener := m.openURL
	if opener == nil {
		opener = defaultOpenURL
	}
	return m, openURLCmd(opener, selected.URL)
}

func (m Model) handleConfirmCloseKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y", "Y":
		pr := *m.confirmClosePR
		m.closingPR = &pr
		m.confirmClosePR = nil
		m.closeErr = nil
		m.closeErrPR = nil
		m.lastClosedPR = nil
		closer := m.closePR
		if closer == nil {
			closer = defaultClosePR
		}
		ctx := m.ctx
		if ctx == nil {
			ctx = context.Background()
		}
		return m, closePRCmd(ctx, closer, pr, "Closing PR as stale")
	default:
		m.confirmClosePR = nil
		return m, nil
	}
}

func (m Model) handleToggleStackKey() (tea.Model, tea.Cmd) {
	if m.IsNotificationFocused() {
		return m, nil
	}
	selected := m.SelectedPR()
	if selected == nil || !selected.IsPartOfStack() {
		return m, nil
	}
	return m.toggleStack(selected.StackKey()), nil
}

func (m Model) handleToggleAllStacksKey() (tea.Model, tea.Cmd) {
	if m.IsNotificationFocused() {
		return m, nil
	}
	return m.toggleAllStacks(), nil
}

func (m Model) handleNavKey(key string) (Model, bool) {
	if m.IsNotificationFocused() {
		switch key {
		case "j", "down":
			if m.notificationCursor >= len(m.notifications)-1 {
				m.notificationFocused = false
				vis := m.visibleItemIndices(m.activeTab)
				if len(vis) > 0 {
					m = m.setCursor(vis[0])
				}
				return m, true
			}
			m.notificationCursor = min(len(m.notifications)-1, m.notificationCursor+1)
			return m, true
		case "k", "up":
			m.notificationCursor = max(0, m.notificationCursor-1)
			return m, true
		case "g", "home":
			m.notificationCursor = 0
			return m, true
		case "G", "end":
			m.notificationCursor = max(0, len(m.notifications)-1)
			return m, true
		case "pgdown", "ctrl+d", "pgup", "ctrl+u":
			return m, true
		}
		return m, false
	}

	switch key {
	case "j", "down":
		return m.moveCursor(1), true
	case "k", "up":
		if len(m.notifications) > 0 && !m.hideNotifs {
			vis := m.visibleItemIndices(m.activeTab)
			cursor := m.Cursor()
			if len(vis) == 0 || cursor == vis[0] {
				m.notificationFocused = true
				m.notificationCursor = len(m.notifications) - 1
				return m, true
			}
		}
		return m.moveCursor(-1), true
	case "pgdown", "ctrl+d":
		visRows := m.VisibleRows()
		return m.moveCursor(max(1, visRows)), true
	case "pgup", "ctrl+u":
		visRows := m.VisibleRows()
		return m.moveCursor(-max(1, visRows)), true
	case "g", "home":
		vis := m.visibleItemIndices(m.activeTab)
		if len(vis) > 0 {
			return m.setCursor(vis[0]), true
		}
		return m.setCursor(0), true
	case "G", "end":
		vis := m.visibleItemIndices(m.activeTab)
		if len(vis) > 0 {
			return m.setCursor(vis[len(vis)-1]), true
		}
		return m, true
	}
	return m, false
}

// handleQueueLoaded adopts a completed fetch.
func (m Model) handleQueueLoaded(msg QueueLoadedMsg) (Model, tea.Cmd) {
	wasInitial := m.loading || m.staleSnapshot
	m.loading = false
	m.refreshing = false
	m.staleSnapshot = false
	m.loadingLoaded = 0
	m.loadingTotal = 0
	if msg.Err != nil {
		m.fetchErr = msg.Err
		return m, nil
	}
	m.fetchErr = nil
	if !m.nowInjected {
		// Recompute staleness and scores against the current clock so PRs
		// age while the app runs; injected clocks stay fixed for tests.
		m.now = time.Now()
	}
	m.lastFetch = time.Now()

	var notifCmd tea.Cmd
	if wasInitial {
		if m.detector != nil {
			m.detector.Seed(msg.Queue.Authored)
		}
	} else if m.detector != nil && !m.isDaemon() {
		notifCmd = notifyCmd(m.ctx, m.detector, m.notifier, m.logger, m.queue.Authored, msg.Queue.Authored)
	}

	m = m.applyQueue(msg.Queue)
	return m, notifCmd
}

func (m Model) isDaemon() bool {
	if ds, ok := m.src.(interface{ IsDaemon() bool }); ok && ds.IsDaemon() {
		return true
	}
	return false
}

func isTriggerEvent(t events.Type) bool {
	return slices.Contains(events.TriggerTypes, t)
}

func notificationFromEvent(ev events.Event, cfg *config.NotificationConfig) notify.Notification {
	n := notify.Notification{
		Trigger:  ev.Type,
		PRNumber: ev.PR,
		PRTitle:  ev.Title,
		Repo:     ev.Repo,
		URL:      fmt.Sprintf("https://github.com/%s/pull/%d", ev.Repo, ev.PR),
	}
	switch ev.Type {
	case events.TypeCIPassed:
		n.Title = fmt.Sprintf("CI Passed (#%d)", ev.PR)
		n.Message = fmt.Sprintf("Checks passed for %q (%s#%d)", ev.Title, ev.Repo, ev.PR)
	case events.TypeCIFailed:
		n.Title = fmt.Sprintf("CI Failed (#%d)", ev.PR)
		n.Message = fmt.Sprintf("CI failed for %q (%s#%d)", ev.Title, ev.Repo, ev.PR)
	case events.TypeConflict:
		n.Title = fmt.Sprintf("Merge Conflict (#%d)", ev.PR)
		n.Message = fmt.Sprintf("Merge conflict in %q (%s#%d)", ev.Title, ev.Repo, ev.PR)
	case events.TypePRMerged:
		n.Title = fmt.Sprintf("PR Merged (#%d)", ev.PR)
		n.Message = fmt.Sprintf("Merged: %q (%s#%d)", ev.Title, ev.Repo, ev.PR)
	case events.TypeReviewReceived:
		n.Title = fmt.Sprintf("Review on #%d", ev.PR)
		var author, state string
		if p, ok := ev.Payload.(events.ReviewPayload); ok {
			author = p.Author
			state = p.State
			n.SubmittedAt = p.SubmittedAt
		} else if raw, err := json.Marshal(ev.Payload); err == nil {
			var p events.ReviewPayload
			if json.Unmarshal(raw, &p) == nil {
				author = p.Author
				state = p.State
				n.SubmittedAt = p.SubmittedAt
			}
		}
		n.Author = author
		n.ReviewState = state
		stateText := formatReviewState(state)
		n.Message = fmt.Sprintf("@%s %s: %q (%s#%d)", author, stateText, ev.Title, ev.Repo, ev.PR)
	}
	notifyAssets(cfg).Apply(&n)
	return n
}

func notifySingleCmd(
	ctx context.Context,
	notifier notify.Notifier,
	logger zerolog.Logger,
	n notify.Notification,
) tea.Cmd {
	return func() tea.Msg {
		if notifier != nil {
			if ctx == nil {
				ctx = context.Background()
			}
			if err := notifier.Notify(ctx, n); err != nil {
				logNotifyFailure(logger, err, n)
			}
		}
		return NotificationMsg{Notifications: []notify.Notification{n}}
	}
}

// logNotifyFailure records a delivery failure. Without it a suppressed or
// broken notification backend is indistinguishable from a quiet queue.
func logNotifyFailure(logger zerolog.Logger, err error, n notify.Notification) {
	logger.Error().
		Err(err).
		Str("trigger", string(n.Trigger)).
		Str("repo", n.Repo).
		Int("pr", n.PRNumber).
		Msg("delivering notification failed")
}

func refreshQueueCmd(ctx context.Context, refresher any) tea.Cmd {
	return func() tea.Msg {
		var err error
		switch r := refresher.(type) {
		case interface {
			Refresh(context.Context) (server.Snapshot, error)
		}:
			_, err = r.Refresh(ctx)
		case interface{ Refresh(context.Context) error }:
			err = r.Refresh(ctx)
		}
		if err != nil {
			return QueueLoadedMsg{Err: fmt.Errorf("queue refresh: %w", err)}
		}
		return nil
	}
}

func formatReviewState(state string) string {
	switch state {
	case "APPROVED":
		return "Approved"
	case "CHANGES_REQUESTED":
		return "Changes requested"
	case "COMMENTED":
		return "Commented"
	default:
		return state
	}
}

func (m Model) activeList() []score.Scored {
	switch m.activeTab {
	case TabFocus:
		return m.focusItems
	case TabMine:
		return m.mineItems
	case TabInbox:
		return m.inboxItems
	}
	return nil
}

func (m Model) isStackExpanded(key string) bool {
	if val, ok := m.expandedStacks[key]; ok {
		return val
	}
	return !m.stacksCollapsed
}

func (m Model) findStackBottomIndex(tab Tab, key string) int {
	var list []score.Scored
	switch tab {
	case TabFocus:
		list = m.focusItems
	case TabMine:
		list = m.mineItems
	case TabInbox:
		list = m.inboxItems
	}
	for i, it := range list {
		if it.PR.IsPartOfStack() && it.PR.StackKey() == key {
			return i
		}
	}
	return -1
}

func (m Model) toggleStack(key string) Model {
	if m.expandedStacks == nil {
		m.expandedStacks = make(map[string]bool)
	}
	wasExp := m.isStackExpanded(key)
	m.expandedStacks[key] = !wasExp
	if wasExp {
		// Collapsing: if current cursor is on a child PR in this stack, snap to bottom PR
		bottomIdx := m.findStackBottomIndex(m.activeTab, key)
		if bottomIdx >= 0 {
			if curr := m.SelectedPR(); curr != nil && curr.StackKey() == key {
				m = m.setCursor(bottomIdx)
			}
		}
	}
	return m.clampTabView(m.activeTab)
}

func (m Model) toggleAllStacks() Model {
	list := m.activeList()
	hasCollapsed := false
	for _, it := range list {
		if it.PR.IsPartOfStack() {
			key := it.PR.StackKey()
			if !m.isStackExpanded(key) {
				hasCollapsed = true
				break
			}
		}
	}

	m.expandedStacks = make(map[string]bool)
	if hasCollapsed {
		m.stacksCollapsed = false
	} else {
		m.stacksCollapsed = true
		vis := m.visibleItemIndices(m.activeTab)
		cursor := m.Cursor()
		found := slices.Contains(vis, cursor)
		if !found && len(vis) > 0 {
			if curr := m.SelectedPR(); curr != nil && curr.IsPartOfStack() {
				bottomIdx := m.findStackBottomIndex(m.activeTab, curr.StackKey())
				if bottomIdx >= 0 {
					m = m.setCursor(bottomIdx)
				} else {
					m = m.setCursor(vis[0])
				}
			} else {
				m = m.setCursor(vis[0])
			}
		}
	}
	return m.clampTabView(m.activeTab)
}

func (m Model) visibleItemIndices(tab Tab) []int {
	rows := m.buildDisplayRows(tab)
	var indices []int
	for _, r := range rows {
		if r.kind == rowItem {
			indices = append(indices, r.itemIndex)
		}
	}
	return indices
}

func (m Model) moveCursor(delta int) Model {
	list := m.activeList()
	if len(list) == 0 {
		return m
	}
	vis := m.visibleItemIndices(m.activeTab)
	if len(vis) == 0 {
		return m
	}
	cursor := m.Cursor()
	currPos := 0
	found := false
	for i, idx := range vis {
		if idx == cursor {
			currPos = i
			found = true
			break
		}
	}
	if !found {
		currPos = 0
	}
	newPos := clampIndex(currPos+delta, len(vis)-1)
	return m.setCursor(vis[newPos])
}

func (m Model) setCursor(pos int) Model {
	list := m.activeList()
	if len(list) == 0 {
		return m
	}
	switch m.activeTab {
	case TabFocus:
		m.cursorFocus = clampIndex(pos, len(list)-1)
	case TabMine:
		m.cursorMine = clampIndex(pos, len(list)-1)
	case TabInbox:
		m.cursorInbox = clampIndex(pos, len(list)-1)
	}
	return m.clampTabView(m.activeTab)
}

// clampTabView clamps the tab's cursor to list bounds and re-anchors scroll
// so the cursor's display row stays within the visible window.
func (m Model) clampTabView(tab Tab) Model {
	visRows := m.VisibleRows()
	switch tab {
	case TabFocus:
		if len(m.focusItems) == 0 {
			m.cursorFocus, m.scrollFocus = 0, 0
			return m
		}
		m.cursorFocus = clampIndex(m.cursorFocus, len(m.focusItems)-1)
		dispCursor, totalRows := m.displayCursorAndRows(TabFocus)
		m.scrollFocus = clampScroll(m.scrollFocus, dispCursor, totalRows, visRows)
		return m
	case TabMine:
		if len(m.mineItems) == 0 {
			m.cursorMine, m.scrollMine = 0, 0
			return m
		}
		m.cursorMine = clampIndex(m.cursorMine, len(m.mineItems)-1)
		dispCursor, totalRows := m.displayCursorAndRows(TabMine)
		m.scrollMine = clampScroll(m.scrollMine, dispCursor, totalRows, visRows)
		return m
	case TabInbox:
		if len(m.inboxItems) == 0 {
			m.cursorInbox, m.scrollInbox = 0, 0
			return m
		}
		m.cursorInbox = clampIndex(m.cursorInbox, len(m.inboxItems)-1)
		dispCursor, totalRows := m.displayCursorAndRows(TabInbox)
		m.scrollInbox = clampScroll(m.scrollInbox, dispCursor, totalRows, visRows)
		return m
	}
	return m
}

func (m Model) displayCursorAndRows(tab Tab) (int, int) {
	rows := m.buildDisplayRows(tab)
	if len(rows) == 0 {
		return 0, 0
	}
	var cursor int
	switch tab {
	case TabFocus:
		cursor = m.cursorFocus
	case TabMine:
		cursor = m.cursorMine
	case TabInbox:
		cursor = m.cursorInbox
	}

	displayCursor := cursor
	for i, r := range rows {
		if r.kind == rowItem && r.itemIndex == cursor {
			displayCursor = i
			break
		}
	}
	vis := m.visibleItemIndices(tab)
	if len(vis) > 0 && cursor == vis[0] {
		// At the list top, anchor to the display top so the first section
		// divider stays visible. Safe because ranking sorts by the same
		// categories the display groups by: item 0 is always in the first
		// section, at row 0 or 1.
		displayCursor = 0
	}
	return displayCursor, len(rows)
}

func clampScroll(scroll, cursor, listLen, visRows int) int {
	if listLen <= visRows {
		return 0
	}
	if cursor < scroll {
		scroll = cursor
	} else if cursor >= scroll+visRows {
		scroll = cursor - visRows + 1
	}
	maxScroll := listLen - visRows
	if scroll > maxScroll {
		scroll = maxScroll
	}
	if scroll < 0 {
		scroll = 0
	}
	return scroll
}

func clampIndex(val, maxVal int) int {
	if val < 0 {
		return 0
	}
	if val > maxVal {
		return maxVal
	}
	return val
}

// ActiveTab returns the currently selected tab.
func (m Model) ActiveTab() Tab {
	return m.activeTab
}

// Cursor returns the cursor index in the active tab.
func (m Model) Cursor() int {
	switch m.activeTab {
	case TabFocus:
		return m.cursorFocus
	case TabMine:
		return m.cursorMine
	case TabInbox:
		return m.cursorInbox
	}
	return 0
}

// ScrollOffset returns the scroll offset index in the active tab.
func (m Model) ScrollOffset() int {
	switch m.activeTab {
	case TabFocus:
		return m.scrollFocus
	case TabMine:
		return m.scrollMine
	case TabInbox:
		return m.scrollInbox
	}
	return 0
}

// VisibleRows returns the number of visible table rows fitting within terminal height.
func (m Model) VisibleRows() int {
	h := m.height
	if h <= 0 {
		h = 24
	}
	// Chrome lines: margins (2), tabs (2), table headers & border (3), scroll indicator (1), help bar (2) = 10 lines
	chrome := 10
	var notificationsHeight int
	if len(m.notifications) > 0 {
		if m.hideNotifs {
			notificationsHeight = 2
		} else {
			notificationsHeight = len(m.notifications) + 3
		}
	}
	chrome += notificationsHeight
	if m.statusBannerShown() {
		chrome += 2
	}
	avail := h - chrome
	if avail < 1 {
		return 1
	}
	return avail
}

// statusBannerShown reports whether a status banner is rendered at the
// bottom of the view, consuming two chrome lines. Keep in sync with the View banner switch.
func (m Model) statusBannerShown() bool {
	return m.fetchErr != nil || m.refreshing || m.staleSnapshot ||
		(m.closeErr != nil && m.closeErrPR != nil) ||
		m.confirmClosePR != nil || m.closingPR != nil || m.lastClosedPR != nil ||
		(m.viewErr != nil && m.viewErrPR != nil) || m.notifyHint() != ""
}

func (m Model) removePR(target model.PullRequest) Model {
	matches := func(p model.PullRequest) bool {
		if target.URL != "" && p.URL != "" {
			return p.URL == target.URL
		}
		if target.RepoNameWithOwner != "" && p.RepoNameWithOwner != "" {
			return p.RepoNameWithOwner == target.RepoNameWithOwner && p.Number == target.Number
		}
		return p.Number == target.Number
	}

	newKeys := make(map[model.PRKey]bool, len(m.focusedKeys))
	for k, v := range m.focusedKeys {
		if v {
			newKeys[k] = true
		}
	}
	delete(newKeys, target.Key())

	newInbox := make([]model.PullRequest, 0, len(m.queue.Inbox))
	for _, pr := range m.queue.Inbox {
		if !matches(pr) {
			newInbox = append(newInbox, pr)
		} else {
			delete(newKeys, pr.Key())
		}
	}

	newAuthored := make([]model.PullRequest, 0, len(m.queue.Authored))
	for _, pr := range m.queue.Authored {
		if !matches(pr) {
			newAuthored = append(newAuthored, pr)
		} else {
			delete(newKeys, pr.Key())
		}
	}

	m.focusedKeys = newKeys
	m.queue.Inbox = newInbox
	m.queue.Authored = newAuthored
	return m.applyQueue(m.queue)
}

// SelectedPR returns the currently highlighted pull request, or nil if none.
func (m Model) SelectedPR() *model.PullRequest {
	scored := m.SelectedScored()
	if scored == nil {
		return nil
	}
	return &scored.PR
}

// SelectedScored returns the currently highlighted scored pull request, or nil if none.
func (m Model) SelectedScored() *score.Scored {
	if m.IsNotificationFocused() {
		return nil
	}
	switch m.activeTab {
	case TabFocus:
		if len(m.focusItems) == 0 || m.cursorFocus < 0 || m.cursorFocus >= len(m.focusItems) {
			return nil
		}
		return &m.focusItems[m.cursorFocus]
	case TabMine:
		if len(m.mineItems) == 0 || m.cursorMine < 0 || m.cursorMine >= len(m.mineItems) {
			return nil
		}
		return &m.mineItems[m.cursorMine]
	case TabInbox:
		if len(m.inboxItems) == 0 || m.cursorInbox < 0 || m.cursorInbox >= len(m.inboxItems) {
			return nil
		}
		return &m.inboxItems[m.cursorInbox]
	}
	return nil
}

// IsModalOpen reports whether the score breakdown modal is currently visible.
func (m Model) IsModalOpen() bool {
	return m.modalOpen
}

// IsDetailsOpen reports whether the condensed PR details view is currently visible.
func (m Model) IsDetailsOpen() bool {
	return m.detailsOpen
}

// SortStrategy returns the active sorting strategy.
func (m Model) SortStrategy() SortStrategy {
	return m.sortStrategy
}

// IsFocused reports whether a pull request key is currently focused.
func (m Model) IsFocused(key model.PRKey) bool {
	return m.focusedKeys[key]
}

// FocusedKeys returns a copy of the currently focused PR keys.
func (m Model) FocusedKeys() map[model.PRKey]bool {
	keys := make(map[model.PRKey]bool, len(m.focusedKeys))
	for k, v := range m.focusedKeys {
		if v {
			keys[k] = true
		}
	}
	return keys
}

// FocusItems returns the scored pull requests in the focus tab.
func (m Model) FocusItems() []score.Scored {
	return m.focusItems
}

// WithFocusItems sets initial focus items.
func WithFocusItems(items []score.Scored) Option {
	return func(m *Model) {
		m.focusItems = items
		if m.focusedKeys == nil {
			m.focusedKeys = make(map[model.PRKey]bool)
		}
		for _, item := range items {
			m.focusedKeys[item.PR.Key()] = true
		}
	}
}

// InboxItems returns the scored pull requests in the inbox tab.
func (m Model) InboxItems() []score.Scored {
	return m.inboxItems
}

// MineItems returns the pull requests in the mine tab.
func (m Model) MineItems() []score.Scored {
	return m.mineItems
}

// Viewer returns the viewer login used for scoring attribution.
func (m Model) Viewer() string {
	return m.viewer
}

// Teams returns the viewer's org-qualified teams.
func (m Model) Teams() []string {
	return m.teams
}

// IsLoading reports whether the initial fetch is still in flight.
func (m Model) IsLoading() bool {
	return m.loading
}

// IsRefreshing reports whether a background or manual refresh is in flight.
func (m Model) IsRefreshing() bool {
	return m.refreshing
}

// isRefreshingVisual reports whether the model should display a refresh indicator.
func (m Model) isRefreshingVisual() bool {
	return m.refreshing || m.staleSnapshot || (!m.loading && m.loadingTotal > 0 && m.loadingLoaded < m.loadingTotal)
}

// StaleSnapshot reports whether the startup snapshot was stale enough to
// require an immediate refresh.
func (m Model) StaleSnapshot() bool {
	return m.staleSnapshot
}

// FetchErr returns the last refresh error, if any.
func (m Model) FetchErr() error {
	return m.fetchErr
}

// LoadingProgress returns the loaded and total counts during an in-flight fetch.
func (m Model) LoadingProgress() (loaded, total int) {
	return m.loadingLoaded, m.loadingTotal
}

// ConfirmingClosePR returns the pull request currently pending close confirmation, or nil.
func (m Model) ConfirmingClosePR() *model.PullRequest {
	return m.confirmClosePR
}

// IsClosing reports whether a pull request close operation is in flight.
func (m Model) IsClosing() bool {
	return m.closingPR != nil
}

// CloseErr returns the last error from closing a pull request, if any.
func (m Model) CloseErr() error {
	return m.closeErr
}

const (
	maxNotifications = 5
	notificationTTL  = 30 * time.Minute
)

// Notifications returns the slice of current notifications, newest first.
func (m Model) Notifications() []notify.Notification {
	return m.notifications
}

// NotificationCursor returns the index of the selected notification.
func (m Model) NotificationCursor() int {
	return m.notificationCursor
}

// LastNotification returns the last notification triggered, if any.
func (m Model) LastNotification() *notify.Notification {
	if len(m.notifications) == 0 {
		return nil
	}
	return &m.notifications[0]
}

// IsNotificationFocused reports whether the notification banner currently has focus.
func (m Model) IsNotificationFocused() bool {
	return m.notificationFocused && len(m.notifications) > 0
}

// HideNotifs reports whether the notification banner is currently collapsed.
func (m Model) HideNotifs() bool {
	return m.hideNotifs
}

func (m Model) currentTime() time.Time {
	return m.refTime()
}

func (m Model) clampNotificationCursor() Model {
	if len(m.notifications) == 0 {
		m.notificationCursor = 0
		return m
	}
	if m.notificationCursor < 0 {
		m.notificationCursor = 0
	} else if m.notificationCursor >= len(m.notifications) {
		m.notificationCursor = len(m.notifications) - 1
	}
	return m
}

func (m Model) addNotification(n notify.Notification) Model {
	if n.SubmittedAt.IsZero() {
		n.SubmittedAt = m.currentTime()
	}

	m.notifications = slices.DeleteFunc(m.notifications, func(old notify.Notification) bool {
		samePR := old.PRNumber == n.PRNumber && (old.Repo == "" || n.Repo == "" || old.Repo == n.Repo)
		if !samePR {
			return false
		}
		switch n.Trigger {
		case notify.TriggerCIPassed:
			return old.Trigger == notify.TriggerCIFailed || old.Trigger == notify.TriggerCIPassed
		case notify.TriggerCIFailed:
			return old.Trigger == notify.TriggerCIPassed || old.Trigger == notify.TriggerCIFailed
		case notify.TriggerConflict:
			return old.Trigger == notify.TriggerConflict
		case notify.TriggerPRMerged:
			return true
		default:
			return old.Trigger == n.Trigger
		}
	})

	m.notifications = append([]notify.Notification{n}, m.notifications...)
	if len(m.notifications) > maxNotifications {
		m.notifications = m.notifications[:maxNotifications]
	}
	m = m.clampNotificationCursor()
	return m
}

func (m Model) pruneStaleNotifications() Model {
	if len(m.notifications) == 0 {
		return m
	}
	now := m.currentTime()
	m.notifications = slices.DeleteFunc(m.notifications, func(n notify.Notification) bool {
		if n.SubmittedAt.IsZero() {
			return false
		}
		return now.Sub(n.SubmittedAt) > notificationTTL
	})
	return m.clampNotificationCursor()
}

type queueSyncContext struct {
	currPRs    map[model.PRKey]model.PullRequest
	prevPRs    map[model.PRKey]model.PullRequest
	mergedPRs  map[model.PRKey]bool
	mergedNums map[int]bool
	lastClosed *model.PullRequest
}

func notificationMatchesPR(pr model.PullRequest, n notify.Notification) bool {
	if pr.Number != n.PRNumber {
		return false
	}
	if n.Repo == "" || pr.RepoNameWithOwner == "" {
		return true
	}
	return pr.RepoNameWithOwner == n.Repo
}

func (ctx queueSyncContext) findCurrPR(n notify.Notification) (model.PullRequest, bool) {
	if pr, ok := ctx.currPRs[model.PRKey{Repo: n.Repo, Number: n.PRNumber}]; ok {
		return pr, true
	}
	for _, pr := range ctx.currPRs {
		if notificationMatchesPR(pr, n) {
			return pr, true
		}
	}
	return model.PullRequest{}, false
}

func (ctx queueSyncContext) wasInPrev(n notify.Notification) bool {
	if _, ok := ctx.prevPRs[model.PRKey{Repo: n.Repo, Number: n.PRNumber}]; ok {
		return true
	}
	for _, pr := range ctx.prevPRs {
		if notificationMatchesPR(pr, n) {
			return true
		}
	}
	return false
}

func (ctx queueSyncContext) shouldDrop(n notify.Notification) bool {
	pk := model.PRKey{Repo: n.Repo, Number: n.PRNumber}
	isMerged := ctx.mergedPRs[pk] || ctx.mergedNums[n.PRNumber]

	// If PR is merged, drop earlier pending notifications for that PR
	if isMerged && n.Trigger != notify.TriggerPRMerged {
		return true
	}

	// If PR was previously in queue and is no longer in current queue,
	// it is merged or closed. Drop earlier pending notifications.
	if len(ctx.prevPRs) > 0 {
		if _, inCurr := ctx.findCurrPR(n); !inCurr && ctx.wasInPrev(n) {
			return n.Trigger != notify.TriggerPRMerged
		}
	}

	// If PR matches lastClosedPR, drop pending notifications
	if ctx.lastClosed != nil && notificationMatchesPR(*ctx.lastClosed, n) {
		return true
	}

	// Check current PR state in queue
	if pr, ok := ctx.findCurrPR(n); ok {
		if n.Trigger == notify.TriggerCIFailed && pr.Checks.IsPassing() {
			return true
		}
		if n.Trigger == notify.TriggerConflict && !pr.MergeStatus.HasConflict() {
			return true
		}
	}

	return false
}

func (m Model) syncNotificationsWithQueue() Model {
	if len(m.notifications) == 0 {
		return m
	}

	ctx := queueSyncContext{
		currPRs:    make(map[model.PRKey]model.PullRequest, len(m.queue.Authored)+len(m.queue.Inbox)),
		prevPRs:    make(map[model.PRKey]model.PullRequest, len(m.prevQueue.Authored)+len(m.prevQueue.Inbox)),
		mergedPRs:  make(map[model.PRKey]bool),
		mergedNums: make(map[int]bool),
		lastClosed: m.lastClosedPR,
	}

	for _, pr := range m.queue.Authored {
		ctx.currPRs[pr.Key()] = pr
	}
	for _, pr := range m.queue.Inbox {
		ctx.currPRs[pr.Key()] = pr
	}

	for _, pr := range m.prevQueue.Authored {
		ctx.prevPRs[pr.Key()] = pr
	}
	for _, pr := range m.prevQueue.Inbox {
		ctx.prevPRs[pr.Key()] = pr
	}

	for _, n := range m.notifications {
		if n.Trigger == notify.TriggerPRMerged {
			ctx.mergedPRs[model.PRKey{Repo: n.Repo, Number: n.PRNumber}] = true
			ctx.mergedNums[n.PRNumber] = true
		}
	}

	m.notifications = slices.DeleteFunc(m.notifications, ctx.shouldDrop)
	return m.clampNotificationCursor()
}

func notificationURL(n notify.Notification) string {
	baseURL := n.URL
	if baseURL == "" && n.Repo != "" && n.PRNumber > 0 {
		baseURL = fmt.Sprintf("https://github.com/%s/pull/%d", n.Repo, n.PRNumber)
	}
	if n.Trigger == notify.TriggerCIFailed && baseURL != "" {
		return strings.TrimSuffix(baseURL, "/") + "/checks"
	}
	return baseURL
}

// ViewErr returns the error from the last PR view attempt, if any.
func (m Model) ViewErr() error {
	return m.viewErr
}

// ViewErrPR returns the PR whose view attempt failed, if any.
func (m Model) ViewErrPR() *model.PullRequest {
	return m.viewErrPR
}
