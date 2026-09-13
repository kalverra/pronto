// Package notify provides models, detectors, and dispatchers for pull request notifications.
package notify

import (
	"context"
	"errors"
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
	SoundPath   string
	Title       string
	Message     string
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

// ImageResolver resolves an image file path for a notification.
type ImageResolver func(n Notification) string

// SoundResolver resolves an audio file path or sound name for a notification.
type SoundResolver func(n Notification) string

// DetectorOption configures a Detector.
type DetectorOption func(*Detector)

// WithBotFilter configures whether bot reviews should be filtered out.
func WithBotFilter(filter bool) DetectorOption {
	return func(d *Detector) {
		d.filterBots = filter
	}
}

// WithImageResolver configures an image path resolver for notifications.
func WithImageResolver(resolver ImageResolver) DetectorOption {
	return func(d *Detector) {
		d.imageResolver = resolver
	}
}

// WithTriggerImages configures static image paths per trigger.
func WithTriggerImages(images map[Trigger]string) DetectorOption {
	return func(d *Detector) {
		d.imageResolver = func(n Notification) string {
			return images[n.Trigger]
		}
	}
}

// WithSoundResolver configures a sound path resolver for notifications.
func WithSoundResolver(resolver SoundResolver) DetectorOption {
	return func(d *Detector) {
		d.soundResolver = resolver
	}
}

// WithTriggerSounds configures static audio file paths per trigger.
func WithTriggerSounds(sounds map[Trigger]string) DetectorOption {
	return func(d *Detector) {
		d.soundResolver = func(n Notification) string {
			return sounds[n.Trigger]
		}
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

// NewDefaultNotifier creates the default notifier for the host operating system.
func NewDefaultNotifier() Notifier {
	return NewMacNotifier()
}
