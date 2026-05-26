package cli

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/avnis/kb-system/internal/provider"
	"github.com/avnis/kb-system/internal/querier"
	"github.com/spf13/cobra"
)

var queryCmd = &cobra.Command{
	Use:   "query [query]",
	Short: "Ask a natural language query over the compiled wiki articles",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		queryStr := strings.Join(args, " ")

		// For a clean terminal experience, default query command logger to a discard logger
		// unless a specific log-level flag was explicitly supplied on the command line.
		queryLogger := logger
		if logLevelFlag == "" {
			queryLogger = slog.New(slog.NewTextHandler(io.Discard, nil))
		}

		// Initialize LLM provider chain
		prov := provider.NewChain(
			cfg.GeminiAPIKey,
			cfg.OpenRouterAPIKey,
			cfg.GroqAPIKey,
			cfg.OllamaCloudBaseURL,
			cfg.OllamaCloudAPIKey,
			cfg.OllamaBaseURL,
			queryLogger,
		)
		q := querier.New(cfg, prov, queryLogger)

		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()

		queryLogger.Info("Executing natural language query...", "query", queryStr)
		answer, err := q.Query(ctx, queryStr)
		if err != nil {
			return err
		}

		fmt.Println("\n" + answer)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(queryCmd)
}
