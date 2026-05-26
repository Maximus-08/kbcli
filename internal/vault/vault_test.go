package vault

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/avnis/kb-system/internal/config"
)

func TestPathHelpers(t *testing.T) {
	cfg := &config.Config{
		VaultKBPath: "/dummy/vault",
	}

	expectedRaw := filepath.Clean("/dummy/vault/sources/raw")
	if got := filepath.Clean(RawDir(cfg)); got != expectedRaw {
		t.Errorf("RawDir got %s, want %s", got, expectedRaw)
	}

	expectedWiki := filepath.Clean("/dummy/vault/wiki")
	if got := filepath.Clean(WikiDir(cfg)); got != expectedWiki {
		t.Errorf("WikiDir got %s, want %s", got, expectedWiki)
	}

	expectedIndex := filepath.Clean("/dummy/vault/wiki/INDEX.md")
	if got := filepath.Clean(IndexPath(cfg)); got != expectedIndex {
		t.Errorf("IndexPath got %s, want %s", got, expectedIndex)
	}

	expectedWikiFile := filepath.Clean("/dummy/vault/wiki/my-slug.md")
	if got := filepath.Clean(WikiFilePath(cfg, "my-slug")); got != expectedWikiFile {
		t.Errorf("WikiFilePath got %s, want %s", got, expectedWikiFile)
	}
}

func TestEnsureStructure(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "kb_vault_structure_test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	cfg := &config.Config{
		VaultKBPath: tempDir,
	}

	if err := EnsureStructure(cfg); err != nil {
		t.Fatalf("EnsureStructure failed: %v", err)
	}

	// Verify that directories were created
	raw := RawDir(cfg)
	wiki := WikiDir(cfg)

	if info, err := os.Stat(raw); err != nil || !info.IsDir() {
		t.Errorf("expected raw directory to exist, err: %v", err)
	}
	if info, err := os.Stat(wiki); err != nil || !info.IsDir() {
		t.Errorf("expected wiki directory to exist, err: %v", err)
	}
}

func TestMakeSlug(t *testing.T) {
	tests := []struct {
		title    string
		expected string
	}{
		{"Hello World", "hello-world"},
		{"Quantum Computing Foundations 101", "quantum-computing-foundations-101"},
		{"Machine Learning & Deep Neural Networks!!!", "machine-learning-deep-neural-networks"},
		{"  Trim spaces  ", "trim-spaces"},
		{"---Already Hyphenated---", "already-hyphenated"},
		{"Q&A Section", "q-a-section"},
		{"Single", "single"},
		{"Unicode 🚀 Stars 🌟", "unicode-stars"},
	}

	for _, tc := range tests {
		t.Run(tc.title, func(t *testing.T) {
			if got := MakeSlug(tc.title); got != tc.expected {
				t.Errorf("MakeSlug(%q) = %q; want %q", tc.title, got, tc.expected)
			}
		})
	}
}

func TestResolveWikiLink(t *testing.T) {
	existingSlugs := map[string]bool{
		"quantum-convolutional-neural-networks-qcnns":              true,
		"quantum-computing-foundation":                             true,
		"quantum-fourier-transform":                                true,
		"quantum-entanglement-separable-vs-entangled-states":       true,
		"quantum-teleportation-protocol":                           true,
		"matrix-product-state-quantum-contrastive-learning-mpsqcl": true,
		"deep-learning-transformers":                               true,
	}

	tests := []struct {
		target   string
		expected string
		found    bool
	}{
		// 1. Direct default mapping match (from defaultMappings map)
		{"qcnns", "quantum-convolutional-neural-networks-qcnns", true},
		{"foundation", "quantum-computing-foundation", true},
		{"qft", "quantum-fourier-transform", true},
		{"quantum_teleportation", "quantum-teleportation-protocol", true},

		// 2. Direct slug matches
		{"Quantum Computing Foundation", "quantum-computing-foundation", true},
		{"deep-learning-transformers.md", "deep-learning-transformers", true},

		// 3. Hyphenated and casing fallback matches
		{"quantum computing foundation", "quantum-computing-foundation", true},
		{"quantum_computing_foundation", "quantum-computing-foundation", true},

		// 4. Prefix/partial fallback matches
		{"deep-learning-transf", "deep-learning-transformers", true},
		{"transformers", "deep-learning-transformers", true}, // containing slug

		// 5. Unmatched cases
		{"completely-unmatched-slug", "", false},
		{"", "", false},
		{"   ", "", false},
	}

	for _, tc := range tests {
		t.Run(tc.target, func(t *testing.T) {
			got, found := ResolveWikiLink(tc.target, existingSlugs)
			if found != tc.found {
				t.Errorf("ResolveWikiLink(%q) found = %v; want %v", tc.target, found, tc.found)
			}
			if got != tc.expected {
				t.Errorf("ResolveWikiLink(%q) slug = %q; want %q", tc.target, got, tc.expected)
			}
		})
	}
}
