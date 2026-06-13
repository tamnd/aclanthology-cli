package cli

import (
	"github.com/spf13/cobra"
)

func (a *App) searchCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "search <query>",
		Short: "Search ACL Anthology papers via Semantic Scholar",
		Long: `Search ACL Anthology papers via the Semantic Scholar public API.

Results are filtered to papers that carry an ACL Anthology identifier,
so every hit links back to aclanthology.org. No API key is required.

Examples:
  acl search "attention mechanism"
  acl search "BERT fine-tuning" --limit 20 -o json
  acl search "machine translation" -o url`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			n := a.effectiveLimit(10)
			a.progressf("searching for %q...", args[0])
			papers, err := a.client.Search(cmd.Context(), args[0], n)
			if err != nil {
				return mapFetchErr(err)
			}
			return a.renderOrEmpty(papers, len(papers))
		},
	}
	return cmd
}
