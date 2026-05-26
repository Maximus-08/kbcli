package cli

import (
	"testing"
)

func TestRootCmdAndSubcommands(t *testing.T) {
	// Verify rootCmd has expected settings
	if rootCmd.Use != "kb" {
		t.Errorf("rootCmd.Use = %q, want 'kb'", rootCmd.Use)
	}

	expectedCmds := map[string]bool{
		"cleanup":       true,
		"compact":       true,
		"compile":       true,
		"lint":          true,
		"query":         true,
		"rebuild-index": true,
		"watch":         true,
	}

	subCommands := rootCmd.Commands()
	if len(subCommands) < len(expectedCmds) {
		t.Errorf("expected at least %d subcommands, got %d", len(expectedCmds), len(subCommands))
	}

	foundCmds := make(map[string]bool)
	for _, cmd := range subCommands {
		foundCmds[cmd.Name()] = true
	}

	for expected := range expectedCmds {
		if !foundCmds[expected] {
			t.Errorf("expected subcommand %q was not registered", expected)
		}
	}
}

func TestRootFlags(t *testing.T) {
	// Check persistent flags
	vaultFlag := rootCmd.PersistentFlags().Lookup("vault")
	if vaultFlag == nil {
		t.Error("expected 'vault' persistent flag to be registered")
	}

	logLevelFlag := rootCmd.PersistentFlags().Lookup("log-level")
	if logLevelFlag == nil {
		t.Error("expected 'log-level' persistent flag to be registered")
	}
}
