package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/kalverra/pronto/internal/events"
)

func newAPICmd() *cobra.Command {
	apiCmd := &cobra.Command{
		Use:   "api",
		Short: "Inspect the socket API protocol",
	}
	// The output is the embedded schema.json verbatim — the same artifact
	// tools/gendocs generates from events.ValidTypes, events.ValidCodes, and
	// server.Methods — so the command itself has no content that can drift
	// from the protocol.
	schemaCmd := &cobra.Command{
		Use:   "schema",
		Short: "Print the socket API JSON Schema",
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := fmt.Fprintln(cmd.OutOrStdout(), events.SchemaJSON())
			return err
		},
	}
	apiCmd.AddCommand(schemaCmd)
	return apiCmd
}
