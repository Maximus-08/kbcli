package cli

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/avnis/kb-system/internal/cleaner"
	"github.com/avnis/kb-system/internal/index"
	"github.com/avnis/kb-system/internal/provider"
	"github.com/avnis/kb-system/internal/vault"
	"github.com/spf13/cobra"
)

var (
	cleanupDryRun bool
	cleanupForce  bool
)

var cleanupCmd = &cobra.Command{
	Use:   "cleanup",
	Short: "Detect and remove redundant or orphan wiki articles",
	RunE: func(cmd *cobra.Command, args []string) error {
		// Cleanup subcommand uses the LLM provider chain
		compProvider := provider.NewChain(
			cfg.GeminiAPIKey,
			cfg.OpenRouterAPIKey,
			cfg.GroqAPIKey,
			cfg.OllamaCloudBaseURL,
			cfg.OllamaCloudAPIKey,
			cfg.OllamaBaseURL,
			logger,
		)
		c := cleaner.New(cfg, compProvider, logger)

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()

		logger.Info("Scanning vault for cleanup candidates...")
		candidates, err := c.DetectCandidates(ctx)
		if err != nil {
			return err
		}

		if len(candidates) == 0 {
			logger.Info("No cleanup candidates detected.")
			return nil
		}

		fmt.Printf("Detected %d cleanup candidate(s):\n\n", len(candidates))
		for i, cand := range candidates {
			fmt.Printf("[%d] Type: %s\n    Path: %s\n    Reason: %s\n\n", i+1, cand.Type, filepath.Base(cand.Path), cand.Reason)
		}

		if cleanupDryRun {
			logger.Info("[DRY-RUN] Finished scan. No files were modified.")
			return nil
		}

		reader := bufio.NewReader(os.Stdin)
		var trashedCount int

		for _, cand := range candidates {
			shouldTrash := cleanupForce
			if !shouldTrash {
				fmt.Printf("Move %s to trash? [y/N]: ", filepath.Base(cand.Path))
				text, err := reader.ReadString('\n')
				if err != nil {
					logger.Error("Failed to read input, skipping candidate", "file", filepath.Base(cand.Path), "error", err)
					continue
				}
				text = strings.ToLower(strings.TrimSpace(text))
				shouldTrash = (text == "y" || text == "yes")
			}

			if shouldTrash {
				trashPath, err := cleaner.TrashFile(cfg, cand.Path)
				if err != nil {
					logger.Error("Failed to trash file", "file", filepath.Base(cand.Path), "error", err)
					continue
				}
				logger.Info("Moved file to trash", "file", filepath.Base(cand.Path), "trashPath", trashPath)

				// Remove from index
				slug := strings.TrimSuffix(strings.ToLower(filepath.Base(cand.Path)), ".md")
				indexPath := vault.IndexPath(cfg)
				if err := index.Remove(indexPath, slug); err != nil {
					logger.Warn("Failed to remove slug from index", "slug", slug, "error", err)
				} else {
					logger.Debug("Removed slug from INDEX.md", "slug", slug)
				}
				trashedCount++
			}
		}

		logger.Info("Cleanup complete", "filesTrashed", trashedCount)
		return nil
	},
}

func init() {
	cleanupCmd.Flags().BoolVar(&cleanupDryRun, "dry-run", false, "Scan and display candidates without deleting them")
	cleanupCmd.Flags().BoolVarP(&cleanupForce, "force", "f", false, "Trash candidates without prompting for confirmation")
	rootCmd.AddCommand(cleanupCmd)
}
