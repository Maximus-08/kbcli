package cli

import (
	"fmt"
	"os"

	"github.com/avnis/kb-system/internal/linter"
	"github.com/spf13/cobra"
)

var (
	lintFix bool
)

var lintCmd = &cobra.Command{
	Use:   "lint",
	Short: "Check knowledge base consistency, frontmatter metadata, and link validity",
	RunE: func(cmd *cobra.Command, args []string) error {
		l := linter.New(cfg)

		if lintFix {
			logger.Info("Executing automatic link resolution and repairs...")
			fixed, err := l.FixLinks()
			if err != nil {
				logger.Error("Failed to auto-fix links", "error", err)
			} else {
				logger.Info("Automatic link resolution complete", "linksFixed", fixed)
				fmt.Printf("Auto-fixed %d link(s).\n\n", fixed)
			}
		}

		diagnostics, err := l.Run()
		if err != nil {
			return err
		}

		var errorCount, warnCount int
		for _, d := range diagnostics {
			if d.Severity == "ERROR" {
				errorCount++
			} else {
				warnCount++
			}

			fmt.Printf("[%s] %s  %s: %s\n", d.Severity, d.Code, d.File, d.Message)
		}

		if len(diagnostics) > 0 {
			fmt.Printf("\nLint complete: %d error(s), %d warning(s) found.\n", errorCount, warnCount)
		} else {
			fmt.Println("Lint complete: No issues found.")
		}

		if errorCount > 0 {
			os.Exit(1)
		}

		return nil
	},
}

func init() {
	lintCmd.Flags().BoolVar(&lintFix, "fix", false, "Automatically fix case/format mismatches and raw source file dead links in articles")
	rootCmd.AddCommand(lintCmd)
}
