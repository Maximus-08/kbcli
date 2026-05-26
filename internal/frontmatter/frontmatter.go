package frontmatter

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

type SourceFrontmatter struct {
	Title     string   `yaml:"title"`
	SourceURL string   `yaml:"source_url"`
	Author    string   `yaml:"author"`
	DateAdded string   `yaml:"date_added"`
	Type      string   `yaml:"type"` // article|linkedin-post|paper|video|tool
	Tags      []string `yaml:"tags"`
	Status    string   `yaml:"status"` // uncompiled|compiled
}

type WikiFrontmatter struct {
	Title        string   `yaml:"title"`
	Type         string   `yaml:"type"` // concept|project|resource|synthesis|query-result
	Tags         []string `yaml:"tags"`
	Created      string   `yaml:"created"`
	Updated      string   `yaml:"updated"`
	Sources      []string `yaml:"sources"`
	Related      []string `yaml:"related"`
	Provenance   string   `yaml:"provenance"` // extracted|inferred|ambiguous
	Summary      string   `yaml:"summary"`
	CompiledFrom string   `yaml:"compiled_from"`
}

// Parse extracts raw frontmatter bytes and body text from a markdown file
func Parse(content []byte) (map[string]any, string, error) {
	fmBytes, body, err := splitFrontmatter(content)
	if err != nil {
		return nil, "", err
	}

	var fm map[string]any
	if len(fmBytes) > 0 {
		if err := yaml.Unmarshal(fmBytes, &fm); err != nil {
			return nil, "", fmt.Errorf("failed to parse YAML frontmatter: %w", err)
		}
	}
	return fm, body, nil
}

// ParseSource parses raw markdown into SourceFrontmatter and body robustly
func ParseSource(content []byte) (*SourceFrontmatter, string, error) {
	fmBytes, body, err := splitFrontmatter(content)
	if err != nil {
		return &SourceFrontmatter{}, string(content), nil
	}

	var sf SourceFrontmatter
	if len(fmBytes) > 0 {
		if err := yaml.Unmarshal(fmBytes, &sf); err != nil {
			// YAML parsing failed, fallback to robust parsing
			sf = parseSourceRobustly(fmBytes)
		}
	}

	// If title is empty, search body for a markdown heading '# Title'
	if sf.Title == "" {
		sf.Title = extractTitleFromHeading(body)
	}

	return &sf, body, nil
}

// ParseWiki parses raw markdown into WikiFrontmatter and body
func ParseWiki(content []byte) (*WikiFrontmatter, string, error) {
	fmBytes, body, err := splitFrontmatter(content)
	if err != nil {
		return nil, "", err
	}

	var wf WikiFrontmatter
	if len(fmBytes) > 0 {
		if err := yaml.Unmarshal(fmBytes, &wf); err != nil {
			return nil, "", fmt.Errorf("failed to parse Wiki YAML frontmatter: %w", err)
		}
	}
	return &wf, body, nil
}

func parseSourceRobustly(fmBytes []byte) SourceFrontmatter {
	var sf SourceFrontmatter
	fmStr := string(fmBytes)

	sf.Title = extractRegexField(fmStr, `(?i)\btitle\s*:\s*("[^"]*"|'[^']*'|[^:\n\r]+?)(?:\s+\w+\s*:|[\n\r]|$)`)
	sf.Status = extractRegexField(fmStr, `(?i)\bstatus\s*:\s*("[^"]*"|'[^']*'|[^:\n\r]+?)(?:\s+\w+\s*:|[\n\r]|$)`)
	sf.SourceURL = extractRegexField(fmStr, `(?i)\bsource_url\s*:\s*("[^"]*"|'[^']*'|[^:\n\r]+?)(?:\s+\w+\s*:|[\n\r]|$)`)
	sf.Author = extractRegexField(fmStr, `(?i)\bauthor\s*:\s*("[^"]*"|'[^']*'|[^:\n\r]+?)(?:\s+\w+\s*:|[\n\r]|$)`)
	sf.DateAdded = extractRegexField(fmStr, `(?i)\bdate_added\s*:\s*("[^"]*"|'[^']*'|[^:\n\r]+?)(?:\s+\w+\s*:|[\n\r]|$)`)
	sf.Type = extractRegexField(fmStr, `(?i)\btype\s*:\s*("[^"]*"|'[^']*'|[^:\n\r]+?)(?:\s+\w+\s*:|[\n\r]|$)`)

	// Parse tags manually if it's an inline/yaml list
	tagsStr := extractRegexField(fmStr, `(?i)\btags\s*:\s*("[^"]*"|'[^']*'|[^:\n\r]+?)(?:\s+\w+\s*:|[\n\r]|$)`)
	if tagsStr != "" {
		tagsStr = strings.Trim(tagsStr, "[]\"' ")
		parts := strings.Split(tagsStr, ",")
		for _, part := range parts {
			part = strings.TrimSpace(part)
			if part != "" {
				sf.Tags = append(sf.Tags, part)
			}
		}
	} else {
		// Try line-by-line bullet points for tags
		lines := strings.Split(fmStr, "\n")
		inTags := false
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(strings.ToLower(line), "tags:") {
				inTags = true
				continue
			}
			if inTags {
				if line == "" || strings.Contains(line, ":") {
					inTags = false
					continue
				}
				if strings.HasPrefix(line, "- ") {
					tag := strings.Trim(line[2:], "\"'[] ")
					if tag != "" {
						sf.Tags = append(sf.Tags, tag)
					}
				}
			}
		}
	}

	return sf
}

func extractRegexField(content, pattern string) string {
	re := regexp.MustCompile(pattern)
	matches := re.FindStringSubmatch(content)
	if len(matches) > 1 {
		val := strings.TrimSpace(matches[1])
		return strings.Trim(val, `"' `)
	}
	return ""
}

func extractTitleFromHeading(body string) string {
	lines := strings.Split(body, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "# ") {
			return strings.TrimSpace(line[2:])
		}
	}
	return ""
}

// Marshal combines frontmatter struct/map and body into a single markdown byte slice
func Marshal(fm any, body string) ([]byte, error) {
	fmBytes, err := yaml.Marshal(fm)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal YAML frontmatter: %w", err)
	}

	var buf bytes.Buffer
	buf.WriteString("---\n")
	buf.Write(fmBytes)
	buf.WriteString("---\n")
	buf.WriteString(strings.TrimSpace(body))
	buf.WriteString("\n")

	return buf.Bytes(), nil
}

func splitFrontmatter(content []byte) ([]byte, string, error) {
	trimmed := bytes.TrimSpace(content)
	if !bytes.HasPrefix(trimmed, []byte("---\n")) && !bytes.HasPrefix(trimmed, []byte("---\r\n")) {
		return nil, string(content), nil
	}

	// Find the second "---" delimiter
	delim := []byte("---\n")
	startIdx := 4
	if bytes.HasPrefix(trimmed, []byte("---\r\n")) {
		delim = []byte("---\r\n")
		startIdx = 5
	}

	endIdx := bytes.Index(trimmed[startIdx:], delim)
	if endIdx == -1 {
		return nil, string(content), nil
	}

	fmBytes := trimmed[startIdx : startIdx+endIdx]
	bodyBytes := trimmed[startIdx+endIdx+len(delim):]

	return fmBytes, string(bodyBytes), nil
}
