package notify

import "context"

// Test seams: the trigger tests drive detection at the Observed level so each
// case controls scopes directly instead of going through Classify.

// DetectChanges exposes detectChanges.
func (d *Detector) DetectChanges(ctx context.Context, prev, curr []Observed) []Notification {
	return d.detectChanges(ctx, prev, curr)
}

// SeedObserved exposes seedObserved.
func (d *Detector) SeedObserved(obs []Observed) {
	d.seedObserved(obs)
}

// DiffEvents exposes diffEvents.
var DiffEvents = diffEvents
