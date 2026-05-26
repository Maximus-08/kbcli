package linter

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/avnis/kb-system/internal/config"
	"github.com/avnis/kb-system/internal/frontmatter"
	"github.com/avnis/kb-system/internal/index"
	"github.com/avnis/kb-system/internal/vault"
)

type Diagnostic struct {
	File     string // Relative or absolute path
	Severity string // "ERROR" or "WARNING"
	Code     string // e.g. "L001"
	Message  string // Detailed diagnostic description
}

type Linter struct {
	cfg *config.Config
}

func New(cfg *config.Config) *Linter {
	return &Linter{cfg: cfg}
}

var wikilinkRegex = regexp.MustCompile(`\[\[([^\]|#]+)(?:\|[^\]]+)?\]\]`)

func (l *Linter) Run() ([]Diagnostic, error) {
	var diagnostics []Diagnostic

	wikiDir := vault.WikiDir(l.cfg)
	rawDir := vault.RawDir(l.cfg)
	indexPath := vault.IndexPath(l.cfg)

	// Read INDEX.md entries
	indexEntries, err := index.Read(indexPath)
	if err != nil {
		// If INDEX.md doesn't exist, we don't return error but warn, or we can continue
		if !os.IsNotExist(err) {
			return nil, fmt.Errorf("failed to read INDEX.md: %w", err)
		}
	}

	indexSlugs := make(map[string]bool)
	indexSlugToSummary := make(map[string]string)
	for _, entry := range indexEntries {
		indexSlugs[strings.ToLower(entry.Slug)] = true
		indexSlugToSummary[strings.ToLower(entry.Slug)] = entry.Summary
	}

	// 1. Gather all wiki articles
	wikiFiles := make(map[string]string) // lowercase slug -> original filename/path
	var wikiPaths []string
	entries, err := os.ReadDir(wikiDir)
	if err == nil {
		for _, entry := range entries {
			if entry.IsDir() || strings.EqualFold(entry.Name(), "INDEX.md") || filepath.Ext(entry.Name()) != ".md" {
				continue
			}
			slug := strings.TrimSuffix(strings.ToLower(entry.Name()), ".md")
			path := filepath.Join(wikiDir, entry.Name())
			wikiFiles[slug] = path
			wikiPaths = append(wikiPaths, path)
		}
	}

	// Track which sources are referenced by wiki articles
	referencedSources := make(map[string]bool)

	// Lint each wiki article
	for _, path := range wikiPaths {
		relPath, _ := filepath.Rel(l.cfg.VaultKBPath, path)
		content, err := os.ReadFile(path)
		if err != nil {
			diagnostics = append(diagnostics, Diagnostic{
				File:     relPath,
				Severity: "ERROR",
				Code:     "L000",
				Message:  fmt.Sprintf("failed to read file: %v", err),
			})
			continue
		}

		wf, body, err := frontmatter.ParseWiki(content)
		if err != nil {
			diagnostics = append(diagnostics, Diagnostic{
				File:     relPath,
				Severity: "ERROR",
				Code:     "L000",
				Message:  fmt.Sprintf("failed to parse frontmatter: %v", err),
			})
			continue
		}

		// L001: Missing Title
		if strings.TrimSpace(wf.Title) == "" {
			diagnostics = append(diagnostics, Diagnostic{
				File:     relPath,
				Severity: "ERROR",
				Code:     "L001",
				Message:  "Missing 'title' frontmatter field",
			})
		}

		// L002: Missing or Invalid Type
		validTypes := map[string]bool{
			"concept":      true,
			"project":      true,
			"resource":     true,
			"synthesis":    true,
			"query-result": true,
		}
		if wf.Type == "" {
			diagnostics = append(diagnostics, Diagnostic{
				File:     relPath,
				Severity: "ERROR",
				Code:     "L002",
				Message:  "Missing 'type' frontmatter field",
			})
		} else if !validTypes[wf.Type] {
			diagnostics = append(diagnostics, Diagnostic{
				File:     relPath,
				Severity: "ERROR",
				Code:     "L002",
				Message:  fmt.Sprintf("Invalid 'type' value '%s'; must be one of: concept, project, resource, synthesis, query-result", wf.Type),
			})
		}

		// L003: Invalid Tags
		if len(wf.Tags) < 2 || len(wf.Tags) > 5 {
			diagnostics = append(diagnostics, Diagnostic{
				File:     relPath,
				Severity: "WARNING",
				Code:     "L003",
				Message:  fmt.Sprintf("Wiki article should have between 2 and 5 tags, but has %d", len(wf.Tags)),
			})
		}

		// L004: Invalid Dates
		if wf.Created == "" {
			diagnostics = append(diagnostics, Diagnostic{
				File:     relPath,
				Severity: "ERROR",
				Code:     "L004",
				Message:  "Missing 'created' date field",
			})
		} else if _, err := time.Parse("2006-01-02", wf.Created); err != nil {
			diagnostics = append(diagnostics, Diagnostic{
				File:     relPath,
				Severity: "ERROR",
				Code:     "L004",
				Message:  fmt.Sprintf("Invalid 'created' date '%s'; must be in YYYY-MM-DD format", wf.Created),
			})
		}
		if wf.Updated == "" {
			diagnostics = append(diagnostics, Diagnostic{
				File:     relPath,
				Severity: "ERROR",
				Code:     "L004",
				Message:  "Missing 'updated' date field",
			})
		} else if _, err := time.Parse("2006-01-02", wf.Updated); err != nil {
			diagnostics = append(diagnostics, Diagnostic{
				File:     relPath,
				Severity: "ERROR",
				Code:     "L004",
				Message:  fmt.Sprintf("Invalid 'updated' date '%s'; must be in YYYY-MM-DD format", wf.Updated),
			})
		}

		// L005: Invalid Provenance
		validProvenances := map[string]bool{
			"extracted": true,
			"inferred":  true,
			"ambiguous": true,
			"synthesis": true,
		}
		if wf.Provenance == "" {
			diagnostics = append(diagnostics, Diagnostic{
				File:     relPath,
				Severity: "ERROR",
				Code:     "L005",
				Message:  "Missing 'provenance' frontmatter field",
			})
		} else if !validProvenances[wf.Provenance] {
			diagnostics = append(diagnostics, Diagnostic{
				File:     relPath,
				Severity: "ERROR",
				Code:     "L005",
				Message:  fmt.Sprintf("Invalid 'provenance' value '%s'; must be one of: extracted, inferred, ambiguous, synthesis", wf.Provenance),
			})
		}

		// L006: Missing/Empty Summary
		if strings.TrimSpace(wf.Summary) == "" {
			diagnostics = append(diagnostics, Diagnostic{
				File:     relPath,
				Severity: "WARNING",
				Code:     "L006",
				Message:  "Missing 'summary' frontmatter field",
			})
		}

		// Track sources referenced
		for _, src := range wf.Sources {
			// Extract filenames from sources links like [[file.md]]
			cleaned := strings.Trim(src, "[]")
			referencedSources[strings.ToLower(cleaned)] = true
		}

		// L007: Unindexed Wiki Page
		slug := strings.TrimSuffix(strings.ToLower(filepath.Base(path)), ".md")
		if !indexSlugs[slug] {
			diagnostics = append(diagnostics, Diagnostic{
				File:     relPath,
				Severity: "WARNING",
				Code:     "L007",
				Message:  "Wiki page is not listed in INDEX.md",
			})
		}

		// L009: Dead Links inside Body
		matches := wikilinkRegex.FindAllStringSubmatch(body, -1)
		for _, match := range matches {
			if len(match) < 2 {
				continue
			}
			target := strings.TrimSpace(match[1])
			// If target references a source file (ends with .md), skip.
			if strings.HasSuffix(strings.ToLower(target), ".md") {
				continue
			}
			targetSlug := strings.ToLower(target)
			if _, exists := wikiFiles[targetSlug]; !exists {
				diagnostics = append(diagnostics, Diagnostic{
					File:     relPath,
					Severity: "ERROR",
					Code:     "L009",
					Message:  fmt.Sprintf("Dead wikilink: [[%s]] points to a non-existent wiki article", target),
				})
			}
		}

		// L011: Low Information Density (Conversational Filler or Verbose Prose)
		fillers := []string{
			"in this article", "in this section", "in conclusion", "as we can see",
			"it is important to note", "it's important to note", "basically", "in summary",
			"as mentioned earlier", "let's take a look",
		}
		bodyLower := strings.ToLower(body)
		hasFiller := false
		for _, filler := range fillers {
			if strings.Contains(bodyLower, filler) {
				diagnostics = append(diagnostics, Diagnostic{
					File:     relPath,
					Severity: "WARNING",
					Code:     "L011",
					Message:  fmt.Sprintf("Low information density: conversational filler phrase '%s' detected", filler),
				})
				hasFiller = true
				break
			}
		}

		if !hasFiller {
			paragraphs := strings.Split(body, "\n\n")
			for _, para := range paragraphs {
				para = strings.TrimSpace(para)
				if para == "" {
					continue
				}
				// Skip bullet lists, numbered lists, tables, and code blocks as they are structured high-density elements
				if strings.Contains(para, "* ") || strings.Contains(para, "- ") || strings.Contains(para, "1. ") || strings.Contains(para, "|") || strings.Contains(para, "```") {
					continue
				}
				sentences := strings.Split(para, ". ")
				if len(sentences) > 6 {
					diagnostics = append(diagnostics, Diagnostic{
						File:     relPath,
						Severity: "WARNING",
						Code:     "L011",
						Message:  "Low information density: overly verbose paragraph detected (contains more than 6 sentences)",
					})
					break
				}
			}
		}
	}

	// L008: Dangling Index Entries
	relIndexPath, _ := filepath.Rel(l.cfg.VaultKBPath, indexPath)
	for slug, summary := range indexSlugToSummary {
		if _, exists := wikiFiles[slug]; !exists {
			diagnostics = append(diagnostics, Diagnostic{
				File:     relIndexPath,
				Severity: "ERROR",
				Code:     "L008",
				Message:  fmt.Sprintf("Dangling index entry: [[%s]] is listed in INDEX.md but does not exist in the wiki folder", slug),
			})
		} else {
			// Also check if index summary matches the article frontmatter summary
			wikiPath := wikiFiles[slug]
			content, err := os.ReadFile(wikiPath)
			if err == nil {
				wf, _, err := frontmatter.ParseWiki(content)
				if err == nil && wf.Summary != summary {
					diagnostics = append(diagnostics, Diagnostic{
						File:     relIndexPath,
						Severity: "WARNING",
						Code:     "L008",
						Message:  fmt.Sprintf("Index summary mismatch: [[%s]] summary in INDEX.md does not match the article frontmatter summary", slug),
					})
				}
			}
		}
	}

	// L010: Stale Source Status
	rawEntries, err := os.ReadDir(rawDir)
	if err == nil {
		for _, entry := range rawEntries {
			if entry.IsDir() || filepath.Ext(entry.Name()) != ".md" {
				continue
			}

			path := filepath.Join(rawDir, entry.Name())
			relRawPath, _ := filepath.Rel(l.cfg.VaultKBPath, path)

			content, err := os.ReadFile(path)
			if err != nil {
				continue
			}

			sf, _, err := frontmatter.ParseSource(content)
			if err == nil && sf.Status == "compiled" {
				// Check if this source file is referenced by any wiki article
				filename := strings.ToLower(entry.Name())
				if !referencedSources[filename] {
					diagnostics = append(diagnostics, Diagnostic{
						File:     relRawPath,
						Severity: "WARNING",
						Code:     "L010",
						Message:  fmt.Sprintf("Raw source file '%s' is marked as compiled but is not referenced by any wiki article", entry.Name()),
					})
				}
			}
		}
	}

	return diagnostics, nil
}

var fullWikilinkRegex = regexp.MustCompile(`\[\[([^\]|#]+)(#[^\]|]*)?(\|[^\]]*)?\]\]`)

func (l *Linter) FixLinks() (int, error) {
	wikiDir := vault.WikiDir(l.cfg)

	// Gather all existing slugs
	existingSlugs := make(map[string]bool)
	var wikiPaths []string
	entries, err := os.ReadDir(wikiDir)
	if err != nil {
		return 0, fmt.Errorf("failed to read wiki directory: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() || strings.EqualFold(entry.Name(), "INDEX.md") || filepath.Ext(entry.Name()) != ".md" {
			continue
		}
		slug := strings.TrimSuffix(strings.ToLower(entry.Name()), ".md")
		existingSlugs[slug] = true
		wikiPaths = append(wikiPaths, filepath.Join(wikiDir, entry.Name()))
	}

	var fixedCount int

	for _, path := range wikiPaths {
		content, err := os.ReadFile(path)
		if err != nil {
			continue
		}

		wf, body, err := frontmatter.ParseWiki(content)
		if err != nil {
			continue
		}

		modified := false
		newBody := fullWikilinkRegex.ReplaceAllStringFunc(body, func(match string) string {
			submatches := fullWikilinkRegex.FindStringSubmatch(match)
			if len(submatches) < 2 {
				return match
			}

			target := strings.TrimSpace(submatches[1])
			header := submatches[2]
			alias := submatches[3]

			resolvedSlug, resolved := vault.ResolveWikiLink(target, existingSlugs)
			if !resolved {
				return match
			}

			// Construct replacement link
			var replacement string
			if alias != "" {
				replacement = fmt.Sprintf("[[%s%s%s]]", resolvedSlug, header, alias)
			} else if target == resolvedSlug {
				replacement = fmt.Sprintf("[[%s%s]]", resolvedSlug, header)
			} else {
				replacement = fmt.Sprintf("[[%s%s|%s]]", resolvedSlug, header, target)
			}

			if replacement != match {
				modified = true
				fixedCount++
			}
			return replacement
		})

		if modified {
			// Write the modified content back atomically
			tmpPath := path + ".tmp"
			newContent, err := frontmatter.Marshal(wf, newBody)
			if err != nil {
				continue
			}

			if err := os.WriteFile(tmpPath, newContent, 0644); err != nil {
				continue
			}

			if err := os.Rename(tmpPath, path); err != nil {
				os.Remove(tmpPath)
				continue
			}
		}
	}

	return fixedCount, nil
}
