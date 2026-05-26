package compiler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"text/template"
	"time"

	"github.com/avnis/kb-system/internal/cleaner"
	"github.com/avnis/kb-system/internal/config"
	"github.com/avnis/kb-system/internal/frontmatter"
	"github.com/avnis/kb-system/internal/index"
	"github.com/avnis/kb-system/internal/provider"
	"github.com/avnis/kb-system/internal/vault"
	"github.com/avnis/kb-system/prompts"
	"github.com/ledongthuc/pdf"
)

type Compiler struct {
	cfg      *config.Config
	provider provider.Provider
	logger   *slog.Logger
}

type compileResponse struct {
	Title      string   `json:"title"`
	Type       string   `json:"type"`
	Tags       []string `json:"tags"`
	Provenance string   `json:"provenance"`
	Summary    string   `json:"summary"`
	Body       string   `json:"body"`
}

func New(cfg *config.Config, provider provider.Provider, logger *slog.Logger) *Compiler {
	return &Compiler{
		cfg:      cfg,
		provider: provider,
		logger:   logger,
	}
}

// CompileSingle compiles one raw source file into a wiki article, optionally splitting it.
func (c *Compiler) CompileSingle(sourcePath string, force bool, split bool) error {
	c.logger.Info("Starting compilation of source file", "path", sourcePath, "split", split)

	var sf *frontmatter.SourceFrontmatter
	var body string
	var content []byte
	var err error

	isPDF := strings.ToLower(filepath.Ext(sourcePath)) == ".pdf"
	if isPDF {
		c.logger.Info("Extracting text from PDF source file", "path", sourcePath)
		pdfText, err := readPDFText(sourcePath)
		if err != nil {
			return fmt.Errorf("failed to extract text from PDF: %w", err)
		}
		body = pdfText
		content = []byte(pdfText)
		sf = &frontmatter.SourceFrontmatter{
			Type:   "paper",
			Status: "uncompiled",
		}
	} else {
		content, err = os.ReadFile(sourcePath)
		if err != nil {
			return fmt.Errorf("failed to read source file: %w", err)
		}

		sf, body, err = frontmatter.ParseSource(content)
		if err != nil {
			return fmt.Errorf("failed to parse source frontmatter: %w", err)
		}
	}

	if sf.Title == "" {
		sf.Title = strings.TrimSuffix(filepath.Base(sourcePath), filepath.Ext(sourcePath))
		c.logger.Info("Source file lacks title, using filename as title", "title", sf.Title, "path", sourcePath)
	}

	if len(strings.TrimSpace(body)) < 50 {
		c.logger.Warn("Source file body is too short (< 50 chars), skipping", "path", sourcePath)
		return nil
	}

	if sf.Status == "compiled" && !force {
		c.logger.Info("Source already compiled, skipping", "path", sourcePath)
		return nil
	}

	if split {
		err := c.compileSingleWithSplit(sourcePath, sf, body, force)
		if err == nil {
			return nil
		}
		c.logger.Error("Split compilation failed, falling back to standard compilation", "error", err)
	}

	// Load Existing Articles from INDEX.md
	indexPath := vault.IndexPath(c.cfg)
	indexEntries, err := index.Read(indexPath)
	var existingArticlesStr string
	if err == nil && len(indexEntries) > 0 {
		var existingArticles []string
		for _, entry := range indexEntries {
			existingArticles = append(existingArticles, fmt.Sprintf("- [[%s]]: %s", entry.Slug, entry.Summary))
		}
		existingArticlesStr = strings.Join(existingArticles, "\n")
	}

	// Stage 1: Deterministic Similarity Pre-Filtering
	overlap := false
	if len(indexEntries) > 0 {
		overlap = hasPotentialOverlap(sf.Title, indexEntries)
	}

	action := "create"
	targetSlug := ""

	if overlap {
		c.logger.Info("Potential keyword overlap detected. Running Stage 2 Ingestion Analysis...")
		action, targetSlug, err = c.analyzeIngestion(content, existingArticlesStr)
		if err != nil {
			c.logger.Warn("Ingestion analysis failed, falling back to 'create'", "error", err)
			action = "create"
		}
	} else {
		c.logger.Info("No keyword overlap detected. Bypassing Ingestion Analysis and choosing 'create'.")
	}

	// Stage 3: Ingestion Execution
	if action == "skip" {
		c.logger.Info("LLM Ingestion Analysis determined document is redundant. Skipping.", "path", sourcePath)
		if !isPDF {
			sf.Status = "compiled"
			updatedSourceContent, err := frontmatter.Marshal(sf, body)
			if err == nil {
				_ = os.WriteFile(sourcePath, updatedSourceContent, 0644)
			}
		}
		return nil
	}

	if action == "expand" && targetSlug != "" {
		c.logger.Info("LLM Ingestion Analysis determined document should expand existing article.", "target", targetSlug)
		return c.compileExpand(sourcePath, sf, body, targetSlug, existingArticlesStr)
	}

	if action == "synthesize_and_split" && targetSlug != "" {
		c.logger.Info("LLM Ingestion Analysis determined document should be synthesized and split.", "target", targetSlug)
		return c.compileSynthesizeAndSplit(sourcePath, sf, body, targetSlug, existingArticlesStr)
	}

	// Default: action == "create"
	// Render compile single prompt template
	tmpl, err := template.New("single").Parse(prompts.CompileSingle)
	if err != nil {
		return fmt.Errorf("failed to parse prompt template: %w", err)
	}

	var promptBuf bytes.Buffer
	err = tmpl.Execute(&promptBuf, map[string]any{
		"SourceContent":    string(content),
		"ExistingArticles": existingArticlesStr,
	})
	if err != nil {
		return fmt.Errorf("failed to execute prompt template: %w", err)
	}

	var llmResp string
	// Retrying once on malformed response
	for attempt := 1; attempt <= 2; attempt++ {
		c.logger.Debug("Sending compile request to model", "model", c.cfg.CompileModelSingle, "attempt", attempt)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		llmResp, err = c.provider.Generate(ctx, c.cfg.CompileModelSingle, promptBuf.String())
		cancel()

		if err != nil {
			c.logger.Warn("Model generation failed on attempt", "attempt", attempt, "error", err)
			continue
		}

		res, err := parseJSONResponse(llmResp, c.logger)
		if err == nil && res.Title != "" && res.Summary != "" && res.Body != "" {
			// Success! Make slug and handle collision based on LLM-generated title
			slug := vault.MakeSlug(res.Title)
			slug = c.resolveCollision(slug, sourcePath)
			return c.writeWikiArticle(sourcePath, sf, body, res, slug)
		}

		c.logger.Warn("Received malformed JSON from model, retrying", "attempt", attempt, "error", err)
	}

	return fmt.Errorf("failed to compile after 2 attempts due to model timeouts or malformed responses")
}

type splitPlan struct {
	SplitRequired bool           `json:"split_required"`
	Articles      []splitArticle `json:"articles"`
}

type splitArticle struct {
	Slug           string   `json:"slug"`
	Title          string   `json:"title"`
	Type           string   `json:"type"`
	Summary        string   `json:"summary"`
	Instructions   string   `json:"instructions"`
	DependentSlugs []string `json:"dependent_slugs"`
}

func (c *Compiler) compileSingleWithSplit(sourcePath string, sf *frontmatter.SourceFrontmatter, body string, force bool) error {
	c.logger.Info("Starting LLM-guided split compilation analysis", "path", sourcePath)

	// Read raw content (or use body if PDF to avoid reading binary data)
	var content []byte
	var err error
	isPDF := strings.ToLower(filepath.Ext(sourcePath)) == ".pdf"
	if isPDF {
		content = []byte(body)
	} else {
		content, err = os.ReadFile(sourcePath)
		if err != nil {
			return err
		}
	}

	// Load Existing Articles from INDEX.md
	indexPath := vault.IndexPath(c.cfg)
	indexEntries, err := index.Read(indexPath)
	var existingArticlesStr string
	if err == nil && len(indexEntries) > 0 {
		var existingArticles []string
		for _, entry := range indexEntries {
			existingArticles = append(existingArticles, fmt.Sprintf("- [[%s]]: %s", entry.Slug, entry.Summary))
		}
		existingArticlesStr = strings.Join(existingArticles, "\n")
	}

	// Render split planning prompt template
	tmpl, err := template.New("split_plan").Parse(prompts.SplitPlan)
	if err != nil {
		return fmt.Errorf("failed to parse split plan template: %w", err)
	}

	var promptBuf bytes.Buffer
	err = tmpl.Execute(&promptBuf, map[string]any{
		"SourceContent": string(content),
	})
	if err != nil {
		return fmt.Errorf("failed to execute split plan template: %w", err)
	}

	var plan *splitPlan
	for attempt := 1; attempt <= 2; attempt++ {
		c.logger.Debug("Sending split plan request to model", "attempt", attempt)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		llmResp, err := c.provider.Generate(ctx, c.cfg.CompileModelSingle, promptBuf.String())
		cancel()

		if err != nil {
			c.logger.Warn("Split planning generation failed on attempt", "attempt", attempt, "error", err)
			continue
		}

		plan, err = parseSplitPlanResponse(llmResp)
		if err == nil {
			break
		}
		c.logger.Warn("Received malformed JSON for split plan, retrying", "attempt", attempt, "error", err)
	}

	if plan == nil {
		return fmt.Errorf("failed to generate split plan after 2 attempts")
	}

	if !plan.SplitRequired || len(plan.Articles) == 0 {
		c.logger.Info("Split planner determined splitting is not required for this file. Falling back to single compilation.")
		return fmt.Errorf("splitting not required")
	}

	c.logger.Info("Split compilation plan generated successfully", "articlesCount", len(plan.Articles))

	// Compile Spokes
	var compiledSpokes []splitArticle
	for _, spoke := range plan.Articles {
		spokeSlug := vault.MakeSlug(spoke.Slug)
		if spokeSlug == "" {
			spokeSlug = vault.MakeSlug(spoke.Title)
		}
		spokeSlug = c.resolveCollision(spokeSlug, sourcePath)

		c.logger.Info("Compiling Spoke article", "title", spoke.Title, "slug", spokeSlug)

		// Render compile spoke prompt template
		spokeTmpl, err := template.New("compile_spoke").Parse(prompts.CompileSpoke)
		if err != nil {
			return fmt.Errorf("failed to parse compile spoke template: %w", err)
		}

		var spokeBuf bytes.Buffer
		err = spokeTmpl.Execute(&spokeBuf, map[string]any{
			"SourceContent":     string(content),
			"SpokeTitle":        spoke.Title,
			"SpokeSummary":      spoke.Summary,
			"SpokeInstructions": spoke.Instructions,
			"DependentLinks":    spoke.DependentSlugs,
			"ExistingArticles":  existingArticlesStr,
		})
		if err != nil {
			return fmt.Errorf("failed to execute compile spoke template: %w", err)
		}

		var spokeRes *compileResponse
		for attempt := 1; attempt <= 2; attempt++ {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
			llmResp, err := c.provider.Generate(ctx, c.cfg.CompileModelSingle, spokeBuf.String())
			cancel()

			if err != nil {
				c.logger.Warn("Spoke compilation failed on attempt", "title", spoke.Title, "attempt", attempt, "error", err)
				continue
			}

			spokeRes, err = parseJSONResponse(llmResp, c.logger)
			if err == nil && spokeRes.Title != "" && spokeRes.Summary != "" && spokeRes.Body != "" {
				break
			}
			c.logger.Warn("Received malformed JSON for Spoke, retrying", "title", spoke.Title, "attempt", attempt, "error", err)
		}

		if spokeRes == nil {
			return fmt.Errorf("failed to compile Spoke article '%s' after 2 attempts", spoke.Title)
		}

		// Write Spoke article
		spokeWikiPath := vault.WikiFilePath(c.cfg, spokeSlug)
		spokeTmpPath := spokeWikiPath + ".tmp"

		spokeWf := frontmatter.WikiFrontmatter{
			Title:        spokeRes.Title,
			Type:         spokeRes.Type,
			Tags:         spokeRes.Tags,
			Created:      time.Now().Format("2006-01-02"),
			Updated:      time.Now().Format("2006-01-02"),
			Sources:      []string{fmt.Sprintf("[[%s]]", filepath.Base(sourcePath))},
			Provenance:   spokeRes.Provenance,
			Summary:      spokeRes.Summary,
			CompiledFrom: filepath.Base(sourcePath),
			Related:      spoke.DependentSlugs,
		}

		normalizedBody := c.normalizeLinks(spokeRes.Body)
		spokeWikiContent, err := frontmatter.Marshal(spokeWf, normalizedBody)
		if err != nil {
			return fmt.Errorf("failed to marshal Spoke wiki frontmatter: %w", err)
		}

		if err := os.WriteFile(spokeTmpPath, spokeWikiContent, 0644); err != nil {
			return fmt.Errorf("failed to write tmp Spoke wiki file: %w", err)
		}

		if err := os.Rename(spokeTmpPath, spokeWikiPath); err != nil {
			os.Remove(spokeTmpPath)
			return fmt.Errorf("failed to rename tmp Spoke wiki file: %w", err)
		}

		// Append to INDEX.md
		indexPath := vault.IndexPath(c.cfg)
		if err := index.Append(indexPath, index.Entry{Slug: spokeSlug, Summary: spokeRes.Summary}); err != nil {
			return fmt.Errorf("failed to update index for Spoke: %w", err)
		}

		// Keep track of compiled spoke info with its final slug
		compiledSpokes = append(compiledSpokes, splitArticle{
			Slug:    spokeSlug,
			Title:   spokeRes.Title,
			Summary: spokeRes.Summary,
		})
	}

	// Compile Hub
	c.logger.Info("Compiling master Hub article", "title", sf.Title)

	hubTmpl, err := template.New("compile_hub").Parse(prompts.CompileHub)
	if err != nil {
		return fmt.Errorf("failed to parse compile hub template: %w", err)
	}

	var hubBuf bytes.Buffer
	err = hubTmpl.Execute(&hubBuf, map[string]any{
		"SourceContent":    string(content),
		"Spokes":           compiledSpokes,
		"ExistingArticles": existingArticlesStr,
	})
	if err != nil {
		return fmt.Errorf("failed to execute compile hub template: %w", err)
	}

	var hubRes *compileResponse
	for attempt := 1; attempt <= 2; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		llmResp, err := c.provider.Generate(ctx, c.cfg.CompileModelSingle, hubBuf.String())
		cancel()

		if err != nil {
			c.logger.Warn("Hub compilation failed on attempt", "attempt", attempt, "error", err)
			continue
		}

		hubRes, err = parseJSONResponse(llmResp, c.logger)
		if err == nil && hubRes.Title != "" && hubRes.Summary != "" && hubRes.Body != "" {
			break
		}
		c.logger.Warn("Received malformed JSON for Hub, retrying", "attempt", attempt, "error", err)
	}

	if hubRes == nil {
		return fmt.Errorf("failed to compile Hub article after 2 attempts")
	}

	// Hub slug and write
	hubSlug := vault.MakeSlug(sf.Title)
	hubSlug = c.resolveCollision(hubSlug, sourcePath)

	hubWikiPath := vault.WikiFilePath(c.cfg, hubSlug)
	hubTmpPath := hubWikiPath + ".tmp"

	hubWf := frontmatter.WikiFrontmatter{
		Title:        hubRes.Title,
		Type:         hubRes.Type,
		Tags:         hubRes.Tags,
		Created:      time.Now().Format("2006-01-02"),
		Updated:      time.Now().Format("2006-01-02"),
		Sources:      []string{fmt.Sprintf("[[%s]]", filepath.Base(sourcePath))},
		Provenance:   hubRes.Provenance,
		Summary:      hubRes.Summary,
		CompiledFrom: filepath.Base(sourcePath),
	}

	normalizedBody := c.normalizeLinks(hubRes.Body)
	hubWikiContent, err := frontmatter.Marshal(hubWf, normalizedBody)
	if err != nil {
		return fmt.Errorf("failed to marshal Hub wiki frontmatter: %w", err)
	}

	if err := os.WriteFile(hubTmpPath, hubWikiContent, 0644); err != nil {
		return fmt.Errorf("failed to write tmp Hub wiki file: %w", err)
	}

	if err := os.Rename(hubTmpPath, hubWikiPath); err != nil {
		os.Remove(hubTmpPath)
		return fmt.Errorf("failed to rename tmp Hub wiki file: %w", err)
	}

	// Append Hub to INDEX.md
	indexPath = vault.IndexPath(c.cfg)
	if err := index.Append(indexPath, index.Entry{Slug: hubSlug, Summary: hubRes.Summary}); err != nil {
		return fmt.Errorf("failed to update index for Hub: %w", err)
	}

	// Mark the source file as compiled
	if !isPDF {
		sf.Status = "compiled"
		updatedSourceContent, err := frontmatter.Marshal(sf, body)
		if err == nil {
			_ = os.WriteFile(sourcePath, updatedSourceContent, 0644)
		}
	}

	c.logger.Info("Split compilation completed successfully", "hubSlug", hubSlug, "spokesCount", len(compiledSpokes))
	return nil
}

// CompileMulti compiles multiple raw sources into one wiki article.
func (c *Compiler) CompileMulti(sourcePaths []string, force bool) error {
	c.logger.Info("Starting multi-document synthesis", "filesCount", len(sourcePaths))

	// Load Existing Articles from INDEX.md
	indexPath := vault.IndexPath(c.cfg)
	indexEntries, err := index.Read(indexPath)
	var existingArticlesStr string
	if err == nil && len(indexEntries) > 0 {
		var existingArticles []string
		for _, entry := range indexEntries {
			existingArticles = append(existingArticles, fmt.Sprintf("- [[%s]]: %s", entry.Slug, entry.Summary))
		}
		existingArticlesStr = strings.Join(existingArticles, "\n")
	}

	var sourcesText []string
	var alreadyCompiledCount int

	for _, p := range sourcePaths {
		var contentStr string
		var isCompiled bool

		if strings.ToLower(filepath.Ext(p)) == ".pdf" {
			c.logger.Info("Extracting text from PDF source file for multi-doc synthesis", "path", p)
			pdfText, err := readPDFText(p)
			if err != nil {
				return fmt.Errorf("failed to extract text from PDF %s: %w", p, err)
			}
			contentStr = pdfText
			isCompiled = false // PDFs cannot be marked compiled in-place
		} else {
			content, err := os.ReadFile(p)
			if err != nil {
				return fmt.Errorf("failed to read source file %s: %w", p, err)
			}

			sf, _, err := frontmatter.ParseSource(content)
			if err != nil {
				return fmt.Errorf("failed to parse source %s: %w", p, err)
			}

			if sf.Status == "compiled" {
				isCompiled = true
			}
			contentStr = string(content)
		}

		if isCompiled {
			alreadyCompiledCount++
		}

		sourcesText = append(sourcesText, contentStr)
	}

	if alreadyCompiledCount == len(sourcePaths) && !force {
		c.logger.Info("All source documents are already compiled, skipping multi-doc compilation")
		return nil
	}

	// Render prompt template
	tmpl, err := template.New("multi").Parse(prompts.CompileMulti)
	if err != nil {
		return fmt.Errorf("failed to parse multi prompt template: %w", err)
	}

	var promptBuf bytes.Buffer
	err = tmpl.Execute(&promptBuf, map[string]any{
		"Sources":          sourcesText,
		"ExistingArticles": existingArticlesStr,
	})
	if err != nil {
		return fmt.Errorf("failed to execute multi prompt template: %w", err)
	}

	var llmResp string
	var res *compileResponse
	// Retrying once on malformed response
	for attempt := 1; attempt <= 2; attempt++ {
		c.logger.Debug("Sending multi-doc compile request to model", "model", c.cfg.CompileModelMulti, "attempt", attempt)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		llmResp, err = c.provider.Generate(ctx, c.cfg.CompileModelMulti, promptBuf.String())
		cancel()

		if err != nil {
			c.logger.Warn("Multi-doc model generation failed on attempt", "attempt", attempt, "error", err)
			continue
		}

		res, err = parseJSONResponse(llmResp, c.logger)
		if err == nil && res.Title != "" && res.Summary != "" && res.Body != "" {
			break
		}

		c.logger.Warn("Received malformed JSON from multi-doc model, retrying", "attempt", attempt, "error", err)
	}

	if res == nil {
		return fmt.Errorf("failed to compile multi-doc after 2 attempts due to malformed model response")
	}

	// Slug and write
	slug := vault.MakeSlug(res.Title)
	slug = c.resolveCollision(slug, "")

	wikiPath := vault.WikiFilePath(c.cfg, slug)
	tmpPath := wikiPath + ".tmp"

	// Build sources wikilink list
	var sourceLinks []string
	for _, p := range sourcePaths {
		sourceLinks = append(sourceLinks, fmt.Sprintf("[[%s]]", filepath.Base(p)))
	}

	wf := frontmatter.WikiFrontmatter{
		Title:        res.Title,
		Type:         res.Type,
		Tags:         res.Tags,
		Created:      time.Now().Format("2006-01-02"),
		Updated:      time.Now().Format("2006-01-02"),
		Sources:      sourceLinks,
		Provenance:   res.Provenance,
		Summary:      res.Summary,
		CompiledFrom: "multi-doc",
	}

	normalizedBody := c.normalizeLinks(res.Body)
	wikiContent, err := frontmatter.Marshal(wf, normalizedBody)
	if err != nil {
		return fmt.Errorf("failed to marshal wiki frontmatter: %w", err)
	}

	// Atomic write
	if err := os.WriteFile(tmpPath, wikiContent, 0644); err != nil {
		return fmt.Errorf("failed to write tmp wiki file: %w", err)
	}

	if err := os.Rename(tmpPath, wikiPath); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("failed to rename tmp wiki file: %w", err)
	}

	// Append to INDEX.md
	indexPath = vault.IndexPath(c.cfg)
	if err := index.Append(indexPath, index.Entry{Slug: slug, Summary: res.Summary}); err != nil {
		return fmt.Errorf("failed to update index: %w", err)
	}

	// Mark all sources as compiled
	for _, p := range sourcePaths {
		if strings.ToLower(filepath.Ext(p)) == ".pdf" {
			continue
		}
		sourceContent, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		sf, sBody, err := frontmatter.ParseSource(sourceContent)
		if err != nil {
			continue
		}
		sf.Status = "compiled"
		updatedContent, err := frontmatter.Marshal(sf, sBody)
		if err == nil {
			_ = os.WriteFile(p, updatedContent, 0644)
		}
	}

	c.logger.Info("Multi-document compiled successfully", "slug", slug, "title", res.Title)
	return nil
}

func (c *Compiler) writeWikiArticle(sourcePath string, sf *frontmatter.SourceFrontmatter, sourceBody string, res *compileResponse, slug string) error {
	wikiPath := vault.WikiFilePath(c.cfg, slug)
	tmpPath := wikiPath + ".tmp"

	wf := frontmatter.WikiFrontmatter{
		Title:        res.Title,
		Type:         res.Type,
		Tags:         res.Tags,
		Created:      time.Now().Format("2006-01-02"),
		Updated:      time.Now().Format("2006-01-02"),
		Sources:      []string{fmt.Sprintf("[[%s]]", filepath.Base(sourcePath))},
		Provenance:   res.Provenance,
		Summary:      res.Summary,
		CompiledFrom: filepath.Base(sourcePath),
	}

	normalizedBody := c.normalizeLinks(res.Body)
	wikiContent, err := frontmatter.Marshal(wf, normalizedBody)
	if err != nil {
		return fmt.Errorf("failed to marshal wiki frontmatter: %w", err)
	}

	// Atomic write
	if err := os.WriteFile(tmpPath, wikiContent, 0644); err != nil {
		return fmt.Errorf("failed to write tmp wiki file: %w", err)
	}

	if err := os.Rename(tmpPath, wikiPath); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("failed to rename tmp wiki file: %w", err)
	}

	// Append to INDEX.md
	indexPath := vault.IndexPath(c.cfg)
	if err := index.Append(indexPath, index.Entry{Slug: slug, Summary: res.Summary}); err != nil {
		return fmt.Errorf("failed to update index: %w", err)
	}

	// Update source file status if not a PDF
	isPDF := strings.ToLower(filepath.Ext(sourcePath)) == ".pdf"
	if !isPDF {
		sf.Status = "compiled"
		updatedSourceContent, err := frontmatter.Marshal(sf, sourceBody)
		if err != nil {
			return fmt.Errorf("failed to marshal updated source file: %w", err)
		}

		if err := os.WriteFile(sourcePath, updatedSourceContent, 0644); err != nil {
			return fmt.Errorf("failed to update source file status: %w", err)
		}
	}

	c.logger.Info("Source compiled successfully", "slug", slug, "title", res.Title)
	return nil
}

var fullWikilinkRegex = regexp.MustCompile(`\[\[([^\]|#]+)(#[^\]|]*)?(\|[^\]]*)?\]\]`)

func (c *Compiler) getExistingWikiSlugs() map[string]bool {
	slugs := make(map[string]bool)
	wikiDir := vault.WikiDir(c.cfg)
	entries, err := os.ReadDir(wikiDir)
	if err == nil {
		for _, entry := range entries {
			if entry.IsDir() || strings.EqualFold(entry.Name(), "INDEX.md") || filepath.Ext(entry.Name()) != ".md" {
				continue
			}
			slug := strings.TrimSuffix(strings.ToLower(entry.Name()), ".md")
			slugs[slug] = true
		}
	}
	return slugs
}

func (c *Compiler) normalizeLinks(body string) string {
	existingSlugs := c.getExistingWikiSlugs()

	return fullWikilinkRegex.ReplaceAllStringFunc(body, func(match string) string {
		submatches := fullWikilinkRegex.FindStringSubmatch(match)
		if len(submatches) < 2 {
			return match
		}

		target := strings.TrimSpace(submatches[1])
		header := submatches[2]
		alias := submatches[3]

		// Resolve the target slug
		resolvedSlug, resolved := vault.ResolveWikiLink(target, existingSlugs)
		if !resolved {
			// Leave unchanged if not resolvable
			return match
		}

		// Construct the new link
		// If there is an existing alias, preserve it
		if alias != "" {
			return fmt.Sprintf("[[%s%s%s]]", resolvedSlug, header, alias)
		}

		// If no alias, check if target has formatting worth preserving (capitals, spaces, etc.)
		if target == resolvedSlug {
			return fmt.Sprintf("[[%s%s]]", resolvedSlug, header)
		}

		// Otherwise, use the original target as the alias to keep the layout beautiful!
		return fmt.Sprintf("[[%s%s|%s]]", resolvedSlug, header, target)
	})
}

func (c *Compiler) resolveCollision(slug string, sourcePath string) string {
	wikiPath := vault.WikiFilePath(c.cfg, slug)
	if _, err := os.Stat(wikiPath); os.IsNotExist(err) {
		return slug
	}

	// Read and parse the existing wiki file's frontmatter to check if it was compiled from the same source file
	if existingContent, err := os.ReadFile(wikiPath); err == nil {
		if wf, _, err := frontmatter.ParseWiki(existingContent); err == nil {
			if (sourcePath != "" && wf.CompiledFrom == filepath.Base(sourcePath)) || (sourcePath == "" && wf.CompiledFrom == "multi-doc") {
				// It was compiled from the same source/multi-doc! We can overwrite it without collision.
				return slug
			}
		}
	}

	// Slug collision!
	i := 2
	for {
		candidate := fmt.Sprintf("%s-%d", slug, i)
		candidatePath := vault.WikiFilePath(c.cfg, candidate)
		if _, err := os.Stat(candidatePath); os.IsNotExist(err) {
			// Check if this candidate was compiled from the same source file
			if existingContent, err := os.ReadFile(candidatePath); err == nil {
				if wf, _, err := frontmatter.ParseWiki(existingContent); err == nil {
					if (sourcePath != "" && wf.CompiledFrom == filepath.Base(sourcePath)) || (sourcePath == "" && wf.CompiledFrom == "multi-doc") {
						return candidate
					}
				}
			}
			c.logger.Warn("Slug collision resolved by appending suffix", "original", slug, "resolved", candidate)
			return candidate
		}
		i++
	}
}

func parseJSONResponse(resp string, logger *slog.Logger) (*compileResponse, error) {
	var candidates []string

	// Candidate 1: Content inside the ```json ... ``` block (using LastIndex to capture nested code blocks)
	startBlock := strings.Index(resp, "```json")
	if startBlock != -1 {
		endBlock := strings.LastIndex(resp, "```")
		if endBlock != -1 && endBlock > startBlock+7 {
			candidates = append(candidates, resp[startBlock+7:endBlock])
		}
	}

	// Candidate 2: Everything between the first '{' and the last '}'
	firstBrace := strings.Index(resp, "{")
	lastBrace := strings.LastIndex(resp, "}")
	if firstBrace != -1 && lastBrace != -1 && lastBrace > firstBrace {
		candidates = append(candidates, resp[firstBrace:lastBrace+1])
	}

	// Candidate 3: The raw response directly
	candidates = append(candidates, resp)

	// Try standard JSON unmarshal on each candidate after applying fixes
	var lastErr error
	for _, cand := range candidates {
		cand = strings.TrimSpace(cand)
		if cand == "" {
			continue
		}

		processed := fixJSONBackslashes(cand)
		processed = escapeRawNewlinesInJSON(processed)

		var cr compileResponse
		if err := json.Unmarshal([]byte(processed), &cr); err == nil {
			if cr.Title != "" && cr.Summary != "" && cr.Body != "" {
				return &cr, nil
			}
		} else {
			lastErr = err
		}
	}

	// If all standard unmarshaling attempts fail, fall back to robust parsing on the best candidate
	var bestCandidate string
	if len(candidates) > 0 {
		bestCandidate = candidates[0]
	} else {
		bestCandidate = resp
	}
	bestCandidate = strings.TrimSpace(bestCandidate)

	if logger != nil {
		logger.Warn("Failed to unmarshal JSON using standard parser, trying robust fallback", "error", lastErr)
	}

	robustRes := parseJSONRobustly(bestCandidate)
	if robustRes.Title != "" && robustRes.Summary != "" && robustRes.Body != "" {
		if logger != nil {
			logger.Info("Robust fallback successfully parsed the response", "title", robustRes.Title)
		}
		return robustRes, nil
	}

	return nil, fmt.Errorf("failed to parse JSON response: %w", lastErr)
}

func parseJSONRobustly(jsonStr string) *compileResponse {
	var res compileResponse

	// Helper to extract a string/value field robustly
	extractField := func(key string) string {
		re := regexp.MustCompile(`(?i)["']?` + regexp.QuoteMeta(key) + `["']?\s*:\s*(["']?)`)
		loc := re.FindStringSubmatchIndex(jsonStr)
		if len(loc) < 4 {
			return ""
		}
		quoteChar := ""
		if loc[2] != -1 && loc[3] != -1 {
			quoteChar = jsonStr[loc[2]:loc[3]]
		}
		start := loc[1]

		var val strings.Builder
		escaped := false
		runes := []rune(jsonStr[start:])
		for i := 0; i < len(runes); i++ {
			r := runes[i]
			if quoteChar != "" {
				if escaped {
					val.WriteRune(r)
					escaped = false
				} else if r == '\\' {
					val.WriteRune(r)
					escaped = true
				} else if string(r) == quoteChar {
					break
				} else {
					val.WriteRune(r)
				}
			} else {
				if r == ',' || r == '}' || r == '\n' || r == '\r' {
					break
				}
				val.WriteRune(r)
			}
		}

		rawVal := val.String()
		if quoteChar != "" {
			// Unescape JSON string content using json.Unmarshal
			var unescaped string
			wrapped := `"` + rawVal + `"`
			if err := json.Unmarshal([]byte(wrapped), &unescaped); err == nil {
				return unescaped
			}
			// Fallback unescape in case unmarshaling wrapped string fails
			return customUnescape(rawVal)
		}
		return strings.TrimSpace(rawVal)
	}

	res.Title = extractField("title")
	res.Type = extractField("type")
	res.Provenance = extractField("provenance")
	res.Summary = extractField("summary")
	res.Body = extractField("body")

	// Extract tags robustly from ["']?tags["']?\s*:\s*\[([^\]]*)\]
	tagsRe := regexp.MustCompile(`(?i)["']?tags["']?\s*:\s*\[([^\]]*)\]`)
	tagsMatch := tagsRe.FindStringSubmatch(jsonStr)
	if len(tagsMatch) > 1 {
		parts := strings.Split(tagsMatch[1], ",")
		for _, p := range parts {
			p = strings.TrimSpace(p)
			p = strings.Trim(p, `"'`)
			if p != "" {
				res.Tags = append(res.Tags, p)
			}
		}
	}

	return &res
}

func customUnescape(raw string) string {
	var result strings.Builder
	runes := []rune(raw)
	n := len(runes)
	for i := 0; i < n; i++ {
		if runes[i] == '\\' && i+1 < n {
			next := runes[i+1]
			switch next {
			case 'n':
				result.WriteRune('\n')
			case 't':
				result.WriteRune('\t')
			case 'r':
				result.WriteRune('\r')
			case '\\':
				result.WriteRune('\\')
			case '"':
				result.WriteRune('"')
			case '/':
				result.WriteRune('/')
			case 'b':
				result.WriteRune('\b')
			case 'f':
				result.WriteRune('\f')
			default:
				// If it's an unrecognized escape, keep both
				result.WriteRune('\\')
				result.WriteRune(next)
			}
			i++
		} else {
			result.WriteRune(runes[i])
		}
	}
	return result.String()
}

func parseSplitPlanResponse(resp string) (*splitPlan, error) {
	var jsonStr string
	startBlock := strings.Index(resp, "```json")
	if startBlock != -1 {
		endBlock := strings.LastIndex(resp, "```")
		if endBlock != -1 && endBlock > startBlock+7 {
			jsonStr = resp[startBlock+7 : endBlock]
		}
	}

	if jsonStr == "" {
		firstBrace := strings.Index(resp, "{")
		lastBrace := strings.LastIndex(resp, "}")
		if firstBrace != -1 && lastBrace != -1 && lastBrace > firstBrace {
			jsonStr = resp[firstBrace : lastBrace+1]
		}
	}

	if jsonStr == "" {
		jsonStr = resp
	}

	jsonStr = strings.TrimSpace(jsonStr)
	jsonStr = fixJSONBackslashes(jsonStr)
	jsonStr = escapeRawNewlinesInJSON(jsonStr)

	var sp splitPlan
	if err := json.Unmarshal([]byte(jsonStr), &sp); err != nil {
		sp = parseSplitPlanRobustly(jsonStr)
		if len(sp.Articles) > 0 {
			return &sp, nil
		}
		return nil, err
	}

	return &sp, nil
}

func parseSplitPlanRobustly(jsonStr string) splitPlan {
	var sp splitPlan
	sp.SplitRequired = strings.Contains(strings.ToLower(jsonStr), `"split_required"\s*:\s*true`) || strings.Contains(strings.ToLower(jsonStr), `"split_required"\s*:\s*1`)

	articlesIdx := strings.Index(jsonStr, `"articles"`)
	if articlesIdx == -1 {
		return sp
	}

	bracketStart := strings.Index(jsonStr[articlesIdx:], "[")
	if bracketStart == -1 {
		return sp
	}
	bracketStart += articlesIdx

	bracketEnd := strings.LastIndex(jsonStr, "]")
	if bracketEnd == -1 || bracketEnd < bracketStart {
		return sp
	}

	articlesArrStr := jsonStr[bracketStart+1 : bracketEnd]

	var objects []string
	var current strings.Builder
	depth := 0
	for _, r := range articlesArrStr {
		if r == '{' {
			depth++
		}
		if depth > 0 {
			current.WriteRune(r)
		}
		if r == '}' {
			depth--
			if depth == 0 {
				objects = append(objects, current.String())
				current.Reset()
			}
		}
	}

	for _, obj := range objects {
		extractField := func(key string) string {
			re := regexp.MustCompile(`(?i)["']?` + regexp.QuoteMeta(key) + `["']?\s*:\s*(["']?)`)
			loc := re.FindStringSubmatchIndex(obj)
			if len(loc) < 4 {
				return ""
			}
			quoteChar := ""
			if loc[2] != -1 && loc[3] != -1 {
				quoteChar = obj[loc[2]:loc[3]]
			}
			start := loc[1]

			var val strings.Builder
			escaped := false
			runes := []rune(obj[start:])
			for i := 0; i < len(runes); i++ {
				r := runes[i]
				if quoteChar != "" {
					if escaped {
						val.WriteRune(r)
						escaped = false
					} else if r == '\\' {
						val.WriteRune(r)
						escaped = true
					} else if string(r) == quoteChar {
						break
					} else {
						val.WriteRune(r)
					}
				} else {
					if r == ',' || r == '}' || r == '\n' || r == '\r' {
						break
					}
					val.WriteRune(r)
				}
			}

			rawVal := val.String()
			if quoteChar != "" {
				var unescaped string
				wrapped := `"` + rawVal + `"`
				if err := json.Unmarshal([]byte(wrapped), &unescaped); err == nil {
					return unescaped
				}
				return customUnescape(rawVal)
			}
			return strings.TrimSpace(rawVal)
		}

		var sa splitArticle
		sa.Slug = extractField("slug")
		sa.Title = extractField("title")
		sa.Type = extractField("type")
		sa.Summary = extractField("summary")
		sa.Instructions = extractField("instructions")

		depRe := regexp.MustCompile(`(?i)["']?dependent_slugs["']?\s*:\s*\[([^\]]*)\]`)
		depMatch := depRe.FindStringSubmatch(obj)
		if len(depMatch) > 1 {
			parts := strings.Split(depMatch[1], ",")
			for _, p := range parts {
				p = strings.TrimSpace(p)
				p = strings.Trim(p, `"'`)
				if p != "" {
					sa.DependentSlugs = append(sa.DependentSlugs, p)
				}
			}
		}

		if sa.Title != "" && (sa.Slug != "" || sa.Summary != "") {
			sp.Articles = append(sp.Articles, sa)
		}
	}

	if len(sp.Articles) > 0 {
		sp.SplitRequired = true
	}
	return sp
}

func escapeRawNewlinesInJSON(jsonStr string) string {
	var result strings.Builder
	runes := []rune(jsonStr)
	n := len(runes)
	inString := false
	escaped := false

	for i := 0; i < n; i++ {
		r := runes[i]
		if inString {
			if escaped {
				result.WriteRune(r)
				escaped = false
			} else if r == '\\' {
				result.WriteRune(r)
				escaped = true
			} else if r == '"' {
				result.WriteRune(r)
				inString = false
			} else if r == '\n' {
				result.WriteString(`\n`)
			} else if r == '\r' {
				if i+1 < n && runes[i+1] == '\n' {
					// skip
				} else {
					result.WriteString(`\n`)
				}
			} else {
				result.WriteRune(r)
			}
		} else {
			if r == '"' {
				inString = true
			}
			result.WriteRune(r)
		}
	}
	return result.String()
}

func fixJSONBackslashes(jsonStr string) string {
	var result strings.Builder
	runes := []rune(jsonStr)
	n := len(runes)
	for i := 0; i < n; i++ {
		if runes[i] == '\\' {
			if i+1 < n {
				next := runes[i+1]
				switch next {
				case '"', '\\', '/', 'b', 'f', 'n', 'r', 't':
					result.WriteRune('\\')
					result.WriteRune(next)
					i++ // Skip next char
				case 'u':
					if i+5 < n && isHex(runes[i+2]) && isHex(runes[i+3]) && isHex(runes[i+4]) && isHex(runes[i+5]) {
						result.WriteRune('\\')
						result.WriteRune('u')
						result.WriteRune(runes[i+2])
						result.WriteRune(runes[i+3])
						result.WriteRune(runes[i+4])
						result.WriteRune(runes[i+5])
						i += 5 // Skip u and hex digits
					} else {
						result.WriteString(`\\`)
					}
				default:
					result.WriteString(`\\`)
				}
			} else {
				result.WriteString(`\\`)
			}
		} else {
			result.WriteRune(runes[i])
		}
	}
	return result.String()
}

func isHex(r rune) bool {
	return (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')
}

type ingestAnalysisResponse struct {
	Action     string `json:"action"`
	TargetSlug string `json:"target_slug"`
	Reason     string `json:"reason"`
}

func (c *Compiler) analyzeIngestion(sourceContent []byte, existingArticles string) (string, string, error) {
	tmpl, err := template.New("ingest_analysis").Parse(prompts.IngestAnalysis)
	if err != nil {
		return "", "", err
	}

	var promptBuf bytes.Buffer
	err = tmpl.Execute(&promptBuf, map[string]any{
		"SourceContent":    string(sourceContent),
		"ExistingArticles": existingArticles,
	})
	if err != nil {
		return "", "", err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	resp, err := c.provider.Generate(ctx, c.cfg.CompileModelSingle, promptBuf.String())
	if err != nil {
		return "", "", err
	}

	// Parse JSON decision
	var jsonStr string
	firstBrace := strings.Index(resp, "{")
	lastBrace := strings.LastIndex(resp, "}")
	if firstBrace != -1 && lastBrace != -1 && lastBrace > firstBrace {
		jsonStr = resp[firstBrace : lastBrace+1]
	} else {
		jsonStr = resp
	}

	var iar ingestAnalysisResponse
	if err := json.Unmarshal([]byte(jsonStr), &iar); err != nil {
		return "", "", err
	}

	return iar.Action, iar.TargetSlug, nil
}

func (c *Compiler) compileExpand(sourcePath string, sf *frontmatter.SourceFrontmatter, body string, targetSlug string, existingArticles string) error {
	wikiPath := vault.WikiFilePath(c.cfg, targetSlug)

	// Read existing wiki article content
	existingWikiContent, err := os.ReadFile(wikiPath)
	if err != nil {
		return fmt.Errorf("failed to read existing wiki article for expansion: %w", err)
	}

	_, existingBody, err := frontmatter.ParseWiki(existingWikiContent)
	if err != nil {
		existingBody = string(existingWikiContent) // Fallback if parsing fails
	}

	// Step 1: Pre-Merge Atomic Safety Backup!
	c.logger.Info("Creating pre-merge atomic safety backup for existing article...", "slug", targetSlug)
	trashPath, err := cleaner.TrashFile(c.cfg, wikiPath)
	if err != nil {
		c.logger.Warn("Failed to create pre-merge backup", "slug", targetSlug, "error", err)
	} else {
		c.logger.Info("Backup created successfully in trash", "path", trashPath)
	}

	// Step 2: Render compile expand template
	tmpl, err := template.New("compile_expand").Parse(prompts.CompileExpand)
	if err != nil {
		return fmt.Errorf("failed to parse compile_expand template: %w", err)
	}

	var promptBuf bytes.Buffer
	err = tmpl.Execute(&promptBuf, map[string]any{
		"ExistingContent":  existingBody,
		"SourceContent":    body,
		"ExistingArticles": existingArticles,
	})
	if err != nil {
		return fmt.Errorf("failed to execute compile_expand template: %w", err)
	}

	var llmResp string
	var res *compileResponse

	// Call LLM
	for attempt := 1; attempt <= 2; attempt++ {
		c.logger.Debug("Sending compile expand request to model", "model", c.cfg.CompileModelSingle, "attempt", attempt)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		llmResp, err = c.provider.Generate(ctx, c.cfg.CompileModelSingle, promptBuf.String())
		cancel()

		if err != nil {
			c.logger.Warn("Model expansion generation failed on attempt", "attempt", attempt, "error", err)
			continue
		}

		res, err = parseJSONResponse(llmResp, c.logger)
		if err == nil && res.Title != "" && res.Summary != "" && res.Body != "" {
			break
		}
		c.logger.Warn("Received malformed JSON from model, retrying", "attempt", attempt, "error", err)
	}

	if res == nil {
		return fmt.Errorf("failed to compile expanded article after 2 attempts due to model timeouts or malformed responses")
	}

	// Step 3: Write expanded wiki article, preserving and merging metadata
	createdDate := time.Now().Format("2006-01-02")
	existingWF, _, err := frontmatter.ParseWiki(existingWikiContent)
	var mergedSources []string
	if err == nil {
		if existingWF.Created != "" {
			createdDate = existingWF.Created
		}
		mergedSources = existingWF.Sources
	}

	newSourceLink := fmt.Sprintf("[[%s]]", filepath.Base(sourcePath))
	sourceExists := false
	for _, s := range mergedSources {
		if s == newSourceLink {
			sourceExists = true
			break
		}
	}
	if !sourceExists {
		mergedSources = append(mergedSources, newSourceLink)
	}

	wf := frontmatter.WikiFrontmatter{
		Title:        res.Title,
		Type:         res.Type,
		Tags:         res.Tags,
		Created:      createdDate,
		Updated:      time.Now().Format("2006-01-02"),
		Sources:      mergedSources,
		Related:      existingWF.Related,
		Provenance:   res.Provenance,
		Summary:      res.Summary,
		CompiledFrom: filepath.Base(sourcePath),
	}

	wikiContent, err := frontmatter.Marshal(wf, res.Body)
	if err != nil {
		return fmt.Errorf("failed to marshal expanded wiki frontmatter: %w", err)
	}

	// Atomic write back to original path
	tmpPath := wikiPath + ".tmp"
	if err := os.WriteFile(tmpPath, wikiContent, 0644); err != nil {
		return fmt.Errorf("failed to write tmp wiki file: %w", err)
	}

	if err := os.Rename(tmpPath, wikiPath); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("failed to rename tmp wiki file: %w", err)
	}

	// Append/Update INDEX.md
	indexPath := vault.IndexPath(c.cfg)
	if err := index.Append(indexPath, index.Entry{Slug: targetSlug, Summary: res.Summary}); err != nil {
		return fmt.Errorf("failed to update index: %w", err)
	}

	// Update source file status if not a PDF
	isPDF := strings.ToLower(filepath.Ext(sourcePath)) == ".pdf"
	if !isPDF {
		sf.Status = "compiled"
		updatedSourceContent, err := frontmatter.Marshal(sf, body)
		if err == nil {
			_ = os.WriteFile(sourcePath, updatedSourceContent, 0644)
		}
	}

	c.logger.Info("Article expanded successfully", "slug", targetSlug, "title", res.Title)
	return nil
}

type synthesizeSplitPlan struct {
	HubSynthesisInstructions string         `json:"hub_synthesis_instructions"`
	Articles                 []splitArticle `json:"articles"`
}

func parseSynthesizeSplitPlanResponse(resp string) (*synthesizeSplitPlan, error) {
	var jsonStr string
	startBlock := strings.Index(resp, "```json")
	if startBlock != -1 {
		endBlock := strings.LastIndex(resp, "```")
		if endBlock != -1 && endBlock > startBlock+7 {
			jsonStr = resp[startBlock+7 : endBlock]
		}
	}

	if jsonStr == "" {
		firstBrace := strings.Index(resp, "{")
		lastBrace := strings.LastIndex(resp, "}")
		if firstBrace != -1 && lastBrace != -1 && lastBrace > firstBrace {
			jsonStr = resp[firstBrace : lastBrace+1]
		}
	}

	if jsonStr == "" {
		jsonStr = resp
	}

	jsonStr = strings.TrimSpace(jsonStr)
	jsonStr = fixJSONBackslashes(jsonStr)
	jsonStr = escapeRawNewlinesInJSON(jsonStr)

	var ssp synthesizeSplitPlan
	if err := json.Unmarshal([]byte(jsonStr), &ssp); err != nil {
		// Try robust parsing fallback
		ssp.HubSynthesisInstructions = extractFieldRobustly(jsonStr, "hub_synthesis_instructions")
		ssp.Articles = parseSplitPlanRobustly(jsonStr).Articles
		if ssp.HubSynthesisInstructions != "" || len(ssp.Articles) > 0 {
			return &ssp, nil
		}
		return nil, err
	}

	return &ssp, nil
}

func extractFieldRobustly(jsonStr string, key string) string {
	re := regexp.MustCompile(`(?i)["']?` + regexp.QuoteMeta(key) + `["']?\s*:\s*(["']?)`)
	loc := re.FindStringSubmatchIndex(jsonStr)
	if len(loc) < 4 {
		return ""
	}
	quoteChar := ""
	if loc[2] != -1 && loc[3] != -1 {
		quoteChar = jsonStr[loc[2]:loc[3]]
	}
	start := loc[1]

	var val strings.Builder
	escaped := false
	runes := []rune(jsonStr[start:])
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		if quoteChar != "" {
			if escaped {
				val.WriteRune(r)
				escaped = false
			} else if r == '\\' {
				val.WriteRune(r)
				escaped = true
			} else if string(r) == quoteChar {
				break
			} else {
				val.WriteRune(r)
			}
		} else {
			if r == ',' || r == '}' || r == '\n' || r == '\r' {
				break
			}
			val.WriteRune(r)
		}
	}

	rawVal := val.String()
	if quoteChar != "" {
		var unescaped string
		wrapped := `"` + rawVal + `"`
		if err := json.Unmarshal([]byte(wrapped), &unescaped); err == nil {
			return unescaped
		}
		return customUnescape(rawVal)
	}
	return strings.TrimSpace(rawVal)
}

func (c *Compiler) compileSynthesizeAndSplit(sourcePath string, sf *frontmatter.SourceFrontmatter, body string, targetSlug string, existingArticlesStr string) error {
	c.logger.Info("Starting LLM-guided Synthesize-and-Split compilation", "path", sourcePath, "target", targetSlug)

	wikiPath := vault.WikiFilePath(c.cfg, targetSlug)

	// Read existing wiki article content
	existingWikiContent, err := os.ReadFile(wikiPath)
	if err != nil {
		return fmt.Errorf("failed to read existing wiki article for synthesize-and-split: %w", err)
	}

	_, existingBody, err := frontmatter.ParseWiki(existingWikiContent)
	if err != nil {
		existingBody = string(existingWikiContent) // Fallback if parsing fails
	}

	// Read new source content
	var content []byte
	isPDF := strings.ToLower(filepath.Ext(sourcePath)) == ".pdf"
	if isPDF {
		content = []byte(body)
	} else {
		content, err = os.ReadFile(sourcePath)
		if err != nil {
			return err
		}
	}

	// 1. Generate synthesis and split plan
	tmpl, err := template.New("synthesize_split_plan").Parse(prompts.SynthesizeSplitPlan)
	if err != nil {
		return fmt.Errorf("failed to parse synthesize split plan template: %w", err)
	}

	var promptBuf bytes.Buffer
	err = tmpl.Execute(&promptBuf, map[string]any{
		"ExistingContent": existingBody,
		"SourceContent":   string(content),
	})
	if err != nil {
		return fmt.Errorf("failed to execute synthesize split plan template: %w", err)
	}

	var plan *synthesizeSplitPlan
	for attempt := 1; attempt <= 2; attempt++ {
		c.logger.Debug("Sending synthesize split plan request to model", "attempt", attempt)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		llmResp, err := c.provider.Generate(ctx, c.cfg.CompileModelSingle, promptBuf.String())
		cancel()

		if err != nil {
			c.logger.Warn("Synthesize split planning generation failed on attempt", "attempt", attempt, "error", err)
			continue
		}

		plan, err = parseSynthesizeSplitPlanResponse(llmResp)
		if err == nil {
			break
		}
		c.logger.Warn("Received malformed JSON for synthesize split plan, retrying", "attempt", attempt, "error", err)
	}

	if plan == nil {
		return fmt.Errorf("failed to generate synthesize split plan after 2 attempts")
	}

	c.logger.Info("Synthesize split plan generated successfully", "spokesCount", len(plan.Articles))

	// 2. Compile Specialized Spokes
	var compiledSpokes []splitArticle
	for _, spoke := range plan.Articles {
		spokeSlug := vault.MakeSlug(spoke.Slug)
		if spokeSlug == "" {
			spokeSlug = vault.MakeSlug(spoke.Title)
		}
		spokeSlug = c.resolveCollision(spokeSlug, sourcePath)

		c.logger.Info("Compiling specialized Spoke article", "title", spoke.Title, "slug", spokeSlug)

		// Render compile spoke prompt template
		spokeTmpl, err := template.New("compile_spoke").Parse(prompts.CompileSpoke)
		if err != nil {
			return fmt.Errorf("failed to parse compile spoke template: %w", err)
		}

		var spokeBuf bytes.Buffer
		err = spokeTmpl.Execute(&spokeBuf, map[string]any{
			"SourceContent":     string(content),
			"SpokeTitle":        spoke.Title,
			"SpokeSummary":      spoke.Summary,
			"SpokeInstructions": spoke.Instructions,
			"DependentLinks":    append(spoke.DependentSlugs, targetSlug), // Link back to master Hub
			"ExistingArticles":  existingArticlesStr,
		})
		if err != nil {
			return fmt.Errorf("failed to execute compile spoke template: %w", err)
		}

		var spokeRes *compileResponse
		for attempt := 1; attempt <= 2; attempt++ {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
			llmResp, err := c.provider.Generate(ctx, c.cfg.CompileModelSingle, spokeBuf.String())
			cancel()

			if err != nil {
				c.logger.Warn("Spoke compilation failed on attempt", "title", spoke.Title, "attempt", attempt, "error", err)
				continue
			}

			spokeRes, err = parseJSONResponse(llmResp, c.logger)
			if err == nil && spokeRes.Title != "" && spokeRes.Summary != "" && spokeRes.Body != "" {
				break
			}
			c.logger.Warn("Received malformed JSON for Spoke, retrying", "title", spoke.Title, "attempt", attempt, "error", err)
		}

		if spokeRes == nil {
			return fmt.Errorf("failed to compile Spoke article '%s' after 2 attempts", spoke.Title)
		}

		// Write Spoke article
		spokeWikiPath := vault.WikiFilePath(c.cfg, spokeSlug)
		spokeTmpPath := spokeWikiPath + ".tmp"

		spokeWf := frontmatter.WikiFrontmatter{
			Title:        spokeRes.Title,
			Type:         spokeRes.Type,
			Tags:         spokeRes.Tags,
			Created:      time.Now().Format("2006-01-02"),
			Updated:      time.Now().Format("2006-01-02"),
			Sources:      []string{fmt.Sprintf("[[%s]]", filepath.Base(sourcePath))},
			Provenance:   spokeRes.Provenance,
			Summary:      spokeRes.Summary,
			CompiledFrom: filepath.Base(sourcePath),
			Related:      append(spoke.DependentSlugs, targetSlug),
		}

		normalizedBody := c.normalizeLinks(spokeRes.Body)
		spokeWikiContent, err := frontmatter.Marshal(spokeWf, normalizedBody)
		if err != nil {
			return fmt.Errorf("failed to marshal Spoke wiki frontmatter: %w", err)
		}

		if err := os.WriteFile(spokeTmpPath, spokeWikiContent, 0644); err != nil {
			return fmt.Errorf("failed to write tmp Spoke wiki file: %w", err)
		}

		if err := os.Rename(spokeTmpPath, spokeWikiPath); err != nil {
			os.Remove(spokeTmpPath)
			return fmt.Errorf("failed to rename tmp Spoke wiki file: %w", err)
		}

		// Append Spoke to INDEX.md
		indexPath := vault.IndexPath(c.cfg)
		if err := index.Append(indexPath, index.Entry{Slug: spokeSlug, Summary: spokeRes.Summary}); err != nil {
			return fmt.Errorf("failed to update index for Spoke: %w", err)
		}

		// Keep track of compiled spoke info with its final slug
		compiledSpokes = append(compiledSpokes, splitArticle{
			Slug:    spokeSlug,
			Title:   spokeRes.Title,
			Summary: spokeRes.Summary,
		})
	}

	// 3. Pre-Merge Safety Backup for Hub
	c.logger.Info("Creating pre-merge atomic safety backup for existing master Hub article...", "slug", targetSlug)
	trashPath, err := cleaner.TrashFile(c.cfg, wikiPath)
	if err != nil {
		c.logger.Warn("Failed to create pre-merge backup for Hub", "slug", targetSlug, "error", err)
	} else {
		c.logger.Info("Hub backup created successfully in trash", "path", trashPath)
	}

	// 4. Compile and Synthesize Master Hub
	c.logger.Info("Compiling and synthesizing master Hub article...", "slug", targetSlug)

	hubTmpl, err := template.New("compile_hub_synthesis").Parse(prompts.CompileHubSynthesis)
	if err != nil {
		return fmt.Errorf("failed to parse compile hub synthesis template: %w", err)
	}

	var hubBuf bytes.Buffer
	err = hubTmpl.Execute(&hubBuf, map[string]any{
		"ExistingContent":       existingBody,
		"SourceContent":         body,
		"SynthesisInstructions": plan.HubSynthesisInstructions,
		"Spokes":                compiledSpokes,
		"ExistingArticles":      existingArticlesStr,
	})
	if err != nil {
		return fmt.Errorf("failed to execute compile hub synthesis template: %w", err)
	}

	var hubRes *compileResponse
	for attempt := 1; attempt <= 2; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		llmResp, err := c.provider.Generate(ctx, c.cfg.CompileModelSingle, hubBuf.String())
		cancel()

		if err != nil {
			c.logger.Warn("Hub synthesis compilation failed on attempt", "attempt", attempt, "error", err)
			continue
		}

		hubRes, err = parseJSONResponse(llmResp, c.logger)
		if err == nil && hubRes.Title != "" && hubRes.Summary != "" && hubRes.Body != "" {
			break
		}
		c.logger.Warn("Received malformed JSON for Hub synthesis, retrying", "attempt", attempt, "error", err)
	}

	if hubRes == nil {
		return fmt.Errorf("failed to compile Hub synthesis after 2 attempts")
	}

	// Write Synthesized Hub wiki article, merging sources
	createdDate := time.Now().Format("2006-01-02")
	existingWF, _, err := frontmatter.ParseWiki(existingWikiContent)
	var mergedSources []string
	var relatedSlugs []string
	if err == nil {
		if existingWF.Created != "" {
			createdDate = existingWF.Created
		}
		mergedSources = existingWF.Sources
		relatedSlugs = existingWF.Related
	}

	newSourceLink := fmt.Sprintf("[[%s]]", filepath.Base(sourcePath))
	sourceExists := false
	for _, s := range mergedSources {
		if s == newSourceLink {
			sourceExists = true
			break
		}
	}
	if !sourceExists {
		mergedSources = append(mergedSources, newSourceLink)
	}

	// Add new compiled spokes to Hub's related section
	for _, spoke := range compiledSpokes {
		spokeExists := false
		for _, r := range relatedSlugs {
			if r == spoke.Slug {
				spokeExists = true
				break
			}
		}
		if !spokeExists {
			relatedSlugs = append(relatedSlugs, spoke.Slug)
		}
	}

	hubWf := frontmatter.WikiFrontmatter{
		Title:        hubRes.Title,
		Type:         hubRes.Type,
		Tags:         hubRes.Tags,
		Created:      createdDate,
		Updated:      time.Now().Format("2006-01-02"),
		Sources:      mergedSources,
		Related:      relatedSlugs,
		Provenance:   hubRes.Provenance,
		Summary:      hubRes.Summary,
		CompiledFrom: filepath.Base(sourcePath),
	}

	normalizedHubBody := c.normalizeLinks(hubRes.Body)
	hubWikiContent, err := frontmatter.Marshal(hubWf, normalizedHubBody)
	if err != nil {
		return fmt.Errorf("failed to marshal synthesized Hub wiki frontmatter: %w", err)
	}

	// Atomic write
	tmpPath := wikiPath + ".tmp"
	if err := os.WriteFile(tmpPath, hubWikiContent, 0644); err != nil {
		return fmt.Errorf("failed to write tmp Hub wiki file: %w", err)
	}

	if err := os.Rename(tmpPath, wikiPath); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("failed to rename tmp Hub wiki file: %w", err)
	}

	// Append/Update Hub in INDEX.md
	indexPath := vault.IndexPath(c.cfg)
	if err := index.Append(indexPath, index.Entry{Slug: targetSlug, Summary: hubRes.Summary}); err != nil {
		return fmt.Errorf("failed to update index: %w", err)
	}

	// Mark source file as compiled
	if !isPDF {
		sf.Status = "compiled"
		updatedSourceContent, err := frontmatter.Marshal(sf, body)
		if err == nil {
			_ = os.WriteFile(sourcePath, updatedSourceContent, 0644)
		}
	}

	c.logger.Info("Synthesize-and-Split compilation completed successfully", "hubSlug", targetSlug, "spokesCount", len(compiledSpokes))
	return nil
}

func hasPotentialOverlap(rawTitle string, indexEntries []index.Entry) bool {
	words := strings.Fields(strings.ToLower(rawTitle))
	keywords := make(map[string]bool)

	// Stopwords list
	stopwords := map[string]bool{
		"what": true, "with": true, "from": true, "that": true, "this": true,
		"your": true, "have": true, "some": true, "here": true, "there": true,
		"about": true, "their": true, "them": true, "then": true, "than": true,
		"into": true, "over": true, "under": true, "other": true, "could": true,
		"would": true, "should": true, "these": true, "those": true, "where": true,
		"which": true, "while": true, "whoever": true, "whose": true, "quantum": true,
		"computing": true, "learning": true, "notes": true,
	}

	for _, w := range words {
		w = strings.Trim(w, `.,;:!?()[]{}""''*_-`)
		if len(w) > 3 && !stopwords[w] {
			keywords[w] = true
		}
	}

	if len(keywords) == 0 {
		return false
	}

	for _, entry := range indexEntries {
		slugWords := strings.Split(strings.ToLower(entry.Slug), "-")
		for _, sw := range slugWords {
			if keywords[sw] {
				return true
			}
		}
	}

	return false
}

func (c *Compiler) CompactSingle(slug string) error {
	wikiPath := vault.WikiFilePath(c.cfg, slug)

	// Read existing wiki article content
	existingWikiContent, err := os.ReadFile(wikiPath)
	if err != nil {
		return fmt.Errorf("failed to read wiki article for compaction: %w", err)
	}

	wf, body, err := frontmatter.ParseWiki(existingWikiContent)
	if err != nil {
		return fmt.Errorf("failed to parse frontmatter for compaction: %w", err)
	}

	c.logger.Info("Starting technical compaction of wiki article...", "slug", slug)

	// Step 1: Render compaction prompt template
	tmpl, err := template.New("compact").Parse(prompts.Compact)
	if err != nil {
		return fmt.Errorf("failed to parse compact template: %w", err)
	}

	var promptBuf bytes.Buffer
	err = tmpl.Execute(&promptBuf, map[string]any{
		"Content": body,
	})
	if err != nil {
		return fmt.Errorf("failed to execute compact template: %w", err)
	}

	var llmResp string
	var res *compileResponse

	// Call LLM
	for attempt := 1; attempt <= 2; attempt++ {
		c.logger.Debug("Sending compaction request to model", "model", c.cfg.CompileModelSingle, "attempt", attempt)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		llmResp, err = c.provider.Generate(ctx, c.cfg.CompileModelSingle, promptBuf.String())
		cancel()

		if err != nil {
			c.logger.Warn("Model compaction generation failed on attempt", "attempt", attempt, "error", err)
			continue
		}

		res, err = parseJSONResponse(llmResp, c.logger)
		if err == nil && res.Title != "" && res.Summary != "" && res.Body != "" {
			break
		}
		c.logger.Warn("Received malformed JSON from model, retrying", "attempt", attempt, "error", err)
	}

	if res == nil {
		return fmt.Errorf("failed to compact article after 2 attempts due to model timeouts or malformed responses")
	}

	// Step 2: Atomic Pre-Compaction Safety Backup!
	c.logger.Info("Creating pre-compaction atomic safety backup...", "slug", slug)
	trashPath, err := cleaner.TrashFile(c.cfg, wikiPath)
	if err != nil {
		c.logger.Warn("Failed to create backup before compaction", "slug", slug, "error", err)
	} else {
		c.logger.Info("Pre-compaction backup created successfully", "path", trashPath)
	}

	// Step 3: Overwrite wiki article with compacted content, keeping original created date and sources
	wf.Title = res.Title
	wf.Type = res.Type
	wf.Tags = res.Tags
	wf.Updated = time.Now().Format("2006-01-02")
	wf.Summary = res.Summary
	wf.Provenance = res.Provenance

	wikiContent, err := frontmatter.Marshal(wf, res.Body)
	if err != nil {
		return fmt.Errorf("failed to marshal compacted wiki frontmatter: %w", err)
	}

	tmpPath := wikiPath + ".tmp"
	if err := os.WriteFile(tmpPath, wikiContent, 0644); err != nil {
		return fmt.Errorf("failed to write tmp wiki file: %w", err)
	}

	if err := os.Rename(tmpPath, wikiPath); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("failed to rename tmp wiki file: %w", err)
	}

	// Append/Update INDEX.md
	indexPath := vault.IndexPath(c.cfg)
	if err := index.Append(indexPath, index.Entry{Slug: slug, Summary: res.Summary}); err != nil {
		return fmt.Errorf("failed to update index: %w", err)
	}

	c.logger.Info("Article compacted successfully", "slug", slug, "title", res.Title)
	return nil
}

// readPDFText extracts plain text from a PDF file.
func readPDFText(path string) (string, error) {
	f, r, err := pdf.Open(path)
	if err != nil {
		return "", fmt.Errorf("failed to open PDF: %w", err)
	}
	defer f.Close()

	var buf bytes.Buffer
	b, err := r.GetPlainText()
	if err != nil {
		// Fallback to page-by-page row-based extraction
		numPages := r.NumPage()
		for i := 1; i <= numPages; i++ {
			p := r.Page(i)
			if p.V.IsNull() {
				continue
			}
			rows, _ := p.GetTextByRow()
			for _, row := range rows {
				for _, word := range row.Content {
					buf.WriteString(word.S)
					buf.WriteByte(' ')
				}
				buf.WriteByte('\n')
			}
		}
		return buf.String(), nil
	}
	_, err = buf.ReadFrom(b)
	if err != nil {
		return "", fmt.Errorf("failed to read plain text from PDF: %w", err)
	}
	return buf.String(), nil
}
