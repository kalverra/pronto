package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/kalverra/pronto"
)

func newAgentCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "agent",
		Short: "Print instructions and protocol guide for AI coding agents",
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := fmt.Fprint(cmd.OutOrStdout(), pronto.AgentSkill)
			return err
		},
	}
}
