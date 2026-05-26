package querier

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"strings"
	"text/template"

	"github.com/avnis/kb-system/internal/config"
	"github.com/avnis/kb-system/internal/frontmatter"
	"github.com/avnis/kb-system/internal/index"
	"github.com/avnis/kb-system/internal/provider"
	"github.com/avnis/kb-system/internal/vault"
	"github.com/avnis/kb-system/prompts"
)

type Querier struct {
	cfg    *config.Config
	prov   provider.Provider
	logger *slog.Logger
}

type WikiContext struct {
	Slug    string
	Content string
}

func New(cfg *config.Config, prov provider.Provider, logger *slog.Logger) *Querier {
	return &Querier{
		cfg:    cfg,
		prov:   prov,
		logger: logger,
	}
}

func (q *Querier) Query(ctx context.Context, queryStr string) (string, error) {
	indexPath := vault.IndexPath(q.cfg)
	indexEntries, err := index.Read(indexPath)
	if err != nil {
		return "", fmt.Errorf("failed to read INDEX.md: %w", err)
	}

	if len(indexEntries) == 0 {
		return "The wiki index is empty. No articles are available to query.", nil
	}

	// 1. Format the index entries into a bulleted list for the LLM
	var indexLines []string
	for _, e := range indexEntries {
		indexLines = append(indexLines, fmt.Sprintf("- [[%s]] — %s", e.Slug, e.Summary))
	}
	indexStr := strings.Join(indexLines, "\n")

	// 2. Render and run the QuerySelect template
	tmplSelect, err := template.New("select").Parse(prompts.QuerySelect)
	if err != nil {
		return "", fmt.Errorf("failed to parse query_select template: %w", err)
	}

	var selectBuf bytes.Buffer
	err = tmplSelect.Execute(&selectBuf, map[string]any{
		"Query": queryStr,
		"Index": indexStr,
	})
	if err != nil {
		return "", fmt.Errorf("failed to execute query_select template: %w", err)
	}

	q.logger.Debug("Sending query relevance selection request to model", "model", q.cfg.QueryModel)
	respSelect, err := q.prov.Generate(ctx, q.cfg.QueryModel, selectBuf.String())
	if err != nil {
		return "", fmt.Errorf("model relevance check failed: %w", err)
	}

	// 3. Extract and parse the JSON array of slugs from the LLM response
	respSelectClean := cleanThoughtTags(respSelect)
	var selectedSlugs []string
	firstBracket := strings.Index(respSelectClean, "[")
	lastBracket := strings.LastIndex(respSelectClean, "]")
	var jsonStr string
	if firstBracket != -1 && lastBracket != -1 && lastBracket > firstBracket {
		jsonStr = respSelectClean[firstBracket : lastBracket+1]
	} else {
		jsonStr = respSelectClean
	}

	if err := json.Unmarshal([]byte(jsonStr), &selectedSlugs); err != nil {
		q.logger.Warn("Failed to parse LLM selected slugs JSON, falling back to all indexed articles", "response", respSelect, "error", err)
		// Fallback: use all indexed slugs
		for _, e := range indexEntries {
			selectedSlugs = append(selectedSlugs, e.Slug)
		}
	}

	// 4. Gather the content for all selected slugs
	var articles []WikiContext
	for _, slug := range selectedSlugs {
		slug = strings.TrimSpace(slug)
		if slug == "" {
			continue
		}
		wikiPath := vault.WikiFilePath(q.cfg, slug)
		contentBytes, err := os.ReadFile(wikiPath)
		if err != nil {
			q.logger.Debug("Could not read selected wiki article, skipping", "slug", slug, "error", err)
			continue
		}

		wf, body, err := frontmatter.ParseWiki(contentBytes)
		var articleBody string
		if err != nil {
			q.logger.Debug("Could not parse wiki frontmatter, using raw file content", "slug", slug, "error", err)
			articleBody = string(contentBytes)
		} else {
			articleBody = fmt.Sprintf("# %s\n\nTags: %s\n\n%s", wf.Title, strings.Join(wf.Tags, ", "), body)
		}

		articles = append(articles, WikiContext{
			Slug:    slug,
			Content: articleBody,
		})
	}

	if len(articles) == 0 {
		return "No relevant articles could be found in the wiki to answer your query.", nil
	}

	// 5. Render and run the QueryAnswer template
	tmplAnswer, err := template.New("answer").Parse(prompts.QueryAnswer)
	if err != nil {
		return "", fmt.Errorf("failed to parse query_answer template: %w", err)
	}

	var answerBuf bytes.Buffer
	err = tmplAnswer.Execute(&answerBuf, map[string]any{
		"Query":    queryStr,
		"Articles": articles,
	})
	if err != nil {
		return "", fmt.Errorf("failed to execute query_answer template: %w", err)
	}

	q.logger.Debug("Sending query answer generation request to model", "model", q.cfg.QueryModel, "contextArticlesCount", len(articles))
	answerResp, err := q.prov.Generate(ctx, q.cfg.QueryModel, answerBuf.String())
	if err != nil {
		return "", fmt.Errorf("model answer generation failed: %w", err)
	}

	return formatAnswerForTerminal(answerResp), nil
}

var (
	thoughtClosedRegex = regexp.MustCompile(`(?is)<(thought|think)\b[^>]*>.*?</(thought|think)>`)
	thoughtOpenRegex   = regexp.MustCompile(`(?is)<(thought|think)\b[^>]*>.*`)
)

func cleanThoughtTags(s string) string {
	s = thoughtClosedRegex.ReplaceAllString(s, "")
	s = thoughtOpenRegex.ReplaceAllString(s, "")
	return strings.TrimSpace(s)
}

func formatAnswerForTerminal(s string) string {
	// 1. Clean the thought tags first
	s = cleanThoughtTags(s)

	// 2. Format LaTeX formulas
	s = formatMath(s)

	// 3. Format markdown syntax for terminal styling
	s = formatMarkdownForTerminal(s)

	// 4. Extract and clean up inline citations, appending them at the end
	var slugs []string
	s, slugs = formatCitations(s)

	if len(slugs) > 0 {
		var refBuilder strings.Builder
		refBuilder.WriteString("\n\n\033[1;33mSources:\033[0m")
		for i, slug := range slugs {
			refBuilder.WriteString(fmt.Sprintf("\n  \033[1;36m[%d]\033[0m %s", i+1, slug))
		}
		s += refBuilder.String()
	}

	return s
}

func formatMath(s string) string {
	// 1. Replace block equations $$...$$ with indented and cleaned text
	blockRe := regexp.MustCompile(`(?s)\$\$(.*?)\$\$`)
	s = blockRe.ReplaceAllStringFunc(s, func(match string) string {
		sub := blockRe.FindStringSubmatch(match)
		if len(sub) < 2 {
			return match
		}
		cleaned := cleanMathSymbols(sub[1])
		return fmt.Sprintf("\n    %s\n", cleaned)
	})

	// 2. Replace inline equations $...$ with cleaned text
	inlineRe := regexp.MustCompile(`\$(.*?)\$`)
	s = inlineRe.ReplaceAllStringFunc(s, func(match string) string {
		sub := inlineRe.FindStringSubmatch(match)
		if len(sub) < 2 {
			return match
		}
		return cleanMathSymbols(sub[1])
	})

	return s
}

func cleanMathSymbols(math string) string {
	math = strings.TrimSpace(math)

	// Replace Dirac notation commands
	math = strings.ReplaceAll(math, `\rangle`, `⟩`)
	math = strings.ReplaceAll(math, `\langle`, `⟨`)

	// Common mathematical functions/symbols
	math = regexp.MustCompile(`\\text\{([^\}]+)\}`).ReplaceAllString(math, "$1")
	math = strings.ReplaceAll(math, `\dagger`, `†`)
	math = strings.ReplaceAll(math, `\pi`, `π`)
	math = strings.ReplaceAll(math, `\theta`, `θ`)
	math = strings.ReplaceAll(math, `\phi`, `φ`)
	math = strings.ReplaceAll(math, `\psi`, `ψ`)
	math = strings.ReplaceAll(math, `\alpha`, `α`)
	math = strings.ReplaceAll(math, `\beta`, `β`)
	math = strings.ReplaceAll(math, `\gamma`, `γ`)
	math = strings.ReplaceAll(math, `\delta`, `δ`)
	math = strings.ReplaceAll(math, `\sigma`, `σ`)
	math = strings.ReplaceAll(math, `\omega`, `ω`)
	math = strings.ReplaceAll(math, `\hbar`, `ħ`)
	math = strings.ReplaceAll(math, `\lambda`, `λ`)
	math = strings.ReplaceAll(math, `\otimes`, `⊗`)
	math = strings.ReplaceAll(math, `\oplus`, `⊕`)
	math = strings.ReplaceAll(math, `\sqrt`, `√`)

	// Clean super/subscripts if simple
	math = regexp.MustCompile(`\^([a-zA-Z0-9])`).ReplaceAllString(math, "^$1")
	math = regexp.MustCompile(`\_([a-zA-Z0-9])`).ReplaceAllString(math, "_$1")
	math = regexp.MustCompile(`\^\{([^\}]+)\}`).ReplaceAllString(math, "^($1)")
	math = regexp.MustCompile(`\_\{([^\}]+)\}`).ReplaceAllString(math, "_($1)")

	// Remove remaining backslashes for standard symbols
	math = strings.ReplaceAll(math, `\cdot`, `·`)
	math = strings.ReplaceAll(math, `\times`, `×`)
	math = strings.ReplaceAll(math, `\pm`, `±`)
	math = strings.ReplaceAll(math, `\mp`, `∓`)
	math = strings.ReplaceAll(math, `\ge`, `≥`)
	math = strings.ReplaceAll(math, `\le`, `≤`)
	math = strings.ReplaceAll(math, `\neq`, `≠`)
	math = strings.ReplaceAll(math, `\approx`, `≈`)
	math = strings.ReplaceAll(math, `\infty`, `∞`)
	math = strings.ReplaceAll(math, `\partial`, `∂`)
	math = strings.ReplaceAll(math, `\nabla`, `∇`)
	math = strings.ReplaceAll(math, `\in`, `∈`)
	math = strings.ReplaceAll(math, `\notin`, `∉`)
	math = strings.ReplaceAll(math, `\subset`, `⊂`)
	math = strings.ReplaceAll(math, `\supset`, `⊃`)
	math = strings.ReplaceAll(math, `\subseteq`, `⊆`)
	math = strings.ReplaceAll(math, `\supseteq`, `⊇`)

	return math
}

func formatMarkdownForTerminal(s string) string {
	// 1. Replace markdown bold **text** with ANSI bold cyan
	boldRe := regexp.MustCompile(`\*\*([^\*]+)\*\*`)
	s = boldRe.ReplaceAllStringFunc(s, func(match string) string {
		sub := boldRe.FindStringSubmatch(match)
		if len(sub) < 2 {
			return match
		}
		return fmt.Sprintf("\033[1;36m%s\033[0m", sub[1])
	})

	// 2. Format headers
	headerRe := regexp.MustCompile(`(?m)^(#{1,6})\s*(.*?)\s*$`)
	s = headerRe.ReplaceAllStringFunc(s, func(match string) string {
		sub := headerRe.FindStringSubmatch(match)
		if len(sub) < 3 {
			return match
		}
		return fmt.Sprintf("\n\033[1;33m%s\033[0m\n", sub[2])
	})

	return s
}

func formatCitations(s string) (string, []string) {
	re := regexp.MustCompile(`\[\[([^\]|#]+)(?:\|[^\]]+)?\]\]`)
	matches := re.FindAllStringSubmatch(s, -1)

	slugMap := make(map[string]int)
	var slugs []string
	for _, m := range matches {
		slug := strings.TrimSpace(m[1])
		if _, exists := slugMap[slug]; !exists {
			slugMap[slug] = len(slugs) + 1
			slugs = append(slugs, slug)
		}
	}

	result := re.ReplaceAllStringFunc(s, func(match string) string {
		sub := re.FindStringSubmatch(match)
		if len(sub) < 2 {
			return match
		}
		slug := strings.TrimSpace(sub[1])
		idx := slugMap[slug]
		return fmt.Sprintf("\033[1;36m[%d]\033[0m", idx)
	})

	return result, slugs
}
