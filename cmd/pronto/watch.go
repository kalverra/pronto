package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/kalverra/pronto/internal/client"
	"github.com/kalverra/pronto/internal/events"
	"github.com/kalverra/pronto/internal/model"
)

func newWatchCmd() *cobra.Command {
	var typesFilter string
	var jsonOut bool
	watchCmd := &cobra.Command{
		Use:   "watch [ref]",
		Short: "Stream PR events from a running pronto daemon",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			out := cmd.OutOrStdout()

			c, err := dialDaemon(resolveSocketPath(cmd))
			if err != nil {
				return err
			}
			defer c.Close()

			sub, err := buildSubscription(ctx, c, args, typesFilter)
			if err != nil {
				return err
			}

			ch, err := c.Subscribe(ctx, sub)
			if err != nil {
				return fmt.Errorf("subscribe: %w", err)
			}

			for ev := range ch {
				if jsonOut {
					if _, err := fmt.Fprintln(out, eventJSON(ev)); err != nil {
						return err
					}
					continue
				}
				if _, err := fmt.Fprintln(out, formatEventPretty(ev)); err != nil {
					return err
				}
			}
			return nil
		},
	}
	watchCmd.Flags().StringVar(&typesFilter, "types", "", "Comma-separated event types to watch (default: all)")
	watchCmd.Flags().BoolVar(&jsonOut, "json", false, "Emit events as JSONL")
	return watchCmd
}

// buildSubscription resolves an optional ref and --types filter into a
// subscription.
func buildSubscription(
	ctx context.Context,
	c *client.Client,
	args []string,
	typesFilter string,
) (events.Subscription, error) {
	sub := events.Subscription{}
	if typesFilter != "" {
		types, err := events.ParseTypes(typesFilter)
		if err != nil {
			return sub, err
		}
		sub.Types = types
	}
	if len(args) > 0 && strings.TrimSpace(args[0]) != "" {
		pr, err := c.GetPR(ctx, args[0])
		if err != nil {
			return sub, fmt.Errorf("resolve %q: %w", args[0], err)
		}
		sub.Repo = pr.RepoNameWithOwner
		sub.PR = pr.Number
	}
	return sub, nil
}

func eventJSON(ev events.Event) string {
	data, err := json.Marshal(ev)
	if err != nil {
		return fmt.Sprintf(`{"type":%q,"seq":%d,"marshal_error":%q}`, ev.Type, ev.Seq, err.Error())
	}
	return string(data)
}

func formatEventPretty(ev events.Event) string {
	ts := ev.TS.Format("15:04:05")
	if ev.Type == events.TypeQueueRefreshed {
		var payload events.QueueRefreshedPayload
		if raw, err := json.Marshal(ev.Payload); err == nil {
			_ = json.Unmarshal(raw, &payload)
		}
		return fmt.Sprintf(
			"%s queue_refreshed ok=%t authored=%d inbox=%d",
			ts,
			payload.OK,
			payload.Authored,
			payload.Inbox,
		)
	}
	return fmt.Sprintf("%s %s %s %q", ts, ev.Type, model.PRKey{Repo: ev.Repo, Number: ev.PR}, ev.Title)
}
