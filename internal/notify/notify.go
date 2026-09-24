// Package notify provides models, detectors, and dispatchers for pull request notifications.
package notify

import (
	"context"
	"errors"
	"runtime"
	"time"

	"github.com/kalverra/pronto/internal/events"
)

// Trigger identifies the event that caused a notification.
type Trigger = events.Type

const (
	// TriggerCIPassed indicates CI checks have succeeded.
	TriggerCIPassed Trigger = events.TypeCIPassed
	// TriggerCIFailed indicates one or more CI checks have failed.
	TriggerCIFailed Trigger = events.TypeCIFailed
	// TriggerConflict indicates the pull request has merge conflicts.
	TriggerConflict Trigger = events.TypeConflict
	// TriggerReviewReceived indicates a new review was submitted on the pull request.
	TriggerReviewReceived Trigger = events.TypeReviewReceived
	// TriggerPRMerged indicates the pull request has been merged.
	TriggerPRMerged Trigger = events.TypePRMerged
)

// Triggers lists all triggers that can cause notifications.
var Triggers = events.TriggerTypes

// Notification represents a notification event for a pull request.
type Notification struct {
	Trigger     Trigger
	PRNumber    int
	PRTitle     string
	Repo        string
	URL         string
	Author      string
	ReviewState string
	CommitOID   string
	SubmittedAt time.Time
	ImagePath   string
	// Sound is a macOS system sound name (e.g. "Glass"), "default" for the OS
	// default alert sound, or "" for no sound. It is never a file path.
	Sound   string
	Title   string
	Message string
}

// Notifier defines the interface for delivering notifications to the user.
type Notifier interface {
	Notify(ctx context.Context, n Notification) error
}

// MultiNotifier combines multiple Notifier implementations into one.
type MultiNotifier []Notifier

// Notify delivers the notification to all non-nil notifiers in the slice.
func (m MultiNotifier) Notify(ctx context.Context, n Notification) error {
	var errs []error
	for _, notifier := range m {
		if notifier != nil {
			if err := notifier.Notify(ctx, n); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

// PRStatusChecker checks whether a pull request has been merged.
type PRStatusChecker func(ctx context.Context, repo string, number int) (merged bool, err error)

// Assets holds per-trigger image and sound overrides, resolved once from user
// config and applied to every notification before delivery.
type Assets struct {
	Images map[Trigger]string
	Sounds map[Trigger]string
}

// Apply fills n's image and sound from a. An unset or empty image falls back
// to the trigger's default icon; an unset sound leaves n.Sound untouched
// (backends decide their own default when a notifier's sound channel is
// enabled).
func (a Assets) Apply(n *Notification) {
	if img := a.Images[n.Trigger]; img != "" {
		n.ImagePath = img
	}
	if n.ImagePath == "" {
		n.ImagePath = DefaultTriggerImage(*n)
	}
	if snd, ok := a.Sounds[n.Trigger]; ok {
		n.Sound = snd
	}
}

// DetectorOption configures a Detector.
type DetectorOption func(*Detector)

// WithBotFilter configures whether bot reviews should be filtered out.
func WithBotFilter(filter bool) DetectorOption {
	return func(d *Detector) {
		d.filterBots = filter
	}
}

// WithAssets configures the per-trigger image and sound overrides applied to
// every notification the detector produces.
func WithAssets(assets Assets) DetectorOption {
	return func(d *Detector) {
		d.assets = assets
	}
}

// WithCheckerTimeout configures the per-check timeout for vanished PR status checks.
func WithCheckerTimeout(timeout time.Duration) DetectorOption {
	return func(d *Detector) {
		d.checkerTimeout = timeout
	}
}

// WithCheckerParallelism configures the maximum concurrent vanished PR status checks.
func WithCheckerParallelism(n int) DetectorOption {
	return func(d *Detector) {
		d.checkerParallelism = n
	}
}

// Options configures how NewNotifier builds a desktop Notifier, decoupled
// from the config package so notify never imports it.
type Options struct {
	// Mode selects the delivery backend: ModeNative or ModeTerminal.
	Mode string
	// Sound enables the notifier's sound channel.
	Sound bool
}

// NewNotifier returns the desktop notifier for opts.Mode. On macOS, native
// mode delivers through the bundled helper app and falls back to the
// terminal backend (terminal-notifier, then osascript) while the helper is
// missing or not authorized; the returned *FallbackNotifier's Health says
// why. Elsewhere, and in terminal mode, the terminal backend is used.
func NewNotifier(opts Options) Notifier {
	terminal := NewMacNotifier(WithSoundEnabled(opts.Sound))
	if opts.Mode != ModeNative || runtime.GOOS != "darwin" {
		return terminal
	}

	var fallbackOpts []FallbackOption
	switch NativeHelperState(DiscoverNativeHelper()) {
	case HelperMissing:
		fallbackOpts = append(fallbackOpts, WithFallbackReason(ErrHelperNotFound))
	case HelperStale:
		fallbackOpts = append(fallbackOpts, WithFallbackAdvisory(ErrHelperStale))
	case HelperInstalled:
	}
	native := NewNativeNotifier(WithNativeSoundEnabled(opts.Sound))
	return NewFallbackNotifier(native, terminal, fallbackOpts...)
}
