package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/avnis/kb-system/internal/compiler"
	"github.com/avnis/kb-system/internal/frontmatter"
	"github.com/avnis/kb-system/internal/provider"
	"github.com/avnis/kb-system/internal/vault"
	"github.com/spf13/cobra"
)

var (
	compileAll    bool
	compileForce  bool
	compileMulti  bool
	compileDryRun bool
	compileSplit  bool
)

var compileCmd = &cobra.Command{
	Use:   "compile [file...]",
	Short: "Compile one or more raw sources into wiki articles",
	RunE: func(cmd *cobra.Command, args []string) error {
		var targets []string

		if compileAll {
			rawDir := vault.RawDir(cfg)
			entries, err := os.ReadDir(rawDir)
			if err != nil {
				return fmt.Errorf("failed to read raw directory: %w", err)
			}

			for _, entry := range entries {
				if entry.IsDir() || filepath.Ext(entry.Name()) != ".md" {
					continue
				}

				p := filepath.Join(rawDir, entry.Name())
				if compileForce {
					targets = append(targets, p)
					continue
				}

				// Read and check status
				content, err := os.ReadFile(p)
				if err != nil {
					continue
				}
				sf, _, err := frontmatter.ParseSource(content)
				if err == nil && (sf.Status == "uncompiled" || sf.Status == "") {
					targets = append(targets, p)
				}
			}
		} else {
			for _, arg := range args {
				absArg, err := filepath.Abs(arg)
				if err != nil {
					return fmt.Errorf("invalid path %s: %w", arg, err)
				}
				targets = append(targets, absArg)
			}
		}

		if len(targets) == 0 {
			logger.Info("No sources to compile")
			return nil
		}

		if compileDryRun {
			logger.Info("[DRY-RUN] Would compile the following raw documents:")
			for _, t := range targets {
				fmt.Printf("- %s\n", filepath.Base(t))
			}
			return nil
		}

		// Initialize LLM provider
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

		if compileMulti {
			if err := c.CompileMulti(targets, compileForce); err != nil {
				return err
			}
		} else {
			var failedCount int
			for _, t := range targets {
				if err := c.CompileSingle(t, compileForce, compileSplit); err != nil {
					logger.Error("Compilation failed for file", "file", filepath.Base(t), "error", err)
					failedCount++
				}
			}
			if failedCount > 0 {
				return fmt.Errorf("%d compilation(s) failed", failedCount)
			}
		}

		return nil
	},
}

func init() {
	compileCmd.Flags().BoolVarP(&compileAll, "all", "a", false, "Compile all uncompiled sources in raw/ directory")
	compileCmd.Flags().BoolVarP(&compileForce, "force", "f", false, "Force compilation even if already compiled")
	compileCmd.Flags().BoolVarP(&compileMulti, "multi", "m", false, "Combine multiple files into a single wiki article")
	compileCmd.Flags().BoolVar(&compileDryRun, "dry-run", false, "List targets without compiling them")
	compileCmd.Flags().BoolVarP(&compileSplit, "split", "s", false, "Deep compile by dynamically splitting large/complex documents into atomic sub-topic articles linked via a Hub-and-Spoke structure")
	rootCmd.AddCommand(compileCmd)
}
