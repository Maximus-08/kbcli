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
