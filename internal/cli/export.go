package cli

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/avnis/kb-system/internal/frontmatter"
	"github.com/avnis/kb-system/internal/vault"
	"github.com/spf13/cobra"

	"github.com/fumiama/go-docx"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	extast "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/text"
)

var (
	exportAll    bool
	exportForce  bool
	exportOutDir string
)

var wikiLinkRegex = regexp.MustCompile(`\[\[([^\]|#]+)(?:#([^\]|]+))?(?:\|([^\]]+))?\]\]`)
var commentRegex = regexp.MustCompile(`%%[\s\S]*?%%`)
var blockRefRegex = regexp.MustCompile(` \^[a-zA-Z0-9-]+(?:\s|$)`)
var calloutRegex = regexp.MustCompile(`(?m)^(\s*>\s*)\[!([a-zA-Z_-]+)\]\s*(.*)$`)
var metadataRegex = regexp.MustCompile(`(?i)(?:---\s*\n+)?(?:##+|###+)\s+Metadata\s*\n+[\s\S]*?$`)
var mathInlineRegex = regexp.MustCompile(`\$([^$\n]+?)\$`)
var mathBlockRegex = regexp.MustCompile(`\$\$([\s\S]+?)\$\$`)

var latexReplacements = map[string]string{
	`\vert`:        "|",
	`\|`:           "|",
	`\langle`:      "⟨",
	`\rangle`:      "⟩",
	`\alpha`:       "α",
	`\beta`:        "β",
	`\gamma`:       "γ",
	`\delta`:       "δ",
	`\epsilon`:     "ε",
	`\theta`:       "θ",
	`\lambda`:      "λ",
	`\pi`:          "π",
	`\psi`:         "ψ",
	`\Phi`:         "Φ",
	`\sigma`:       "σ",
	`\tau`:         "τ",
	`\log`:         "log",
	`\exp`:         "exp",
	`\approx`:      "≈",
	`\mathbb{1}`:   "𝟙",
	`\mathbb{R}`:   "ℝ",
	`\sum`:         "∑",
	`\mathcal{L}`:  "ℒ",
	`\neq`:         "≠",
	`\left`:        "",
	`\right`:       "",
	`\,-`:          " ",
	`\,`:           "",
	`\;`:           " ",
}

func findMatchingBrace(s string, start int) int {
	if start < 0 || start >= len(s) || s[start] != '{' {
		return -1
	}
	count := 1
	for i := start + 1; i < len(s); i++ {
		if s[i] == '{' {
			count++
		} else if s[i] == '}' {
			count--
			if count == 0 {
				return i
			}
		}
	}
	return -1
}

func cleanFractions(s string) string {
	for {
		idx := strings.Index(s, `\frac{`)
		if idx == -1 {
			break
		}
		start1 := idx + 5
		end1 := findMatchingBrace(s, start1)
		if end1 == -1 {
			break
		}
		if end1+1 >= len(s) || s[end1+1] != '{' {
			break
		}
		start2 := end1 + 1
		end2 := findMatchingBrace(s, start2)
		if end2 == -1 {
			break
		}
		num := s[start1+1 : end1]
		den := s[start2+1 : end2]

		numCleaned := cleanFractions(num)
		denCleaned := cleanFractions(den)

		replaced := numCleaned + "/" + denCleaned
		s = s[:idx] + replaced + s[end2+1:]
	}
	return s
}

func cleanSquareRoots(s string) string {
	for {
		idx := strings.Index(s, `\sqrt{`)
		if idx == -1 {
			break
		}
		start := idx + 5
		end := findMatchingBrace(s, start)
		if end == -1 {
			break
		}
		content := s[start+1 : end]
		contentCleaned := cleanSquareRoots(content)
		s = s[:idx] + "√" + contentCleaned + s[end+1:]
	}
	return s
}

func cleanTextMacros(s string) string {
	for {
		idx := strings.Index(s, `\text{`)
		if idx == -1 {
			break
		}
		start := idx + 5
		end := findMatchingBrace(s, start)
		if end == -1 {
			break
		}
		content := s[start+1 : end]
		contentCleaned := cleanTextMacros(content)
		s = s[:idx] + contentCleaned + s[end+1:]
	}
	return s
}

func toSuperscript(s string) string {
	m := map[rune]rune{
		'0': '⁰', '1': '¹', '2': '²', '3': '³', '4': '⁴',
		'5': '⁵', '6': '⁶', '7': '⁷', '8': '⁸', '9': '⁹',
		'+': '⁺', '-': '⁻', '=': '⁼', '(': '⁽', ')': '⁾',
		'n': 'ⁿ', 'i': 'ⁱ', 'T': 'ᵀ', 'N': 'ᴺ',
		'[': '[', ']': ']',
	}
	var sb strings.Builder
	for _, r := range s {
		if val, ok := m[r]; ok {
			sb.WriteRune(val)
		} else {
			sb.WriteRune(r)
		}
	}
	return sb.String()
}

func toSubscript(s string) string {
	m := map[rune]rune{
		'0': '₀', '1': '₁', '2': '₂', '3': '₃', '4': '₄',
		'5': '₅', '6': '₆', '7': '₇', '8': '₈', '9': '₉',
		'+': '₊', '-': '₋', '=': '₌', '(': '₍', ')': '₎',
		'a': 'ₐ', 'e': 'ₑ', 'h': 'ₕ', 'i': 'ᵢ', 'j': 'ⱼ',
		'k': 'ₖ', 'l': 'ₗ', 'm': 'ₘ', 'n': 'ₙ', 'o': 'ₒ',
		'p': 'ₚ', 'r': 'ᵣ', 's': 'ₛ', 't': 'ₜ', 'u': 'ᵤ',
		'v': 'ᵥ', 'x': 'ₓ',
		'[': '[', ']': ']',
	}
	var sb strings.Builder
	for _, r := range s {
		if val, ok := m[r]; ok {
			sb.WriteRune(val)
		} else {
			sb.WriteRune(r)
		}
	}
	return sb.String()
}

func cleanSupSubBraces(s string) string {
	for {
		idx := strings.Index(s, "^{")
		if idx == -1 {
			break
		}
		end := findMatchingBrace(s, idx+1)
		if end == -1 {
			break
		}
		content := s[idx+2 : end]
		s = s[:idx] + toSuperscript(content) + s[end+1:]
	}
	for {
		idx := strings.Index(s, "_{")
		if idx == -1 {
			break
		}
		end := findMatchingBrace(s, idx+1)
		if end == -1 {
			break
		}
		content := s[idx+2 : end]
		s = s[:idx] + toSubscript(content) + s[end+1:]
	}
	return s
}

func cleanLatex(latex string) string {
	for key, val := range latexReplacements {
		latex = strings.ReplaceAll(latex, key, val)
	}

	latex = cleanFractions(latex)
	latex = cleanSquareRoots(latex)
	latex = cleanTextMacros(latex)
	latex = cleanSupSubBraces(latex)

	superscripts := map[string]string{
		`^+`: "⁺",
		`^-`: "⁻",
		`^T`: "ᵀ",
		`^n`: "ⁿ",
		`^2`: "²",
		`^3`: "³",
		`^N`: "ᴺ",
	}
	for key, val := range superscripts {
		latex = strings.ReplaceAll(latex, key, val)
	}

	subscripts := map[string]string{
		`_0`: "₀",
		`_1`: "₁",
		`_2`: "₂",
		`_3`: "₃",
		`_i`: "ᵢ",
		`_j`: "ⱼ",
		`_k`: "ₖ",
		`_n`: "ₙ",
	}
	for key, val := range subscripts {
		latex = strings.ReplaceAll(latex, key, val)
	}

	latex = strings.ReplaceAll(latex, `\`, "")

	return latex
}

func cleanMathText(txt string) string {
	txt = mathBlockRegex.ReplaceAllStringFunc(txt, func(match string) string {
		if len(match) < 4 {
			return match
		}
		content := match[2 : len(match)-2]
		return cleanLatex(content)
	})

	txt = mathInlineRegex.ReplaceAllStringFunc(txt, func(match string) string {
		if len(match) < 2 {
			return match
		}
		content := match[1 : len(match)-1]
		return cleanLatex(content)
	})

	return txt
}

func addText(p *docx.Paragraph, text string) *docx.Run {
	run := p.AddText(text)
	for _, child := range run.Children {
		if t, ok := child.(*docx.Text); ok {
			t.XMLSpace = "preserve"
		}
	}
	if run.RunProperties == nil {
		run.RunProperties = &docx.RunProperties{}
	}
	run.RunProperties.Fonts = &docx.RunFonts{ASCII: "Calibri", HAnsi: "Calibri"}
	run.Color("333333")
	return run
}

func handleTextRun(doc *docx.Docx, currentParagraph *docx.Paragraph, txt string, inHeading int, inBlockquote, inBold, inItalic, inCode, inTableHeader bool) *docx.Paragraph {
	if currentParagraph == nil {
		currentParagraph = doc.AddParagraph()
	}
	run := addText(currentParagraph, txt)
	if inHeading > 0 {
		run.Bold()
		run.RunProperties.Fonts = &docx.RunFonts{ASCII: "Segoe UI Semibold", HAnsi: "Segoe UI Semibold"}
		switch inHeading {
		case 1:
			run.Size("32")
			run.Color("1F385C")
		case 2:
			run.Size("26")
			run.Color("2E4057")
		case 3:
			run.Size("22")
			run.Color("4A5568")
		case 4:
			run.Size("20")
			run.Color("718096")
		}
	}
	if inBlockquote {
		run.Italic()
		run.Color("5A6A85")
	}
	if inBold {
		run.Bold()
	}
	if inItalic {
		run.Italic()
	}
	if inCode {
		run.Color("C7254E")
		run.Shade("clear", "auto", "F9F2F4")
		run.RunProperties.Fonts = &docx.RunFonts{ASCII: "Consolas", HAnsi: "Consolas"}
	}
	if inTableHeader {
		run.Color("FFFFFF")
		run.Bold()
	}
	return currentParagraph
}

func cleanBody(content string) string {
	content = commentRegex.ReplaceAllString(content, "")
	content = blockRefRegex.ReplaceAllString(content, "")
	content = metadataRegex.ReplaceAllString(content, "")

	// Clean LaTeX formulas before Goldmark parses Markdown to avoid splitting subscripts/characters
	content = cleanMathText(content)

	content = calloutRegex.ReplaceAllStringFunc(content, func(match string) string {
		submatches := calloutRegex.FindStringSubmatch(match)
		if len(submatches) < 4 {
			return match
		}
		prefix := submatches[1]
		cType := strings.ToUpper(submatches[2])
		header := strings.TrimSpace(submatches[3])

		if header == "" {
			return fmt.Sprintf("%s**%s**", prefix, cType)
		}

		if strings.Contains(header, ":") {
			return fmt.Sprintf("%s**%s - %s**", prefix, cType, header)
		}
		return fmt.Sprintf("%s**%s: %s**", prefix, cType, header)
	})

	content = wikiLinkRegex.ReplaceAllStringFunc(content, func(match string) string {
		submatches := wikiLinkRegex.FindStringSubmatch(match)
		if len(submatches) < 4 {
			return match
		}
		display := strings.TrimSpace(submatches[3])
		if display != "" {
			return display
		}
		target := strings.TrimSpace(submatches[1])
		if idx := strings.LastIndex(target, "/"); idx != -1 {
			target = target[idx+1:]
		}
		if strings.HasSuffix(strings.ToLower(target), ".md") {
			target = target[:len(target)-3]
		}
		target = strings.ReplaceAll(target, "-", " ")
		target = strings.ReplaceAll(target, "_", " ")
		return target
	})

	return content
}

func reorderSectPr(doc *docx.Docx) {
	var sectPr *docx.SectPr
	newItems := make([]interface{}, 0, len(doc.Document.Body.Items))
	for _, item := range doc.Document.Body.Items {
		if sp, ok := item.(*docx.SectPr); ok {
			sectPr = sp
		} else {
			newItems = append(newItems, item)
		}
	}
	if sectPr != nil {
		newItems = append(newItems, sectPr)
	}
	doc.Document.Body.Items = newItems
}

var exportCmd = &cobra.Command{
	Use:   "export [file...]",
	Short: "Export wiki articles to Microsoft Word (.docx) format",
	RunE: func(cmd *cobra.Command, args []string) error {
		outDir := exportOutDir
		if outDir == "" {
			outDir = filepath.Join(cfg.VaultKBPath, "export")
		}
		if err := os.MkdirAll(outDir, 0755); err != nil {
			return fmt.Errorf("failed to create output directory: %w", err)
		}

		var targets []string
		if exportAll {
			wikiDir := vault.WikiDir(cfg)
			entries, err := os.ReadDir(wikiDir)
			if err != nil {
				return fmt.Errorf("failed to read wiki directory: %w", err)
			}
			for _, entry := range entries {
				if entry.IsDir() || filepath.Ext(entry.Name()) != ".md" || entry.Name() == "INDEX.md" {
					continue
				}
				targets = append(targets, filepath.Join(wikiDir, entry.Name()))
			}
		} else {
			for _, arg := range args {
				var path string
				if filepath.IsAbs(arg) {
					path = arg
				} else {
					if _, err := os.Stat(arg); err == nil {
						path, _ = filepath.Abs(arg)
					} else {
						path = filepath.Join(vault.WikiDir(cfg), arg)
						if !strings.HasSuffix(path, ".md") {
							path = path + ".md"
						}
					}
				}
				targets = append(targets, path)
			}
		}

		if len(targets) == 0 {
			logger.Info("No documents found to export")
			return nil
		}

		md := goldmark.New(
			goldmark.WithExtensions(
				extension.Table,
			),
		)

		for _, target := range targets {
			content, err := os.ReadFile(target)
			if err != nil {
				logger.Error("Failed to read file", "path", target, "error", err)
				continue
			}

			fm, body, err := frontmatter.ParseWiki(content)
			if err != nil {
				logger.Error("Failed to parse wiki frontmatter", "path", target, "error", err)
				continue
			}

			title := ""
			if fm != nil {
				title = fm.Title
			}
			if title == "" {
				base := filepath.Base(target)
				title = strings.TrimSuffix(base, filepath.Ext(base))
				title = strings.ReplaceAll(title, "-", " ")
				title = strings.ReplaceAll(title, "_", " ")
			}

			cleanedBody := cleanBody(body)
			bodyBytes := []byte(cleanedBody)

			docNode := md.Parser().Parse(text.NewReader(bodyBytes))

			hasH1 := false
			if docNode.FirstChild() != nil && docNode.FirstChild().Kind() == ast.KindHeading {
				headingNode := docNode.FirstChild().(*ast.Heading)
				if headingNode.Level == 1 {
					hasH1 = true
				}
			}

			doc := docx.New().WithDefaultTheme().WithA4Page()

			if !hasH1 && title != "" {
				p := doc.AddParagraph()
				p.Properties = &docx.ParagraphProperties{
					Spacing: &docx.Spacing{Before: 360},
				}
				r := addText(p, title)
				r.Bold()
				r.Size("36")
				r.Color("1F385C")
				r.RunProperties.Fonts = &docx.RunFonts{ASCII: "Segoe UI Semibold", HAnsi: "Segoe UI Semibold"}
			}

			var currentParagraph *docx.Paragraph
			var currentTable *docx.Table
			var currentTableRow *docx.WTableRow
			var currentTableCell *docx.WTableCell
			var tableRowIdx int
			var tableColIdx int
			var inHeading int
			var inBlockquote bool
			var inBold bool
			var inItalic bool
			var inCode bool
			var listOrdered bool
			var listCounter int
			var inTableHeader bool

			err = ast.Walk(docNode, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
				switch node.Kind() {
				case ast.KindDocument:
					// do nothing
				case ast.KindHeading:
					if entering {
						inHeading = node.(*ast.Heading).Level
						currentParagraph = doc.AddParagraph()
						before := 120
						switch inHeading {
						case 1:
							before = 360
						case 2:
							before = 240
						case 3:
							before = 180
						}
						currentParagraph.Properties = &docx.ParagraphProperties{
							Spacing: &docx.Spacing{Before: before},
						}
					} else {
						inHeading = 0
						currentParagraph = nil
					}
				case ast.KindParagraph:
					if entering {
						if currentTableCell != nil {
							currentParagraph = currentTableCell.AddParagraph()
							currentParagraph.Properties = &docx.ParagraphProperties{
								Spacing: &docx.Spacing{Before: 60},
							}
						} else {
							currentParagraph = doc.AddParagraph()
							before := 120
							if inBlockquote {
								currentParagraph.Properties = &docx.ParagraphProperties{
									Ind:     &docx.Ind{Left: 360},
									Spacing: &docx.Spacing{Before: before},
								}
							} else {
								currentParagraph.Properties = &docx.ParagraphProperties{
									Spacing: &docx.Spacing{Before: before},
								}
							}
						}
					} else {
						currentParagraph = nil
					}
				case ast.KindText:
					if entering {
						txt := string(node.(*ast.Text).Value(bodyBytes))
						if txt == "" {
							return ast.WalkContinue, nil
						}
						currentParagraph = handleTextRun(doc, currentParagraph, txt, inHeading, inBlockquote, inBold, inItalic, inCode, inTableHeader)
					}
				case ast.KindString:
					if entering {
						txt := string(node.(*ast.String).Value)
						if txt == "" {
							return ast.WalkContinue, nil
						}
						currentParagraph = handleTextRun(doc, currentParagraph, txt, inHeading, inBlockquote, inBold, inItalic, inCode, inTableHeader)
					}
				case ast.KindEmphasis:
					if entering {
						if node.(*ast.Emphasis).Level == 2 {
							inBold = true
						} else {
							inItalic = true
						}
					} else {
						if node.(*ast.Emphasis).Level == 2 {
							inBold = false
						} else {
							inItalic = false
						}
					}
				case ast.KindCodeSpan:
					if entering {
						inCode = true
					} else {
						inCode = false
					}
				case ast.KindCodeBlock, ast.KindFencedCodeBlock:
					if entering {
						lines := node.Lines()
						var sb strings.Builder
						for i := 0; i < lines.Len(); i++ {
							line := lines.At(i)
							sb.WriteString(string(line.Value(bodyBytes)))
						}
						codeText := sb.String()
						codeText = strings.ReplaceAll(codeText, "\r\n", "\n")
						codeText = strings.TrimSuffix(codeText, "\n")

						p := doc.AddParagraph()
						p.Properties = &docx.ParagraphProperties{
							Spacing: &docx.Spacing{
								Before:   120,
								Line:     200, // 200 dxa = 10pt (exactly matching the font size)
								LineRule: "exactly",
							},
							Ind:           &docx.Ind{Left: 720},
							Justification: &docx.Justification{Val: "left"},
						}
						run := addText(p, codeText)
						run.Size("20") // 10pt (in half-points)
						run.RunProperties.Fonts = &docx.RunFonts{
							ASCII:    "Consolas",
							HAnsi:    "Consolas",
							EastAsia: "Consolas",
						}
						run.Shade("clear", "auto", "F8F9FA")
						return ast.WalkSkipChildren, nil
					}
				case ast.KindBlockquote:
					if entering {
						inBlockquote = true
					} else {
						inBlockquote = false
					}
				case ast.KindList:
					if entering {
						listCounter = 1
						listOrdered = node.(*ast.List).IsOrdered()
					}
				case ast.KindListItem:
					if entering {
						currentParagraph = doc.AddParagraph()
						currentParagraph.Properties = &docx.ParagraphProperties{
							Spacing: &docx.Spacing{Before: 40},
							Ind:     &docx.Ind{Left: 720, Hanging: 360},
						}
						bullet := "•\t"
						if listOrdered {
							bullet = fmt.Sprintf("%d.\t", listCounter)
							listCounter++
						}
						addText(currentParagraph, bullet)
					} else {
						currentParagraph = nil
					}
				case extast.KindTable:
					if entering {
						rowCount := 0
						colCount := 0
						for child := node.FirstChild(); child != nil; child = child.NextSibling() {
							if child.Kind() == extast.KindTableHeader || child.Kind() == extast.KindTableRow {
								rowCount++
								if colCount == 0 {
									cCount := 0
									for cell := child.FirstChild(); cell != nil; cell = cell.NextSibling() {
										cCount++
									}
									colCount = cCount
								}
							}
						}
						if rowCount > 0 && colCount > 0 {
							borders := &docx.APITableBorderColors{
								Top:     "CCCCCC",
								Left:    "CCCCCC",
								Bottom:  "CCCCCC",
								Right:   "CCCCCC",
								InsideH: "CCCCCC",
								InsideV: "CCCCCC",
							}
							currentTable = doc.AddTable(rowCount, colCount, 9000, borders)
							tableRowIdx = 0
						}
					} else {
						currentTable = nil
					}
				case extast.KindTableHeader:
					if entering && currentTable != nil && tableRowIdx < len(currentTable.TableRows) {
						currentTableRow = currentTable.TableRows[tableRowIdx]
						tableColIdx = 0
						inTableHeader = true
					} else if !entering {
						tableRowIdx++
						currentTableRow = nil
						inTableHeader = false
					}
				case extast.KindTableRow:
					if entering && currentTable != nil && tableRowIdx < len(currentTable.TableRows) {
						currentTableRow = currentTable.TableRows[tableRowIdx]
						tableColIdx = 0
						inTableHeader = false
					} else if !entering {
						tableRowIdx++
						currentTableRow = nil
					}
				case extast.KindTableCell:
					if entering && currentTableRow != nil && tableColIdx < len(currentTableRow.TableCells) {
						currentTableCell = currentTableRow.TableCells[tableColIdx]
						if inTableHeader {
							currentTableCell.Shade("clear", "auto", "2E4057")
						} else if tableRowIdx%2 == 0 {
							currentTableCell.Shade("clear", "auto", "F7F9FA")
						}
						currentParagraph = currentTableCell.AddParagraph()
						currentParagraph.Properties = &docx.ParagraphProperties{
							Spacing: &docx.Spacing{Before: 60},
						}
					} else if !entering {
						tableColIdx++
						currentTableCell = nil
						currentParagraph = nil
					}
				}
				return ast.WalkContinue, nil
			})

			if err != nil {
				logger.Error("Failed walking AST", "path", target, "error", err)
				continue
			}

			reorderSectPr(doc)

			slug := strings.TrimSuffix(filepath.Base(target), filepath.Ext(target))
			outPath := filepath.Join(outDir, slug+".docx")

			if _, err := os.Stat(outPath); err == nil && !exportForce {
				logger.Info("File already exists, skipping", "path", outPath)
				continue
			}

			outFile, err := os.Create(outPath)
			if err != nil {
				logger.Error("Failed to create output file", "path", outPath, "error", err)
				continue
			}

			_, err = doc.WriteTo(outFile)
			outFile.Close()
			if err != nil {
				logger.Error("Failed to write docx", "path", outPath, "error", err)
				continue
			}

			if err := postProcessDocx(outPath); err != nil {
				logger.Error("Failed to post-process docx", "path", outPath, "error", err)
			} else {
				logger.Info("Exported and post-processed document successfully", "src", filepath.Base(target), "dest", outPath)
			}
		}

		return nil
	},
}

func postProcessDocx(filePath string) error {
	r, err := zip.OpenReader(filePath)
	if err != nil {
		return fmt.Errorf("failed to open zip reader: %w", err)
	}
	defer r.Close()

	tmpPath := filePath + ".tmp"
	tmpFile, err := os.Create(tmpPath)
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	defer tmpFile.Close()

	w := zip.NewWriter(tmpFile)
	defer w.Close()

	for _, f := range r.File {
		rc, err := f.Open()
		if err != nil {
			return fmt.Errorf("failed to open zip file member: %w", err)
		}

		fw, err := w.Create(f.Name)
		if err != nil {
			rc.Close()
			return fmt.Errorf("failed to create zip file member: %w", err)
		}

		if f.Name == "word/document.xml" {
			var sb strings.Builder
			_, err = io.Copy(&sb, rc)
			rc.Close()
			if err != nil {
				return fmt.Errorf("failed to read word/document.xml: %w", err)
			}

			xmlStr := sb.String()

			// Standardize and include cs="Consolas" for all Consolas runs
			reFonts := regexp.MustCompile(`<w:rFonts\s+[^>]*?Consolas[^>]*?(?:/>|></w:rFonts>)`)
			xmlStr = reFonts.ReplaceAllStringFunc(xmlStr, func(match string) string {
				return `<w:rFonts w:ascii="Consolas" w:hAnsi="Consolas" w:eastAsia="Consolas" w:cs="Consolas"></w:rFonts>`
			})

			// Add <w:noProof/> for all Consolas runs to disable spelling and grammar checks
			xmlStr = strings.ReplaceAll(xmlStr,
				`<w:rFonts w:ascii="Consolas" w:hAnsi="Consolas" w:eastAsia="Consolas" w:cs="Consolas"></w:rFonts>`,
				`<w:rFonts w:ascii="Consolas" w:hAnsi="Consolas" w:eastAsia="Consolas" w:cs="Consolas"></w:rFonts><w:noProof/>`,
			)

			_, err = fw.Write([]byte(xmlStr))
			if err != nil {
				return fmt.Errorf("failed to write modified word/document.xml: %w", err)
			}
		} else {
			_, err = io.Copy(fw, rc)
			rc.Close()
			if err != nil {
				return fmt.Errorf("failed to copy zip file member: %w", err)
			}
		}
	}

	w.Close()
	tmpFile.Close()
	r.Close()

	if err := os.Remove(filePath); err != nil {
		return fmt.Errorf("failed to remove original file: %w", err)
	}
	if err := os.Rename(tmpPath, filePath); err != nil {
		return fmt.Errorf("failed to rename temp file: %w", err)
	}

	return nil
}

func init() {
	exportCmd.Flags().BoolVarP(&exportAll, "all", "a", false, "Export all wiki articles")
	exportCmd.Flags().BoolVarP(&exportForce, "force", "f", false, "Overwrite existing DOCX files")
	exportCmd.Flags().StringVarP(&exportOutDir, "output", "o", "", "Output directory for exported DOCX files (defaults to <vault>/export)")
	rootCmd.AddCommand(exportCmd)
}
