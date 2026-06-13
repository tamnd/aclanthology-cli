package cli

import (
	"github.com/spf13/cobra"
)

func (a *App) eventsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "events",
		Short: "List ACL Anthology events (conferences by year)",
		Long: `List events from the ACL Anthology events index.

Each event corresponds to a conference year, for example ACL 2024 or EMNLP 2023.
Use the event ID with the volume command to explore its proceedings.

Examples:
  acl events
  acl events --limit 20
  acl events -o json`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			n := a.effectiveLimit(30)
			a.progressf("fetching events...")
			events, err := a.client.ListEvents(cmd.Context(), n)
			if err != nil {
				return mapFetchErr(err)
			}
			return a.renderOrEmpty(events, len(events))
		},
	}
}
