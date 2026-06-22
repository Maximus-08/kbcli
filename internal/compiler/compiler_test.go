package compiler

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/avnis/kb-system/internal/config"
	"github.com/avnis/kb-system/internal/index"
)

// MockProvider is a helper to mock LLM interactions
type MockProvider struct {
	GenerateFunc  func(ctx context.Context, model string, prompt string) (string, error)
	AvailableFunc func(ctx context.Context) (bool, error)
}

func (m *MockProvider) Generate(ctx context.Context, model string, prompt string) (string, error) {
	if m.GenerateFunc != nil {
		return m.GenerateFunc(ctx, model, prompt)
	}
	return "", nil
}

func (m *MockProvider) Name() string {
	return "MockProvider"
}

func (m *MockProvider) Available(ctx context.Context) (bool, error) {
	if m.AvailableFunc != nil {
		return m.AvailableFunc(ctx)
	}
	return true, nil
}

func TestRobustJSONParsing(t *testing.T) {
	// Test customUnescape
	t.Run("customUnescape", func(t *testing.T) {
		inputs := []struct {
			raw      string
			expected string
		}{
			{`\n`, "\n"},
			{`\t`, "\t"},
			{`\"`, `"`},
			{`\\`, `\`},
			{`plain text`, `plain text`},
		}
		for _, tc := range inputs {
			if got := customUnescape(tc.raw); got != tc.expected {
				t.Errorf("customUnescape(%q) = %q; want %q", tc.raw, got, tc.expected)
			}
		}
	})

	// Test isHex
	t.Run("isHex", func(t *testing.T) {
		tests := []struct {
			r        rune
			expected bool
		}{
			{'0', true}, {'9', true}, {'a', true}, {'f', true}, {'A', true}, {'F', true},
			{'g', false}, {'z', false}, {' ', false},
		}
		for _, tc := range tests {
			if got := isHex(tc.r); got != tc.expected {
				t.Errorf("isHex(%c) = %v; want %v", tc.r, got, tc.expected)
			}
		}
	})

	// Test escapeRawNewlinesInJSON
	t.Run("escapeRawNewlinesInJSON", func(t *testing.T) {
		input := "{\n\"key\": \"value\nwith newlines\"\n}"
		expected := "{\n\"key\": \"value\\nwith newlines\"\n}"
		if got := escapeRawNewlinesInJSON(input); got != expected {
			t.Errorf("escapeRawNewlinesInJSON() = %q; want %q", got, expected)
		}
	})

	// Test fixJSONBackslashes
	t.Run("fixJSONBackslashes", func(t *testing.T) {
		input := `{"body": "This has a \single backslash and an escaped \" quote"}`
		expected := `{"body": "This has a \\single backslash and an escaped \" quote"}`
		if got := fixJSONBackslashes(input); got != expected {
			t.Errorf("fixJSONBackslashes() = %q; want %q", got, expected)
		}
	})

	// Test extractFieldRobustly
	t.Run("extractFieldRobustly", func(t *testing.T) {
		jsonStr := `{"title": "My Title", "summary": "A short summary"}`
		if title := extractFieldRobustly(jsonStr, "title"); title != "My Title" {
			t.Errorf("expected 'My Title', got %q", title)
		}
		if summary := extractFieldRobustly(jsonStr, "summary"); summary != "A short summary" {
			t.Errorf("expected 'A short summary', got %q", summary)
		}
		if nonExistent := extractFieldRobustly(jsonStr, "nonexistent"); nonExistent != "" {
			t.Errorf("expected empty string for non-existent field, got %q", nonExistent)
		}
	})
}

func TestJSONRobustParsers(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	t.Run("parseJSONResponse", func(t *testing.T) {
		validResp := `{"title": "Quantum Entanglement", "summary": "Comprehensive overview", "body": "Body content", "tags": ["quantum", "physics"]}`
		res, err := parseJSONResponse(validResp, logger)
		if err != nil {
			t.Fatalf("parseJSONResponse failed: %v", err)
		}
		if res.Title != "Quantum Entanglement" || res.Body != "Body content" || len(res.Tags) != 2 {
			t.Errorf("parseJSONResponse parsed fields incorrectly: %+v", res)
		}

		// Try robust parsing on damaged JSON
		damagedResp := "Some introduction text...\n```json\n" + `{"title": "Quantum Entanglement", "summary": "Comprehensive overview", "body": "Body content", "tags": ["quantum", "physics"]}` + "\n```\nSome trailing text..."
		res2, err := parseJSONResponse(damagedResp, logger)
		if err != nil {
			t.Fatalf("parseJSONResponse failed on robust markdown parsing: %v", err)
		}
		if res2.Title != "Quantum Entanglement" {
			t.Errorf("robust parse failed to find title: %q", res2.Title)
		}
	})

	t.Run("parseSplitPlan", func(t *testing.T) {
		validSplitPlan := `{"articles": [{"title": "Part 1", "body": "Content 1"}, {"title": "Part 2", "body": "Content 2"}]}`
		plan, err := parseSplitPlanResponse(validSplitPlan)
		if err != nil {
			t.Fatalf("parseSplitPlanResponse failed: %v", err)
		}
		if len(plan.Articles) != 2 || plan.Articles[0].Title != "Part 1" {
			t.Errorf("parseSplitPlanResponse parsed incorrectly: %+v", plan)
		}
	})
}

func TestHasPotentialOverlap(t *testing.T) {
	entries := []index.Entry{
		{Slug: "quantum-computing-foundation"},
		{Slug: "quantum-fourier-transform"},
	}

	tests := []struct {
		title    string
		expected bool
	}{
		{"Quantum Computing Foundation", true},
		{"Quantum Fourier Transform", true},
		{"Quantum computing foundation", true},
		{"Quantum teleporter", false},
	}

	for _, tc := range tests {
		t.Run(tc.title, func(t *testing.T) {
			if got := hasPotentialOverlap(tc.title, entries); got != tc.expected {
				t.Errorf("hasPotentialOverlap(%q) = %v; want %v", tc.title, got, tc.expected)
			}
		})
	}
}

func TestCompilerFunctional(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "kb_compiler_test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create directories
	rawDir := filepath.Join(tempDir, "sources", "raw")
	wikiDir := filepath.Join(tempDir, "wiki")
	_ = os.MkdirAll(rawDir, 0755)
	_ = os.MkdirAll(wikiDir, 0755)

	cfg := &config.Config{
		VaultKBPath:        tempDir,
		CompileModelSingle: "test-model-single",
		LogLevel:           "error",
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))

	// Let's write an existing wiki article to simulate normalizations and collisions
	existingWikiPath := filepath.Join(wikiDir, "quantum-computing-foundation.md")
	existingContent := "---\ntitle: Quantum Computing Foundation\ncategory: Quantum\nstatus: active\n---\nBody here"
	_ = os.WriteFile(existingWikiPath, []byte(existingContent), 0644)

	// Also write an INDEX.md
	indexPath := filepath.Join(wikiDir, "INDEX.md")
	indexContent := "# INDEX\n\n- [[quantum-computing-foundation]]: Initial notes on quantum computing foundations\n"
	_ = os.WriteFile(indexPath, []byte(indexContent), 0644)

	// Mock LLM provider responses
	mockProvider := &MockProvider{
		GenerateFunc: func(ctx context.Context, model string, prompt string) (string, error) {
			if strings.Contains(prompt, "split") {
				// Split plan response
				return `{"articles": [{"title": "Quantum Basics", "body": "Introduction to qubits."}]}`, nil
			}
			// Compilation response
			return `{
				"title": "Quantum Basics",
				"summary": "Essential quantum concepts explained",
				"category": "Quantum-Basics",
				"tags": ["quantum", "qubit"],
				"sources": ["sources/raw/quantum_notes.md"],
				"body": "A qubit represents the basic unit of quantum information. See [[quantum_foundation]] for background."
			}`, nil
		},
	}

	c := New(cfg, mockProvider, logger)

	t.Run("normalizeLinks", func(t *testing.T) {
		input := "Check [[quantum_foundation]] or [[quantum computing foundation]]."
		expected := "Check [[quantum-computing-foundation|quantum_foundation]] or [[quantum-computing-foundation|quantum computing foundation]]."
		got := c.normalizeLinks(input)
		if got != expected {
			t.Errorf("normalizeLinks got %q, want %q", got, expected)
		}
	})

	t.Run("resolveCollision", func(t *testing.T) {
		// quantum-computing-foundation exists.
		// Collision should append a unique index.
		res := c.resolveCollision("quantum-computing-foundation", "sources/raw/new_notes.md")
		if res != "quantum-computing-foundation-2" {
			t.Errorf("resolveCollision got %q, want 'quantum-computing-foundation-2'", res)
		}
	})

	t.Run("CompileSingle", func(t *testing.T) {
		// Write a raw file to compile
		rawFilePath := filepath.Join(rawDir, "quantum_notes.md")
		rawFileContent := "---\ntitle: Quantum Notes\nstatus: uncompiled\n---\nThis is a long body content that is designed to exceed fifty characters in length so that the compiler does not skip it."
		if err := os.WriteFile(rawFilePath, []byte(rawFileContent), 0644); err != nil {
			t.Fatalf("failed to write raw file: %v", err)
		}

		err := c.CompileSingle(rawFilePath, false, false)
		if err != nil {
			t.Fatalf("CompileSingle failed: %v", err)
		}

		// Verify target wiki article was created
		targetWikiPath := filepath.Join(wikiDir, "quantum-basics.md")
		if _, err := os.Stat(targetWikiPath); err != nil {
			t.Errorf("expected wiki file to exist at %s, but got error: %v", targetWikiPath, err)
		}

		// Verify that the body was written and normalized
		wikiBytes, err := os.ReadFile(targetWikiPath)
		if err != nil {
			t.Fatalf("failed to read wiki file: %v", err)
		}
		wikiContent := string(wikiBytes)
		if !strings.Contains(wikiContent, "[[quantum-computing-foundation|quantum_foundation]]") {
			t.Errorf("expected wiki link in body to be normalized, got: %s", wikiContent)
		}

		// Verify raw file status has updated to compiled
		rawBytes, err := os.ReadFile(rawFilePath)
		if err != nil {
			t.Fatalf("failed to read raw file: %v", err)
		}
		if !strings.Contains(string(rawBytes), "status: compiled") {
			t.Errorf("expected raw file frontmatter status to be compiled, got: %s", string(rawBytes))
		}
	})
}

func TestProcessAndCopyImages(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "kb_image_test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create directories
	rawDir := filepath.Join(tempDir, "sources", "raw")
	wikiDir := filepath.Join(tempDir, "wiki")
	mediaDir := filepath.Join(tempDir, "wiki", "media")
	_ = os.MkdirAll(rawDir, 0755)
	_ = os.MkdirAll(wikiDir, 0755)
	_ = os.MkdirAll(mediaDir, 0755)

	cfg := &config.Config{
		VaultKBPath: tempDir,
	}

	// Create dummy image file in rawDir
	imgName := "diagram.png"
	imgPath := filepath.Join(rawDir, imgName)
	imgData := []byte("fake-image-bytes")
	if err := os.WriteFile(imgPath, imgData, 0644); err != nil {
		t.Fatalf("failed to write dummy image file: %v", err)
	}

	// Create dummy markdown file referencing the image
	mdPath := filepath.Join(rawDir, "note.md")

	c := New(cfg, &MockProvider{}, slog.Default())

	// Test Obsidian style, Markdown style, and no-extension image embeds
	body := "Here is Obsidian: ![[diagram.png|300]] and Markdown: ![Alt Text](diagram.png) and NoExt: ![[diagram]]"
	destSlug := "test-slug"
	rewritten := c.processAndCopyImages(body, []string{mdPath}, destSlug)

	expectedObsidian := "![[media/test-slug_diagram.png|300]]"
	expectedMarkdown := "![Alt Text](media/test-slug_diagram.png)"
	expectedNoExt := "![[media/test-slug_diagram.png]]"

	if !strings.Contains(rewritten, expectedObsidian) {
		t.Errorf("expected rewritten body to contain %q, got %q", expectedObsidian, rewritten)
	}
	if !strings.Contains(rewritten, expectedMarkdown) {
		t.Errorf("expected rewritten body to contain %q, got %q", expectedMarkdown, rewritten)
	}
	if !strings.Contains(rewritten, expectedNoExt) {
		t.Errorf("expected rewritten body to contain %q, got %q", expectedNoExt, rewritten)
	}

	// Verify the image was copied to mediaDir
	copiedImgPath := filepath.Join(mediaDir, "test-slug_diagram.png")
	copiedData, err := os.ReadFile(copiedImgPath)
	if err != nil {
		t.Fatalf("expected copied image to exist at %s, got error: %v", copiedImgPath, err)
	}

	if string(copiedData) != string(imgData) {
		t.Errorf("copied image data mismatch; got %q, want %q", string(copiedData), string(imgData))
	}
}

func TestPDFImageExtraction(t *testing.T) {
	pdfPath := "/mnt/c/ML/sources/raw/cheatsheet-convolutional-neural-networks.pdf"
	if _, err := os.Stat(pdfPath); os.IsNotExist(err) {
		t.Skip("Skipping PDF image extraction test because cheatsheet-convolutional-neural-networks.pdf is not available")
	}

	tempDir, err := os.MkdirTemp("", "kb_pdf_test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create directories
	rawDir := filepath.Join(tempDir, "sources", "raw")
	wikiDir := filepath.Join(tempDir, "wiki")
	mediaDir := filepath.Join(tempDir, "wiki", "media")
	_ = os.MkdirAll(rawDir, 0755)
	_ = os.MkdirAll(wikiDir, 0755)
	_ = os.MkdirAll(mediaDir, 0755)

	cfg := &config.Config{
		VaultKBPath:        tempDir,
		CompileModelSingle: "test-model-single",
		LogLevel:           "error",
	}

	// Copy PDF to rawDir
	testPDFPath := filepath.Join(rawDir, "cheatsheet.pdf")
	pdfBytes, err := os.ReadFile(pdfPath)
	if err != nil {
		t.Fatalf("failed to read test PDF: %v", err)
	}
	if err := os.WriteFile(testPDFPath, pdfBytes, 0644); err != nil {
		t.Fatalf("failed to write test PDF to raw: %v", err)
	}

	var calledPrompt string
	mockProvider := &MockProvider{
		GenerateFunc: func(ctx context.Context, model string, prompt string) (string, error) {
			calledPrompt = prompt

			// Find an image name in prompt
			imgName := "nonexistent.png"
			lines := strings.Split(prompt, "\n")
			for _, line := range lines {
				if strings.HasPrefix(line, "- ") {
					imgName = strings.TrimSpace(line[2:])
					break
				}
			}

			// Return a body referencing the extracted image
			return `{
				"title": "CNN Cheatsheet Compiled",
				"summary": "Convolutional Neural Networks cheatsheet content",
				"category": "Deep-Learning",
				"tags": ["cnn", "deep-learning"],
				"sources": ["sources/raw/cheatsheet.pdf"],
				"body": "This cheatsheet details CNN architectures. Here is a diagram: ![[` + imgName + `]]."
			}`, nil
		},
	}

	c := New(cfg, mockProvider, slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})))

	// Run CompileSingle
	err = c.CompileSingle(testPDFPath, false, false)
	if err != nil {
		t.Fatalf("CompileSingle failed: %v", err)
	}

	// Check if prompt had the AVAILABLE EMBEDDED IMAGES list
	if !strings.Contains(calledPrompt, "AVAILABLE EMBEDDED IMAGES:") {
		t.Errorf("expected prompt to contain image catalog, but it did not.")
	}

	// Verify that the images tempDir was cleaned up (not in compiler's maps)
	c.mu.Lock()
	tempDirsLen := len(c.pdfTempDirs)
	c.mu.Unlock()
	if tempDirsLen != 0 {
		t.Errorf("expected pdfTempDirs map to be empty, got size: %d", tempDirsLen)
	}

	// Extract the first image file name from prompt to verify it was copied
	imgName := ""
	lines := strings.Split(calledPrompt, "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, "- ") {
			imgName = strings.TrimSpace(line[2:])
			break
		}
	}

	if imgName == "" {
		t.Fatalf("No image names found in LLM prompt")
	}

	// Verify the image was copied to mediaDir and compiled markdown rewritten correctly
	destSlug := "cnn-cheatsheet-compiled"
	copiedImgPath := filepath.Join(mediaDir, destSlug+"_"+imgName)
	if _, err := os.Stat(copiedImgPath); os.IsNotExist(err) {
		t.Errorf("expected copied image to exist at %s, but it does not", copiedImgPath)
	}

	wikiFile := filepath.Join(wikiDir, destSlug+".md")
	wikiBytes, err := os.ReadFile(wikiFile)
	if err != nil {
		t.Fatalf("failed to read compiled wiki file: %v", err)
	}
	expectedEmbed := "![[media/" + destSlug + "_" + imgName + "]]"
	if !strings.Contains(string(wikiBytes), expectedEmbed) {
		t.Errorf("expected wiki file to contain %q, got: %s", expectedEmbed, string(wikiBytes))
	}
}

type MockMultimodalProvider struct {
	MockProvider
	GenerateMultimodalFunc func(ctx context.Context, model string, prompt string, images [][]byte, mimeTypes []string) (string, error)
}

func (m *MockMultimodalProvider) GenerateMultimodal(ctx context.Context, model string, prompt string, images [][]byte, mimeTypes []string) (string, error) {
	if m.GenerateMultimodalFunc != nil {
		return m.GenerateMultimodalFunc(ctx, model, prompt, images, mimeTypes)
	}
	return "{}", nil
}

func TestImageCache(t *testing.T) {
	tempFile, err := os.CreateTemp("", "image_cache_*.json")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	tempPath := tempFile.Name()
	tempFile.Close()
	defer os.Remove(tempPath)

	ic := NewImageCache(tempPath)
	ic.Set("hash1", "description 1")
	if err := ic.Save(); err != nil {
		t.Fatalf("failed to save cache: %v", err)
	}

	ic2 := NewImageCache(tempPath)
	desc, ok := ic2.Get("hash1")
	if !ok || desc != "description 1" {
		t.Errorf("expected 'description 1', got %q (ok=%v)", desc, ok)
	}
}

func TestCaptionImages(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "kb_caption_test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create dummy image file
	imgName := "image1.png"
	imgPath := filepath.Join(tempDir, imgName)
	if err := os.WriteFile(imgPath, []byte("fake-image-bytes-1"), 0644); err != nil {
		t.Fatalf("failed to write dummy image: %v", err)
	}

	cfg := &config.Config{
		VaultKBPath: tempDir,
	}

	calledMultimodal := false
	mockProvider := &MockMultimodalProvider{
		MockProvider: MockProvider{},
		GenerateMultimodalFunc: func(ctx context.Context, model string, prompt string, images [][]byte, mimeTypes []string) (string, error) {
			calledMultimodal = true
			if model != "gemini-2.5-flash" {
				t.Errorf("expected model 'gemini-2.5-flash', got %q", model)
			}
			// Return valid JSON mapping
			return `{"image1.png": "A simple blue square"}`, nil
		},
	}

	c := New(cfg, mockProvider, slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})))

	// Run captionImages
	captions, err := c.captionImages([]string{imgName}, tempDir)
	if err != nil {
		t.Fatalf("captionImages failed: %v", err)
	}

	if !calledMultimodal {
		t.Errorf("expected GenerateMultimodal to be called, but it was not")
	}

	desc, ok := captions[imgName]
	if !ok || desc != "A simple blue square" {
		t.Errorf("expected description 'A simple blue square', got %q", desc)
	}

	// Verify it was cached
	h, _ := ComputeSHA256(imgPath)
	cachedDesc, ok := c.imageCache.Get(h)
	if !ok || cachedDesc != "A simple blue square" {
		t.Errorf("expected description in cache to be 'A simple blue square', got %q", cachedDesc)
	}

	// Run again. Multimodal provider should NOT be called because it is cached!
	calledMultimodal = false
	captions2, err := c.captionImages([]string{imgName}, tempDir)
	if err != nil {
		t.Fatalf("second captionImages failed: %v", err)
	}

	if calledMultimodal {
		t.Errorf("expected GenerateMultimodal NOT to be called on second run (cached), but it was")
	}

	if captions2[imgName] != "A simple blue square" {
		t.Errorf("expected cached description, got: %s", captions2[imgName])
	}
}

func TestResolveCollisionCandidateReuse(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "kb_collision_test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	wikiDir := filepath.Join(tempDir, "wiki")
	_ = os.MkdirAll(wikiDir, 0755)

	cfg := &config.Config{
		VaultKBPath: tempDir,
	}

	c := New(cfg, &MockProvider{}, slog.Default())

	// Create a collision. Write "wiki/collision-test.md" compiled from "first.md"
	firstWikiPath := filepath.Join(wikiDir, "collision-test.md")
	firstContent := "---\ntitle: Collision Test\ncompiled_from: first.md\n---\nBody 1"
	_ = os.WriteFile(firstWikiPath, []byte(firstContent), 0644)

	// Write "wiki/collision-test-2.md" compiled from "second.md"
	secondWikiPath := filepath.Join(wikiDir, "collision-test-2.md")
	secondContent := "---\ntitle: Collision Test 2\ncompiled_from: second.md\n---\nBody 2"
	_ = os.WriteFile(secondWikiPath, []byte(secondContent), 0644)

	// 1. Check resolveCollision with "first.md". It should return "collision-test" since it matches firstWiki's CompiledFrom.
	slug1 := c.resolveCollision("collision-test", "sources/raw/first.md")
	if slug1 != "collision-test" {
		t.Errorf("expected 'collision-test', got %q", slug1)
	}

	// 2. Check resolveCollision with "second.md". It should return "collision-test-2" since it matches secondWiki's CompiledFrom.
	slug2 := c.resolveCollision("collision-test", "sources/raw/second.md")
	if slug2 != "collision-test-2" {
		t.Errorf("expected 'collision-test-2', got %q", slug2)
	}

	// 3. Check resolveCollision with "third.md". It should return "collision-test-3" since it doesn't match either,
	// and collision-test-3.md does not exist yet.
	slug3 := c.resolveCollision("collision-test", "sources/raw/third.md")
	if slug3 != "collision-test-3" {
		t.Errorf("expected 'collision-test-3', got %q", slug3)
	}
}

func TestCleanupOldCompilationProducts(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "kb_cleanup_test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	wikiDir := filepath.Join(tempDir, "wiki")
	_ = os.MkdirAll(wikiDir, 0755)

	cfg := &config.Config{
		VaultKBPath: tempDir,
	}

	c := New(cfg, &MockProvider{}, slog.Default())

	// Write three files compiled from "source.pdf"
	_ = os.WriteFile(filepath.Join(wikiDir, "spoke-1.md"), []byte("---\ntitle: Spoke 1\ncompiled_from: source.pdf\n---\nBody 1"), 0644)
	_ = os.WriteFile(filepath.Join(wikiDir, "spoke-2.md"), []byte("---\ntitle: Spoke 2\ncompiled_from: source.pdf\n---\nBody 2"), 0644)
	_ = os.WriteFile(filepath.Join(wikiDir, "hub.md"), []byte("---\ntitle: Hub\ncompiled_from: source.pdf\n---\nBody Hub"), 0644)

	// Write one file compiled from "another.pdf" (should NOT be cleaned up)
	_ = os.WriteFile(filepath.Join(wikiDir, "other.md"), []byte("---\ntitle: Other\ncompiled_from: another.pdf\n---\nBody Other"), 0644)

	// Write INDEX.md and populate it
	indexPath := filepath.Join(wikiDir, "INDEX.md")
	_ = os.WriteFile(indexPath, []byte("# INDEX\n\n- [[spoke-1]] — Spoke 1\n- [[spoke-2]] — Spoke 2\n- [[hub]] — Hub\n- [[other]] — Other\n"), 0644)

	// Run cleanup, keeping only "spoke-1" and "hub"
	err = c.cleanupOldCompilationProducts("sources/raw/source.pdf", []string{"spoke-1", "hub"})
	if err != nil {
		t.Fatalf("cleanupOldCompilationProducts failed: %v", err)
	}

	// spoke-2 should be trashed. spoke-1, hub, and other should remain.
	if _, err := os.Stat(filepath.Join(wikiDir, "spoke-2.md")); !os.IsNotExist(err) {
		t.Errorf("expected spoke-2.md to be cleaned up/trashed, but it still exists")
	}
	if _, err := os.Stat(filepath.Join(wikiDir, "spoke-1.md")); err != nil {
		t.Errorf("expected spoke-1.md to remain, got error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(wikiDir, "hub.md")); err != nil {
		t.Errorf("expected hub.md to remain, got error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(wikiDir, "other.md")); err != nil {
		t.Errorf("expected other.md to remain, got error: %v", err)
	}

	// INDEX.md should be updated, so spoke-2 should not be there anymore
	indexEntries, err := index.Read(indexPath)
	if err != nil {
		t.Fatalf("failed to read index: %v", err)
	}

	for _, entry := range indexEntries {
		if strings.EqualFold(entry.Slug, "spoke-2") {
			t.Errorf("expected spoke-2 to be removed from INDEX.md, but it is still present")
		}
	}
}
