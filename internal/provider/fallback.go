package provider

import (
	"context"
	"fmt"
	"log/slog"
)

type FallbackProvider struct {
	primary  Provider
	fallback Provider
	logger   *slog.Logger
}

func NewFallback(primary Provider, fallback Provider, logger *slog.Logger) *FallbackProvider {
	return &FallbackProvider{
		primary:  primary,
		fallback: fallback,
		logger:   logger,
	}
}

func (f *FallbackProvider) Name() string {
	return "Fallback(" + f.primary.Name() + " -> " + f.fallback.Name() + ")"
}

func (f *FallbackProvider) Available(ctx context.Context) (bool, error) {
	avail, err := f.primary.Available(ctx)
	if err == nil && avail {
		return true, nil
	}
	return f.fallback.Available(ctx)
}

func (f *FallbackProvider) Generate(ctx context.Context, model string, prompt string) (string, error) {
	resp, err := f.primary.Generate(ctx, model, prompt)
	if err != nil {
		f.logger.Warn("Primary provider failed, trying fallback provider...", "primary", f.primary.Name(), "error", err)
		fallbackResp, fallbackErr := f.fallback.Generate(ctx, model, prompt)
		if fallbackErr != nil {
			return "", fmt.Errorf("both primary and fallback providers failed. Primary error: %v. Fallback error: %v", err, fallbackErr)
		}
		return fallbackResp, nil
	}
	return resp, nil
}
