package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/kalverra/pronto/internal/source"
)

// ErrJSONRequired is returned when list is invoked without --json; use the
// bare `pronto` TUI for interactive output.
var ErrJSONRequired = errors.New("list requires --json (run bare `pronto` for the TUI)")

func newListCmd(src source.Source) *cobra.Command {
	var listJSON bool
	listCmd := &cobra.Command{
		Use:   "list",
		Short: "List pull requests in review queue",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !listJSON {
				return ErrJSONRequired
			}

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

			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			if err := enc.Encode(queue); err != nil {
				return fmt.Errorf("encode queue json: %w", err)
			}
			return nil
		},
	}
	listCmd.Flags().BoolVar(&listJSON, "json", false, "Output full queue in JSON format")
	return listCmd
}
