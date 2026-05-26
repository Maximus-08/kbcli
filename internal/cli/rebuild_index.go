package cli

import (
	"fmt"

	"github.com/avnis/kb-system/internal/index"
	"github.com/avnis/kb-system/internal/vault"
	"github.com/spf13/cobra"
)

var rebuildIndexCmd = &cobra.Command{
	Use:   "rebuild-index",
	Short: "Regenerate INDEX.md from all wiki article frontmatter summaries",
	RunE: func(cmd *cobra.Command, args []string) error {
		wikiDir := vault.WikiDir(cfg)
		indexPath := vault.IndexPath(cfg)

		logger.Info("Rebuilding index from wiki directory...", "wikiDir", wikiDir, "indexPath", indexPath)

		if err := index.Rebuild(wikiDir, indexPath); err != nil {
			return fmt.Errorf("failed to rebuild index: %w", err)
		}

		logger.Info("INDEX.md rebuilt successfully!")
		return nil
	},
}

func init() {
	rootCmd.AddCommand(rebuildIndexCmd)
}
