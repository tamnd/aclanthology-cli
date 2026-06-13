package cli

import (
	"github.com/spf13/cobra"
)

func (a *App) recentCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "recent",
		Short: "List recent papers from major ACL-org venues",
		Long: `List recent papers from the major ACL-org conference venues:
ACL 2024, EMNLP 2024, NAACL 2024, EACL 2024, and ACL Findings 2024.

Papers are returned in volume order, newest venues first.

Examples:
  acl recent
  acl recent --limit 50 -o json
  acl recent -o url`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			n := a.effectiveLimit(20)
			a.progressf("fetching recent papers...")
			papers, err := a.client.Recent(cmd.Context(), n)
			if err != nil {
				return mapFetchErr(err)
			}
			return a.renderOrEmpty(papers, len(papers))
		},
	}
}
