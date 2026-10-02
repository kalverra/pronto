package notify

import (
	"github.com/kalverra/pronto/internal/events"
)

// notificationEvent transforms a Notification into a typed events.Event for the bus.
// If wantNotify is true, ev.Notify is populated with the rendered notification banner.
func notificationEvent(n Notification, wantNotify bool) events.Event {
	ev := events.Event{
		Type:  n.Trigger,
		Repo:  n.Repo,
		PR:    n.PRNumber,
		Title: n.PRTitle,
	}
	switch n.Trigger {
	case TriggerReviewReceived:
		ev.Payload = events.ReviewPayload{
			Author:      n.Author,
			State:       n.ReviewState,
			SubmittedAt: n.SubmittedAt,
		}
	case TriggerEntered:
		if n.Entered != nil {
			ev.Payload = events.EnteredPayload{
				Tab:     string(n.Entered.Tab),
				Section: string(n.Entered.Section),
			}
		}
	}
	if wantNotify {
		ev.Notify = &events.NotificationPayload{
			Title:   n.Title,
			Message: n.Message,
			URL:     n.URL,
			Image:   n.ImagePath,
			Sound:   n.Sound,
		}
	}
	return ev
}
