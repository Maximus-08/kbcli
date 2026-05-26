package cli

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/avnis/kb-system/internal/compiler"
	"github.com/avnis/kb-system/internal/provider"
	"github.com/avnis/kb-system/internal/watcher"
	"github.com/spf13/cobra"
)

var (
	watchPoll bool
)

var watchCmd = &cobra.Command{
	Use:   "watch",
	Short: "Watch the raw directory for new or updated source files",
	RunE: func(cmd *cobra.Command, args []string) error {
		// Watcher uses the LLM provider chain for compilation
		compProvider := provider.NewChain(
			cfg.GeminiAPIKey,
			cfg.OpenRouterAPIKey,
			cfg.GroqAPIKey,
			cfg.OllamaCloudBaseURL,
			cfg.OllamaCloudAPIKey,
			cfg.OllamaBaseURL,
			logger,
		)
		comp := compiler.New(cfg, compProvider, logger)

		usePoll := watchPoll || cfg.WatcherPollFallback

		w := watcher.New(cfg, comp, logger, usePoll)

		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()

		logger.Info("Starting watch pipeline. Press Ctrl+C to stop.")
		if err := w.Start(ctx); err != nil {
			return err
		}

		logger.Info("Watch pipeline stopped.")
		return nil
	},
}

func init() {
	watchCmd.Flags().BoolVar(&watchPoll, "poll", false, "Force polling mode instead of fsnotify")
	rootCmd.AddCommand(watchCmd)
}
