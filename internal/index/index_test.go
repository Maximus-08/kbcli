package index

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/avnis/kb-system/internal/frontmatter"
)

func TestAppendAndRead(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "kb-index-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	indexPath := filepath.Join(tmpDir, "INDEX.md")

	// Append 1
	err = Append(indexPath, Entry{Slug: "attention-mechanisms", Summary: "Summary of attention"})
	if err != nil {
		t.Fatalf("Append 1 failed: %v", err)
	}

	// Append 2 (should sort before 1 alphabetically)
	err = Append(indexPath, Entry{Slug: "alpha-concept", Summary: "Summary of alpha"})
	if err != nil {
		t.Fatalf("Append 2 failed: %v", err)
	}

	// Read and verify
	entries, err := Read(indexPath)
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}

	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}

	// Verify sorting alphabetically by slug
	if entries[0].Slug != "alpha-concept" || entries[0].Summary != "Summary of alpha" {
		t.Errorf("expected first entry to be alpha-concept, got %+v", entries[0])
	}
	if entries[1].Slug != "attention-mechanisms" || entries[1].Summary != "Summary of attention" {
		t.Errorf("expected second entry to be attention-mechanisms, got %+v", entries[1])
	}
}

func TestAppendDuplicateUpdate(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "kb-index-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	indexPath := filepath.Join(tmpDir, "INDEX.md")

	_ = Append(indexPath, Entry{Slug: "attention-mechanisms", Summary: "Original summary"})
	_ = Append(indexPath, Entry{Slug: "attention-mechanisms", Summary: "Updated summary"})

	entries, err := Read(indexPath)
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}

	if len(entries) != 1 {
		t.Fatalf("expected 1 entry (de-duplicated), got %d", len(entries))
	}

	if entries[0].Summary != "Updated summary" {
		t.Errorf("expected updated summary, got '%s'", entries[0].Summary)
	}
}

func TestRemove(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "kb-index-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	indexPath := filepath.Join(tmpDir, "INDEX.md")

	_ = Append(indexPath, Entry{Slug: "slug-a", Summary: "summary a"})
	_ = Append(indexPath, Entry{Slug: "slug-b", Summary: "summary b"})

	err = Remove(indexPath, "slug-a")
	if err != nil {
		t.Fatalf("Remove failed: %v", err)
	}

	entries, err := Read(indexPath)
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}

	if len(entries) != 1 {
		t.Fatalf("expected 1 entry left, got %d", len(entries))
	}

	if entries[0].Slug != "slug-b" {
		t.Errorf("expected slug-b to remain, got '%s'", entries[0].Slug)
	}
}

func TestRebuild(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "kb-index-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	wikiDir := filepath.Join(tmpDir, "wiki")
	os.MkdirAll(wikiDir, 0755)

	indexPath := filepath.Join(wikiDir, "INDEX.md")

	// Create a couple of wiki markdown files
	wf1 := frontmatter.WikiFrontmatter{
		Title:   "Alpha Concept",
		Summary: "Summary alpha",
	}
	content1, _ := frontmatter.Marshal(wf1, "Alpha body")
	os.WriteFile(filepath.Join(wikiDir, "alpha-concept.md"), content1, 0644)

	wf2 := frontmatter.WikiFrontmatter{
		Title:   "Beta Concept",
		Summary: "Summary beta",
	}
	content2, _ := frontmatter.Marshal(wf2, "Beta body")
	os.WriteFile(filepath.Join(wikiDir, "beta-concept.md"), content2, 0644)

	// Rebuild index
	err = Rebuild(wikiDir, indexPath)
	if err != nil {
		t.Fatalf("Rebuild failed: %v", err)
	}

	entries, err := Read(indexPath)
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}

	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}

	if entries[0].Slug != "alpha-concept" || entries[0].Summary != "Summary alpha" {
		t.Errorf("unexpected first entry: %+v", entries[0])
	}
	if entries[1].Slug != "beta-concept" || entries[1].Summary != "Summary beta" {
		t.Errorf("unexpected second entry: %+v", entries[1])
	}
}
