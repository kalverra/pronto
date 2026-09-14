package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/kalverra/pronto/internal/score"
	"github.com/kalverra/pronto/internal/source"
)

// ErrRefRequired is returned when why is invoked without a pull request reference.
var ErrRefRequired = errors.New("why requires a pull request reference (e.g. #123 or org/repo#123)")

func newWhyCmd(src source.Source) *cobra.Command {
	var whyJSON bool
	whyCmd := &cobra.Command{
		Use:   "why <ref>",
		Short: "Explain priority score calculation for a pull request",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 || strings.TrimSpace(args[0]) == "" {
				return ErrRefRequired
			}
			ref := args[0]

			s, err := resolveQueueSource(cmd.Context(), cmd, src)
			if err != nil {
				return err
			}

			fetchCtx, cancel := context.WithTimeout(cmd.Context(), source.ColdFetchTimeout)
			defer cancel()

			queue, err := s.Fetch(fetchCtx)
			if err != nil {
				return fmt.Errorf("fetch queue: %w", err)
			}

			pr, err := queue.Find(ref)
			if err != nil {
				return fmt.Errorf("find pull request %q: %w", ref, err)
			}

			isAuthored := queue.IsAuthored(*pr)

			var breakdown score.Breakdown
			if isAuthored {
				breakdown = score.ExplainMine(*pr, time.Now(), score.DefaultMineWeights())
			} else {
				breakdown = score.Explain(*pr, time.Now(), queue.Viewer, queue.Teams, nil, score.DefaultWeights())
			}

			if whyJSON {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				if err := enc.Encode(breakdown); err != nil {
					return fmt.Errorf("encode breakdown json: %w", err)
				}
				return nil
			}

			if _, err := fmt.Fprintf(cmd.OutOrStdout(), "Score: %.1f\n", breakdown.Total); err != nil {
				return fmt.Errorf("write score: %w", err)
			}
			for _, term := range breakdown.Terms {
				if _, err := fmt.Fprintf(
					cmd.OutOrStdout(),
					"  %-18s raw=%-6.1f wt=%-6.1f contrib=%+.1f\n",
					term.Name,
					term.Raw,
					term.Weight,
					term.Contribution,
				); err != nil {
					return fmt.Errorf("write term: %w", err)
				}
			}
			return nil
		},
	}
	whyCmd.Flags().BoolVar(&whyJSON, "json", false, "Output breakdown in JSON format")
	return whyCmd
}
