package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/avnis/kb-system/internal/compiler"
	"github.com/avnis/kb-system/internal/provider"
	"github.com/avnis/kb-system/internal/vault"
	"github.com/spf13/cobra"
)

var (
	compactAll bool
)

var compactCmd = &cobra.Command{
	Use:   "compact [slug]",
	Short: "Perform loss-less technical compaction on wiki articles to maximize information density",
	RunE: func(cmd *cobra.Command, args []string) error {
		compProvider := provider.NewChain(
			cfg.GeminiAPIKey,
			cfg.OpenRouterAPIKey,
			cfg.GroqAPIKey,
			cfg.OllamaCloudBaseURL,
			cfg.OllamaCloudAPIKey,
			cfg.OllamaBaseURL,
			logger,
		)
		c := compiler.New(cfg, compProvider, logger)

		wikiDir := vault.WikiDir(cfg)

		if compactAll {
			logger.Info("Starting sweep compaction of all wiki articles...")
			entries, err := os.ReadDir(wikiDir)
			if err != nil {
				return fmt.Errorf("failed to read wiki directory: %w", err)
			}

			var count int
			for _, entry := range entries {
				if entry.IsDir() || strings.EqualFold(entry.Name(), "INDEX.md") || filepath.Ext(entry.Name()) != ".md" {
					continue
				}
				slug := strings.TrimSuffix(strings.ToLower(entry.Name()), ".md")
				logger.Info("Compacting wiki article...", "slug", slug)

				err := c.CompactSingle(slug)
				if err != nil {
					logger.Error("Failed to compact article", "slug", slug, "error", err)
					continue
				}
				count++
			}
			logger.Info("Sweep compaction completed!", "articlesCompacted", count)
			return nil
		}

		if len(args) == 0 {
			return fmt.Errorf("please provide a slug to compact, or use the --all flag to compact all articles")
		}

		slug := strings.TrimSuffix(strings.ToLower(args[0]), ".md")
		logger.Info("Compacting wiki article...", "slug", slug)
		err := c.CompactSingle(slug)
		if err != nil {
			return err
		}

		logger.Info("Compaction complete for article", "slug", slug)
		return nil
	},
}

func init() {
	compactCmd.Flags().BoolVar(&compactAll, "all", false, "Compact all articles in the wiki")
	rootCmd.AddCommand(compactCmd)
}
