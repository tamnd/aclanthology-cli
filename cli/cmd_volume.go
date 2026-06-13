package cli

import (
	"github.com/spf13/cobra"
)

func (a *App) volumeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "volume <volume-id>",
		Short: "List papers in an ACL Anthology volume",
		Long: `List all papers in an ACL Anthology proceedings volume.

The volume-id is the identifier used on aclanthology.org, for example:
  2024.acl-long      — ACL 2024 long papers
  2024.emnlp-main    — EMNLP 2024 main proceedings
  2024.naacl-long    — NAACL 2024 long papers
  2024.findings-acl  — ACL 2024 Findings
  2023.acl-long      — ACL 2023 long papers

Examples:
  acl volume 2024.acl-long
  acl volume 2024.emnlp-main --limit 50
  acl volume 2024.acl-long -o json
  acl volume 2024.naacl-long -o url`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			n := a.effectiveLimit(0)
			a.progressf("fetching volume %q...", args[0])
			papers, err := a.client.ListVolume(cmd.Context(), args[0], n)
			if err != nil {
				return mapFetchErr(err)
			}
			return a.renderOrEmpty(papers, len(papers))
		},
	}
}
