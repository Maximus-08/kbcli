package cleaner

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

	"github.com/avnis/kb-system/internal/config"
	"github.com/avnis/kb-system/internal/frontmatter"
	"github.com/avnis/kb-system/internal/index"
	"github.com/avnis/kb-system/internal/provider"
	"github.com/avnis/kb-system/internal/vault"
	"github.com/avnis/kb-system/prompts"
)

type CandidateType string

const (
	CandidateOrphan    CandidateType = "orphan"
	CandidateRedundant CandidateType = "redundant"
)

type Candidate struct {
	Path    string
	Type    CandidateType
	Reason  string
	Details string
}

type Cleaner struct {
	cfg    *config.Config
	prov   provider.Provider
	logger *slog.Logger
}

func New(cfg *config.Config, prov provider.Provider, logger *slog.Logger) *Cleaner {
	return &Cleaner{
		cfg:    cfg,
		prov:   prov,
		logger: logger,
	}
}

var wikilinkRegex = regexp.MustCompile(`\[\[([^\]|#]+)(?:\|[^\]]+)?\]\]`)

type cacheEntry struct {
	MTimeA int64 `json:"mtime_a"`
	MTimeB int64 `json:"mtime_b"`
}

type cleanupCache struct {
	VerifiedPairs map[string]cacheEntry `json:"verified_pairs"`
}

func (c *Cleaner) DetectCandidates(ctx context.Context) ([]Candidate, error) {
	var candidates []Candidate

	wikiDir := vault.WikiDir(c.cfg)
	indexPath := vault.IndexPath(c.cfg)

	// Load cleanup cache
	cachePath := filepath.Join(c.cfg.VaultKBPath, ".cleanup_cache.json")
	cache := cleanupCache{VerifiedPairs: make(map[string]cacheEntry)}
	cacheBytes, err := os.ReadFile(cachePath)
	if err == nil {
		_ = json.Unmarshal(cacheBytes, &cache)
	}
	cacheModified := false

	// 1. Gather all wiki articles and their mod times
	wikiFiles := make(map[string]string) // lowercase slug -> absolute path
	fileMTimes := make(map[string]int64) // lowercase slug -> unix timestamp
	var wikiSlugs []string
	entries, err := os.ReadDir(wikiDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read wiki directory: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() || strings.EqualFold(entry.Name(), "INDEX.md") || filepath.Ext(entry.Name()) != ".md" {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		slug := strings.TrimSuffix(strings.ToLower(entry.Name()), ".md")
		wikiFiles[slug] = filepath.Join(wikiDir, entry.Name())
		wikiSlugs = append(wikiSlugs, slug)
		fileMTimes[slug] = info.ModTime().Unix()
	}

	// Clean up stale cache entries for files that no longer exist
	for key := range cache.VerifiedPairs {
		parts := strings.Split(key, ":")
		if len(parts) != 2 {
			delete(cache.VerifiedPairs, key)
			cacheModified = true
			continue
		}
		slugA := parts[0]
		slugB := parts[1]
		if _, existsA := wikiFiles[slugA]; !existsA {
			delete(cache.VerifiedPairs, key)
			cacheModified = true
		} else if _, existsB := wikiFiles[slugB]; !existsB {
			delete(cache.VerifiedPairs, key)
			cacheModified = true
		}
	}

	// 2. Read INDEX.md
	indexedSlugs := make(map[string]bool)
	indexEntries, err := index.Read(indexPath)
	if err == nil {
		for _, e := range indexEntries {
			indexedSlugs[strings.ToLower(e.Slug)] = true
		}
	}

	// 3. Track incoming links
	incomingLinks := make(map[string]int)
	fileContents := make(map[string]string)
	fileWikiFM := make(map[string]*frontmatter.WikiFrontmatter)

	for slug, path := range wikiFiles {
		contentBytes, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		wf, body, err := frontmatter.ParseWiki(contentBytes)
		if err != nil {
			continue
		}
		fileContents[slug] = body
		fileWikiFM[slug] = wf

		// Scan links in body
		matches := wikilinkRegex.FindAllStringSubmatch(body, -1)
		for _, match := range matches {
			if len(match) < 2 {
				continue
			}
			targetSlug := strings.ToLower(strings.TrimSpace(match[1]))
			incomingLinks[targetSlug]++
		}
	}

	// 4. Find Orphan candidates
	for slug, path := range wikiFiles {
		if !indexedSlugs[slug] && incomingLinks[slug] == 0 {
			relPath, _ := filepath.Rel(c.cfg.VaultKBPath, path)
			candidates = append(candidates, Candidate{
				Path:    path,
				Type:    CandidateOrphan,
				Reason:  "Article has no incoming links from other wiki files and is not listed in INDEX.md.",
				Details: fmt.Sprintf("File: %s", relPath),
			})
		}
	}

	// 5. Find Redundant candidates (pairwise comparison with Jaccard heuristic)
	var pairsToVerify []redundancyPair
	compared := make(map[string]bool)
	for i := 0; i < len(wikiSlugs); i++ {
		for j := i + 1; j < len(wikiSlugs); j++ {
			slugA := wikiSlugs[i]
			slugB := wikiSlugs[j]

			key := fmt.Sprintf("%s:%s", slugA, slugB)
			if compared[key] {
				continue
			}
			compared[key] = true

			fmA := fileWikiFM[slugA]
			fmB := fileWikiFM[slugB]
			if fmA == nil || fmB == nil {
				continue
			}

			// Heuristic: only run LLM comparison if titles share at least one word
			if !titleOverlap(fmA.Title, fmB.Title) {
				continue
			}

			// Cache check: skip if already verified as non-redundant and unmodified!
			mtimeA := fileMTimes[slugA]
			mtimeB := fileMTimes[slugB]
			if cached, exists := cache.VerifiedPairs[key]; exists {
				if cached.MTimeA == mtimeA && cached.MTimeB == mtimeB {
					c.logger.Debug("Redundancy check cached and clean; skipping", "pair", key)
					continue
				}
			}

			pairsToVerify = append(pairsToVerify, redundancyPair{
				PairID:   key,
				TitleA:   fmA.Title,
				SummaryA: fmA.Summary,
				ContentA: fileContents[slugA],
				TitleB:   fmB.Title,
				SummaryB: fmB.Summary,
				ContentB: fileContents[slugB],
				SlugA:    slugA,
				SlugB:    slugB,
			})
		}
	}

	// Process pairs in batches of 5 to optimize API request volume and avoid rate limits
	batchSize := 5
	for i := 0; i < len(pairsToVerify); i += batchSize {
		end := i + batchSize
		if end > len(pairsToVerify) {
			end = len(pairsToVerify)
		}
		batch := pairsToVerify[i:end]

		c.logger.Info("Checking redundancy batch...", "batchIndex", i/batchSize+1, "batchCount", (len(pairsToVerify)+batchSize-1)/batchSize, "pairsInBatch", len(batch))

		// Call LLM for batched overlap verification
		results, err := c.checkRedundancyBatch(ctx, batch)
		if err != nil {
			c.logger.Warn("Batched redundancy check failed, falling back to individual checks for this batch", "error", err)
			// Fallback: run individual check for each pair in the batch
			for _, pair := range batch {
				redundant, reason, err := c.checkRedundancy(ctx, fileWikiFM[pair.SlugA], pair.ContentA, fileWikiFM[pair.SlugB], pair.ContentB)
				if err != nil {
					c.logger.Debug("Redundancy check failed in individual fallback", "pair", pair.PairID, "error", err)
					continue
				}
				if redundant {
					c.addRedundantCandidate(&candidates, pair.SlugA, pair.SlugB, fileContents, wikiFiles, reason)
				} else {
					// Clean! Save to cache
					cache.VerifiedPairs[pair.PairID] = cacheEntry{
						MTimeA: fileMTimes[pair.SlugA],
						MTimeB: fileMTimes[pair.SlugB],
					}
					cacheModified = true
				}
			}
			continue
		}

		// Process successful batch results
		for _, pair := range batch {
			res, ok := results[pair.PairID]
			if !ok {
				// Fallback to individual check if pair key is missing from JSON response
				c.logger.Debug("Pair ID missing in batch JSON response, falling back to individual check", "pair", pair.PairID)
				redundant, reason, err := c.checkRedundancy(ctx, fileWikiFM[pair.SlugA], pair.ContentA, fileWikiFM[pair.SlugB], pair.ContentB)
				if err == nil {
					if redundant {
						c.addRedundantCandidate(&candidates, pair.SlugA, pair.SlugB, fileContents, wikiFiles, reason)
					} else {
						// Clean! Save to cache
						cache.VerifiedPairs[pair.PairID] = cacheEntry{
							MTimeA: fileMTimes[pair.SlugA],
							MTimeB: fileMTimes[pair.SlugB],
						}
						cacheModified = true
					}
				}
				continue
			}

			if res.Redundant {
				c.addRedundantCandidate(&candidates, pair.SlugA, pair.SlugB, fileContents, wikiFiles, res.Reason)
			} else {
				// Clean! Save to cache
				cache.VerifiedPairs[pair.PairID] = cacheEntry{
					MTimeA: fileMTimes[pair.SlugA],
					MTimeB: fileMTimes[pair.SlugB],
				}
				cacheModified = true
			}
		}
	}

	// Write cache back if updated
	if cacheModified {
		newCacheBytes, err := json.MarshalIndent(cache, "", "  ")
		if err == nil {
			_ = os.WriteFile(cachePath, newCacheBytes, 0644)
			c.logger.Debug("Saved cleanup cache", "path", cachePath, "entriesCount", len(cache.VerifiedPairs))
		}
	}

	return candidates, nil
}

func titleOverlap(t1, t2 string) bool {
	w1 := strings.Fields(strings.ToLower(t1))
	w2 := strings.Fields(strings.ToLower(t2))

	m1 := make(map[string]bool)
	for _, w := range w1 {
		// ignore small words
		if len(w) > 3 {
			m1[w] = true
		}
	}

	for _, w := range w2 {
		if len(w) > 3 && m1[w] {
			return true
		}
	}
	return false
}

type redundancyResponse struct {
	Redundant bool   `json:"redundant"`
	Reason    string `json:"reason"`
}

type redundancyPair struct {
	PairID   string `json:"pair_id"`
	TitleA   string `json:"title_a"`
	SummaryA string `json:"summary_a"`
	ContentA string `json:"content_a"`
	TitleB   string `json:"title_b"`
	SummaryB string `json:"summary_b"`
	ContentB string `json:"content_b"`
	SlugA    string `json:"-"`
	SlugB    string `json:"-"`
}

func (c *Cleaner) checkRedundancyBatch(ctx context.Context, batch []redundancyPair) (map[string]redundancyResponse, error) {
	tmpl, err := template.New("cleanup_batch").Parse(prompts.CleanupRedundancy)
	if err != nil {
		return nil, err
	}

	var promptBuf bytes.Buffer
	err = tmpl.Execute(&promptBuf, map[string]any{
		"Pairs": batch,
	})
	if err != nil {
		return nil, err
	}

	resp, err := c.prov.Generate(ctx, c.cfg.CleanupModel, promptBuf.String())
	if err != nil {
		return nil, err
	}

	// Parse JSON mapping from response
	var jsonStr string
	firstBrace := strings.Index(resp, "{")
	lastBrace := strings.LastIndex(resp, "}")
	if firstBrace != -1 && lastBrace != -1 && lastBrace > firstBrace {
		jsonStr = resp[firstBrace : lastBrace+1]
	} else {
		jsonStr = resp
	}

	var results map[string]redundancyResponse
	if err := json.Unmarshal([]byte(jsonStr), &results); err != nil {
		return nil, fmt.Errorf("failed to unmarshal batched redundancy JSON response: %w", err)
	}

	return results, nil
}

func (c *Cleaner) checkRedundancy(ctx context.Context, fmA *frontmatter.WikiFrontmatter, bodyA string, fmB *frontmatter.WikiFrontmatter, bodyB string) (bool, string, error) {
	// Fallback prompt execution expects single pair details
	tmpl, err := template.New("cleanup_single_fallback").Parse(prompts.CleanupRedundancy)
	if err != nil {
		return false, "", err
	}

	// Format single pair into a slice of 1 so it matches the expected CleanupRedundancy template schema
	pair := redundancyPair{
		PairID:   "single_pair",
		TitleA:   fmA.Title,
		SummaryA: fmA.Summary,
		ContentA: bodyA,
		TitleB:   fmB.Title,
		SummaryB: fmB.Summary,
		ContentB: bodyB,
	}

	var promptBuf bytes.Buffer
	err = tmpl.Execute(&promptBuf, map[string]any{
		"Pairs": []redundancyPair{pair},
	})
	if err != nil {
		return false, "", err
	}

	resp, err := c.prov.Generate(ctx, c.cfg.CleanupModel, promptBuf.String())
	if err != nil {
		return false, "", err
	}

	// Parse JSON mapping from response
	var jsonStr string
	firstBrace := strings.Index(resp, "{")
	lastBrace := strings.LastIndex(resp, "}")
	if firstBrace != -1 && lastBrace != -1 && lastBrace > firstBrace {
		jsonStr = resp[firstBrace : lastBrace+1]
	} else {
		jsonStr = resp
	}

	var results map[string]redundancyResponse
	if err := json.Unmarshal([]byte(jsonStr), &results); err == nil {
		if res, ok := results["single_pair"]; ok {
			return res.Redundant, res.Reason, nil
		}
	}

	// Fallback to unmarshaling as a single redundancyResponse directly in case the model responded in the old single-pair schema
	var rr redundancyResponse
	if err := json.Unmarshal([]byte(jsonStr), &rr); err == nil {
		return rr.Redundant, rr.Reason, nil
	}

	return false, "", fmt.Errorf("failed to parse fallback redundancy response")
}

func (c *Cleaner) addRedundantCandidate(candidates *[]Candidate, slugA, slugB string, fileContents map[string]string, wikiFiles map[string]string, reason string) {
	targetSlug := slugB
	sourceSlug := slugA
	if len(fileContents[slugA]) < len(fileContents[slugB]) {
		targetSlug = slugA
		sourceSlug = slugB
	}

	path := wikiFiles[targetSlug]
	relPath, _ := filepath.Rel(c.cfg.VaultKBPath, path)
	*candidates = append(*candidates, Candidate{
		Path:    path,
		Type:    CandidateRedundant,
		Reason:  fmt.Sprintf("High overlap with [[%s]]. Reason: %s", sourceSlug, reason),
		Details: fmt.Sprintf("File: %s", relPath),
	})
}

func TrashFile(cfg *config.Config, filePath string) (string, error) {
	trashDir := filepath.Join(cfg.VaultKBPath, ".trash")
	if err := os.MkdirAll(trashDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create trash directory: %w", err)
	}

	base := filepath.Base(filePath)
	ext := filepath.Ext(base)
	nameWithoutExt := strings.TrimSuffix(base, ext)

	targetPath := filepath.Join(trashDir, base)
	i := 1
	for {
		if _, err := os.Stat(targetPath); os.IsNotExist(err) {
			break
		}
		targetPath = filepath.Join(trashDir, fmt.Sprintf("%s.%d%s", nameWithoutExt, i, ext))
		i++
	}

	if err := os.Rename(filePath, targetPath); err != nil {
		return "", fmt.Errorf("failed to move file to trash: %w", err)
	}

	return targetPath, nil
}
