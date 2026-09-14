package main

import (
	"errors"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/kalverra/pronto/internal/events"
)

// ErrWaitTimeout is returned when wait exits before its --until condition
// was met. The CLI exits with code 2 in this case.
var ErrWaitTimeout = errors.New("wait timed out before the requested event occurred")

func newWaitCmd() *cobra.Command {
	var until, timeout string
	waitCmd := &cobra.Command{
		Use:   "wait <ref>",
		Short: "Block until an event occurs for a pull request",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			out := cmd.OutOrStdout()
			ref := args[0]

			untilTypes, err := parseUntil(until)
			if err != nil {
				return err
			}

			c, err := dialDaemon(resolveSocketPath(cmd))
			if err != nil {
				return err
			}
			defer c.Close()

			pr, err := c.GetPR(ctx, ref)
			if err != nil {
				return fmt.Errorf("resolve %q: %w", ref, err)
			}

			ch, err := c.Subscribe(ctx, events.Subscription{
				Types: untilTypes,
				Repo:  pr.RepoNameWithOwner,
				PR:    pr.Number,
			})
			if err != nil {
				return fmt.Errorf("subscribe: %w", err)
			}

			var timeoutCh <-chan time.Time
			if timeout != "" {
				d, err := time.ParseDuration(timeout)
				if err != nil {
					return fmt.Errorf("parse --timeout: %w", err)
				}
				if d > 0 {
					timer := time.NewTimer(d)
					defer timer.Stop()
					timeoutCh = timer.C
				}
			}

			select {
			case ev, ok := <-ch:
				if !ok {
					return errors.New("event stream closed unexpectedly")
				}
				_, err := fmt.Fprintln(out, formatEventPretty(ev))
				return err
			case <-timeoutCh:
				return ErrWaitTimeout
			case <-ctx.Done():
				return ctx.Err()
			}
		},
	}
	waitCmd.Flags().StringVar(&until, "until", "", "Event to wait for: an event type or alias (merged, ci_settled)")
	waitCmd.Flags().StringVar(&timeout, "timeout", "", "Give up after this duration (e.g. 30m)")
	_ = waitCmd.MarkFlagRequired("until")
	return waitCmd
}

// parseUntil resolves an --until value into the event types it matches.
func parseUntil(until string) ([]events.Type, error) {
	switch until {
	case "merged":
		return []events.Type{events.TypePRMerged}, nil
	case "ci_settled":
		return []events.Type{events.TypeCIPassed, events.TypeCIFailed}, nil
	case "":
		return nil, errors.New("--until is required (event type or alias: merged, ci_settled)")
	}
	if !events.ValidTypes[events.Type(until)] {
		return nil, fmt.Errorf("unknown --until value %q (event type or alias: merged, ci_settled)", until)
	}
	return []events.Type{events.Type(until)}, nil
}
