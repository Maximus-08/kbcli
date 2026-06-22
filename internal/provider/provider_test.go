package provider

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
)

type MockProvider struct {
	name      string
	available bool
	generate  func(model, prompt string) (string, error)
}

func (m *MockProvider) Name() string {
	return m.name
}

func (m *MockProvider) Available(ctx context.Context) (bool, error) {
	return m.available, nil
}

func (m *MockProvider) Generate(ctx context.Context, model string, prompt string) (string, error) {
	if m.generate != nil {
		return m.generate(model, prompt)
	}
	return "mock response", nil
}

func TestSmartRouterRouting(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	mockGemini := &MockProvider{name: "Gemini", available: true}
	mockOpenRouter := &MockProvider{name: "OpenRouter", available: true}
	mockGroq := &MockProvider{name: "Groq", available: true}
	mockLocal := &MockProvider{name: "Ollama-Local", available: true}

	router := &SmartRouter{
		gemini:     mockGemini,
		openRouter: mockOpenRouter,
		groq:       mockGroq,
		local:      mockLocal,
		logger:     logger,
	}

	// 1. Test routing to Gemini based on "gemini-" prefix
	calledGemini := false
	mockGemini.generate = func(model, prompt string) (string, error) {
		if model == "gemini-3-flash" {
			calledGemini = true
		}
		return "gemini response", nil
	}

	ctx := context.Background()
	resp, err := router.Generate(ctx, "gemini-3-flash", "hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp != "gemini response" || !calledGemini {
		t.Errorf("routing to Gemini failed, got: %s, called: %v", resp, calledGemini)
	}

	// 2. Test routing to OpenRouter based on "/" presence
	calledOpenRouter := false
	mockOpenRouter.generate = func(model, prompt string) (string, error) {
		if model == "google/gemma-4-31b-it:free" {
			calledOpenRouter = true
		}
		return "openrouter response", nil
	}

	resp, err = router.Generate(ctx, "google/gemma-4-31b-it:free", "hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp != "openrouter response" || !calledOpenRouter {
		t.Errorf("routing to OpenRouter failed, got: %s, called: %v", resp, calledOpenRouter)
	}

	// 3. Test routing to Groq based on "llama-" prefix
	calledGroq := false
	mockGroq.generate = func(model, prompt string) (string, error) {
		if model == "llama-4-scout" {
			calledGroq = true
		}
		return "groq response", nil
	}

	resp, err = router.Generate(ctx, "llama-4-scout", "hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp != "groq response" || !calledGroq {
		t.Errorf("routing to Groq failed, got: %s, called: %v", resp, calledGroq)
	}
}

func TestSmartRouterFallback(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	mockGemini := &MockProvider{name: "Gemini", available: true}
	mockLocal := &MockProvider{name: "Ollama-Local", available: true}

	router := &SmartRouter{
		gemini: mockGemini,
		local:  mockLocal,
		logger: logger,
	}

	// Make Gemini generate call fail to trigger fallback
	mockGemini.generate = func(model, prompt string) (string, error) {
		return "", errors.New("gemini quota exceeded")
	}

	// Verifying fallback to local Ollama with model substitution
	calledLocal := false
	mockLocal.generate = func(model, prompt string) (string, error) {
		if model == "gemma4:e4b" {
			calledLocal = true
		}
		return "fallback local response", nil
	}

	ctx := context.Background()
	resp, err := router.Generate(ctx, "gemini-3-flash", "hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp != "fallback local response" || !calledLocal {
		t.Errorf("fallback to local Ollama failed or model was not substituted, got: %s, substituted: %v", resp, calledLocal)
	}
}

type MockMultimodalProvider struct {
	MockProvider
	generateMultimodal func(model, prompt string, images [][]byte, mimeTypes []string) (string, error)
}

func (m *MockMultimodalProvider) GenerateMultimodal(ctx context.Context, model string, prompt string, images [][]byte, mimeTypes []string) (string, error) {
	if m.generateMultimodal != nil {
		return m.generateMultimodal(model, prompt, images, mimeTypes)
	}
	return "mock multimodal response", nil
}

func TestSmartRouterMultimodalRouting(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	mockGemini := &MockMultimodalProvider{
		MockProvider: MockProvider{name: "Gemini", available: true},
	}

	router := &SmartRouter{
		gemini: mockGemini,
		logger: logger,
	}

	calledMultimodal := false
	mockGemini.generateMultimodal = func(model, prompt string, images [][]byte, mimeTypes []string) (string, error) {
		if model == "gemini-2.5-flash" && len(images) == 1 && mimeTypes[0] == "image/png" {
			calledMultimodal = true
		}
		return "gemini multimodal response", nil
	}

	ctx := context.Background()
	resp, err := router.GenerateMultimodal(ctx, "gemini-2.5-flash", "describe this", [][]byte{[]byte("fake-image")}, []string{"image/png"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp != "gemini multimodal response" || !calledMultimodal {
		t.Errorf("routing GenerateMultimodal failed, got: %s, called: %v", resp, calledMultimodal)
	}
}
