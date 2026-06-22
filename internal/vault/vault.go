package vault

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/avnis/kb-system/internal/config"
)

var defaultMappings = map[string]string{
	"qcnns":                                 "quantum-convolutional-neural-networks-qcnns",
	"mpsqcl":                                "matrix-product-state-quantum-contrastive-learning-mpsqcl",
	"quantum_contrastive_learning":          "quantum-enhanced-contrastive-learning",
	"quantum_foundation":                    "quantum-computing-foundation",
	"foundation":                            "quantum-computing-foundation",
	"qft":                                   "quantum-fourier-transform",
	"quantum_entanglement":                  "quantum-entanglement-separable-vs-entangled-states",
	"quantum entanglement":                  "quantum-entanglement-separable-vs-entangled-states",
	"quantum_teleportation":                 "quantum-teleportation-protocol",
	"quantum teleportation":                 "quantum-teleportation-protocol",
	"teleportation_advancements":            "quantum-teleportation-protocol",
	"quantum_computing_notes":               "quantum-computing-complete-course-notes",
	"chat1_02_quantum_contrastive_learning": "quantum-enhanced-contrastive-learning",
	"chat1_03_mpsqcl":                       "matrix-product-state-quantum-contrastive-learning-mpsqcl",
}

func RawDir(cfg *config.Config) string {
	return filepath.Join(cfg.VaultKBPath, "sources", "raw")
}

func WikiDir(cfg *config.Config) string {
	return filepath.Join(cfg.VaultKBPath, "wiki")
}

func MediaDir(cfg *config.Config) string {
	return filepath.Join(cfg.VaultKBPath, "wiki", "media")
}

func IndexPath(cfg *config.Config) string {
	return filepath.Join(cfg.VaultKBPath, "wiki", "INDEX.md")
}

func WikiFilePath(cfg *config.Config, slug string) string {
	return filepath.Join(cfg.VaultKBPath, "wiki", slug+".md")
}

func EnsureStructure(cfg *config.Config) error {
	raw := RawDir(cfg)
	wiki := WikiDir(cfg)
	media := MediaDir(cfg)

	if err := os.MkdirAll(raw, 0755); err != nil {
		return err
	}
	if err := os.MkdirAll(wiki, 0755); err != nil {
		return err
	}
	if err := os.MkdirAll(media, 0755); err != nil {
		return err
	}
	return nil
}

func MakeSlug(title string) string {
	s := strings.ToLower(title)
	reg := regexp.MustCompile(`[^a-z0-9]+`)
	s = reg.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	return s
}

func ResolveWikiLink(target string, existingSlugs map[string]bool) (string, bool) {
	// 1. Clean the target
	cleaned := strings.TrimSpace(target)
	if strings.HasSuffix(strings.ToLower(cleaned), ".md") {
		cleaned = cleaned[:len(cleaned)-3]
	}
	cleaned = strings.TrimSpace(cleaned)
	if cleaned == "" {
		return "", false
	}

	cleanedLower := strings.ToLower(cleaned)

	// 2. Check direct default mappings for raw filenames or concepts
	if resolved, found := defaultMappings[cleanedLower]; found {
		if existingSlugs[resolved] {
			return resolved, true
		}
	}

	// 3. Try standard slugification
	slug := MakeSlug(cleaned)
	if existingSlugs[slug] {
		return slug, true
	}

	// 4. Try matching case-insensitively with space-to-hyphen translation
	hyphenated := strings.ReplaceAll(cleanedLower, " ", "-")
	hyphenated = strings.ReplaceAll(hyphenated, "_", "-")
	if existingSlugs[hyphenated] {
		return hyphenated, true
	}

	// 5. Try prefix/partial matches against existing slugs to be robust
	if len(slug) > 3 {
		for existing := range existingSlugs {
			if strings.HasPrefix(existing, slug) || strings.Contains(existing, slug) {
				return existing, true
			}
		}
	}

	return "", false
}
