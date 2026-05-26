package linter

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/avnis/kb-system/internal/config"
	"github.com/avnis/kb-system/internal/frontmatter"
	"github.com/avnis/kb-system/internal/index"
	"github.com/avnis/kb-system/internal/vault"
)

func TestLinterDiagnostics(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "kb-linter-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	cfg := &config.Config{
		VaultKBPath: tmpDir,
	}

	err = vault.EnsureStructure(cfg)
	if err != nil {
		t.Fatalf("failed to build vault structure: %v", err)
	}

	// 1. Create a valid wiki article (so we have a baseline)
	validWF := frontmatter.WikiFrontmatter{
		Title:      "Attention Mechanisms",
		Type:       "concept",
		Tags:       []string{"deep-learning", "attention"},
		Created:    "2026-05-21",
		Updated:    "2026-05-21",
		Sources:    []string{"[[transformer-concept.md]]"},
		Provenance: "extracted",
		Summary:    "An overview of attention mechanism.",
	}
	validContent, _ := frontmatter.Marshal(validWF, "This is a body referencing [[missing-article]].")
	err = os.WriteFile(filepath.Join(vault.WikiDir(cfg), "attention-mechanisms.md"), validContent, 0644)
	if err != nil {
		t.Fatalf("failed to write valid wiki file: %v", err)
	}

	// 2. Create an invalid wiki article (with multiple frontmatter errors: L001, L002, L003, L004, L005, L006)
	invalidWF := frontmatter.WikiFrontmatter{
		Title:      "",                       // L001
		Type:       "invalid-type",           // L002
		Tags:       []string{"one-tag-only"}, // L003
		Created:    "invalid-date",           // L004
		Updated:    "2026-05-21",
		Provenance: "invalid-prov", // L005
		Summary:    "",             // L006
	}
	invalidContent, _ := frontmatter.Marshal(invalidWF, "Body text")
	err = os.WriteFile(filepath.Join(vault.WikiDir(cfg), "invalid-article.md"), invalidContent, 0644)
	if err != nil {
		t.Fatalf("failed to write invalid wiki file: %v", err)
	}

	// 3. Write INDEX.md with a dangling index entry (L008) and missing the valid/invalid articles (L007)
	// Let's add a dangling slug
	indexPath := vault.IndexPath(cfg)
	_ = index.Append(indexPath, index.Entry{Slug: "dangling-slug", Summary: "This slug has no file."})
	_ = index.Append(indexPath, index.Entry{Slug: "attention-mechanisms", Summary: "Mismatched summary to trigger mismatch warning"})

	// 4. Create a stale compiled source (L010)
	sf := frontmatter.SourceFrontmatter{
		Title:  "Stale Source",
		Status: "compiled",
	}
	sfContent, _ := frontmatter.Marshal(sf, "Raw body")
	err = os.WriteFile(filepath.Join(vault.RawDir(cfg), "stale-source.md"), sfContent, 0644)
	if err != nil {
		t.Fatalf("failed to write source file: %v", err)
	}

	// Run Linter
	l := New(cfg)
	diagnostics, err := l.Run()
	if err != nil {
		t.Fatalf("linter execution failed: %v", err)
	}

	codes := make(map[string]int)
	for _, d := range diagnostics {
		codes[d.Code]++
	}

	expectedCodes := []string{"L001", "L002", "L003", "L004", "L005", "L006", "L007", "L008", "L009", "L010"}
	for _, code := range expectedCodes {
		if codes[code] == 0 {
			t.Errorf("Expected diagnostic code %s was not triggered in diagnostics list: %+v", code, diagnostics)
		}
	}
}
