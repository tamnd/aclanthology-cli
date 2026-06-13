package cli

import (
	"github.com/spf13/cobra"
)

func (a *App) paperCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "paper <paper-id>",
		Short: "Fetch metadata for a single ACL Anthology paper",
		Long: `Fetch the full metadata for a single paper by its ACL Anthology ID.

The paper-id is the identifier from the paper's URL on aclanthology.org:
  2024.acl-long.1    — first long paper at ACL 2024
  P19-1001           — legacy ACL 2019 paper
  2023.emnlp-main.42 — EMNLP 2023 paper

Examples:
  acl paper 2024.acl-long.1
  acl paper 2024.emnlp-main.42 -o json
  acl paper P19-1001 --fields title,authors,year,url`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a.progressf("fetching paper %q...", args[0])
			p, err := a.client.GetPaper(cmd.Context(), args[0])
			if err != nil {
				return mapFetchErr(err)
			}
			return a.render(p)
		},
	}
}
