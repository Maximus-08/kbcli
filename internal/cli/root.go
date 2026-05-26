package cli

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/avnis/kb-system/internal/config"
	"github.com/avnis/kb-system/internal/vault"
	"github.com/spf13/cobra"
)

var (
	vaultFlag    string
	logLevelFlag string
	cfg          *config.Config
	logger       *slog.Logger
)

var rootCmd = &cobra.Command{
	Use:   "kb",
	Short: "Personal knowledge base pipeline",
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		// Load config
		var err error
		cfg, err = config.Load(vaultFlag)
		if err != nil {
			// Print config error to stderr and exit with code 2
			fmt.Fprintf(os.Stderr, "Configuration Error: %v\n", err)
			os.Exit(2)
		}

		// Configure logger
		logLevel := logLevelFlag
		if logLevel == "" {
			logLevel = cfg.LogLevel
		}

		var level slog.Level
		switch logLevel {
		case "debug":
			level = slog.LevelDebug
		case "warn":
			level = slog.LevelWarn
		case "error":
			level = slog.LevelError
		default:
			level = slog.LevelInfo
		}

		logger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
		slog.SetDefault(logger)

		// Ensure vault directories exist
		if err := vault.EnsureStructure(cfg); err != nil {
			return fmt.Errorf("failed to create vault folder structure: %v", err)
		}

		return nil
	},
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func init() {
	rootCmd.PersistentFlags().StringVar(&vaultFlag, "vault", "", "Path to the Obsidian vault root")
	rootCmd.PersistentFlags().StringVar(&logLevelFlag, "log-level", "", "Logging level (debug, info, warn, error)")
}
