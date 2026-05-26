package provider

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
)

type Provider interface {
	// Generate sends a prompt and returns the response text.
	Generate(ctx context.Context, model string, prompt string) (string, error)
	// Name returns the provider name for logging.
	Name() string
	// Available checks if the provider is reachable.
	Available(ctx context.Context) (bool, error)
}

type SmartRouter struct {
	gemini     Provider
	openRouter Provider
	groq       Provider
	ollamaCl   Provider
	local      Provider
	logger     *slog.Logger
}

type tryProvider struct {
	provider Provider
	model    string
}

func NewSmartRouter(
	geminiAPIKey string,
	openRouterAPIKey string,
	groqAPIKey string,
	ollamaCloudBaseURL string,
	ollamaCloudAPIKey string,
	ollamaBaseURL string,
	logger *slog.Logger,
) *SmartRouter {
	return &SmartRouter{
		gemini:     NewGemini(geminiAPIKey),
		openRouter: NewOpenRouter(openRouterAPIKey),
		groq:       NewGroq(groqAPIKey),
		ollamaCl:   NewOllama(ollamaCloudBaseURL, ollamaCloudAPIKey, "Ollama-Cloud"),
		local:      NewOllama(ollamaBaseURL, "", "Ollama-Local"),
		logger:     logger,
	}
}

func (s *SmartRouter) Name() string {
	return "SmartRouter"
}

func (s *SmartRouter) Available(ctx context.Context) (bool, error) {
	if avail, err := s.local.Available(ctx); err == nil && avail {
		return true, nil
	}
	if isConfigured(s.gemini) {
		if avail, err := s.gemini.Available(ctx); err == nil && avail {
			return true, nil
		}
	}
	if isConfigured(s.openRouter) {
		if avail, err := s.openRouter.Available(ctx); err == nil && avail {
			return true, nil
		}
	}
	if isConfigured(s.groq) {
		if avail, err := s.groq.Available(ctx); err == nil && avail {
			return true, nil
		}
	}
	return false, nil
}

func (s *SmartRouter) Generate(ctx context.Context, model string, prompt string) (string, error) {
	// 1. Resolve primary provider based on model name patterns
	var primary Provider
	if strings.Contains(model, "/") {
		primary = s.openRouter
	} else if strings.HasPrefix(model, "gemini-") || strings.HasPrefix(model, "gemma-") {
		primary = s.gemini
	} else if strings.HasPrefix(model, "llama-") || strings.HasPrefix(model, "mixtral-") || model == "llama-4-scout" {
		primary = s.groq
	} else {
		primary = s.local
	}

	// 2. Build the list of attempts in fallback order
	var attempts []tryProvider

	// Add the primary attempt first if it is configured
	if isConfigured(primary) {
		attempts = append(attempts, tryProvider{provider: primary, model: model})
	}

	// If the primary provider is Gemini, add other Gemini models as immediate backups under the same API key!
	if primary.Name() == "Gemini" && isConfigured(s.gemini) {
		geminiModels := []string{
			"gemma-4-31b-it",
			"gemini-3.5-flash",
			"gemini-3-flash",
			"gemma-4-26b-a4b-it",
			"gemini-2.0-flash",
			"gemini-1.5-pro",
			"gemini-1.5-flash",
		}
		for _, m := range geminiModels {
			if m != model {
				attempts = append(attempts, tryProvider{provider: s.gemini, model: m})
			}
		}
	}

	// Append other configured cloud backups in fallback priority order
	if isConfigured(s.gemini) && primary.Name() != "Gemini" {
		attempts = append(attempts, tryProvider{provider: s.gemini, model: "gemma-4-31b-it"})
		attempts = append(attempts, tryProvider{provider: s.gemini, model: "gemini-3.5-flash"})
	}
	if isConfigured(s.openRouter) && primary.Name() != "OpenRouter" {
		attempts = append(attempts, tryProvider{provider: s.openRouter, model: "google/gemma-4-31b-it:free"})
	}
	if isConfigured(s.groq) && primary.Name() != "Groq" {
		attempts = append(attempts, tryProvider{provider: s.groq, model: "llama-3.1-8b-instant"})
	}
	if isConfigured(s.ollamaCl) && primary.Name() != "Ollama-Cloud" {
		attempts = append(attempts, tryProvider{provider: s.ollamaCl, model: "llama-4-scout"})
	}
	if primary.Name() != "Ollama-Local" {
		attempts = append(attempts, tryProvider{provider: s.local, model: "gemma4:e4b"})
	}

	// If no primary was configured and attempts is empty, start directly with local Ollama
	if len(attempts) == 0 {
		attempts = append(attempts, tryProvider{provider: s.local, model: "gemma4:e4b"})
	}

	// 3. Try each provider sequentially
	var lastErr error
	for i, attempt := range attempts {
		s.logger.Info("Routing request to provider",
			"attempt", i+1,
			"totalAttempts", len(attempts),
			"provider", attempt.provider.Name(),
			"model", attempt.model,
		)

		resp, err := attempt.provider.Generate(ctx, attempt.model, prompt)
		if err == nil {
			return resp, nil
		}

		lastErr = err
		s.logger.Error("Provider attempt failed",
			"provider", attempt.provider.Name(),
			"model", attempt.model,
			"error", err.Error(),
		)
	}

	return "", fmt.Errorf("all providers in the fallback chain failed. Last error: %w", lastErr)
}

func isConfigured(p Provider) bool {
	if p == nil {
		return false
	}
	switch prov := p.(type) {
	case *GeminiProvider:
		return prov.apiKey != ""
	case *OpenRouterProvider:
		return prov.apiKey != ""
	case *GroqProvider:
		return prov.apiKey != ""
	case *OllamaProvider:
		return prov.baseURL != ""
	default:
		return true
	}
}

func NewChain(
	geminiAPIKey string,
	openRouterAPIKey string,
	groqAPIKey string,
	ollamaCloudBaseURL string,
	ollamaCloudAPIKey string,
	ollamaBaseURL string,
	logger *slog.Logger,
) Provider {
	return NewSmartRouter(
		geminiAPIKey,
		openRouterAPIKey,
		groqAPIKey,
		ollamaCloudBaseURL,
		ollamaCloudAPIKey,
		ollamaBaseURL,
		logger,
	)
}
