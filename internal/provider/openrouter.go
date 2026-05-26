package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
)

type OpenRouterProvider struct {
	apiKey string
}

func NewOpenRouter(apiKey string) *OpenRouterProvider {
	return &OpenRouterProvider{apiKey: apiKey}
}

func (o *OpenRouterProvider) Name() string {
	return "OpenRouter"
}

func (o *OpenRouterProvider) Available(ctx context.Context) (bool, error) {
	if o.apiKey == "" {
		return false, nil
	}
	req, err := http.NewRequestWithContext(ctx, "GET", "https://openrouter.ai/api/v1/models", nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("Authorization", "Bearer "+o.apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()

	return resp.StatusCode == http.StatusOK, nil
}

type openRouterMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openRouterRequest struct {
	Model    string              `json:"model"`
	Messages []openRouterMessage `json:"messages"`
}

type openRouterResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

func (o *OpenRouterProvider) Generate(ctx context.Context, model string, prompt string) (string, error) {
	if o.apiKey == "" {
		return "", errors.New("OpenRouter API key not configured")
	}

	url := "https://openrouter.ai/api/v1/chat/completions"
	reqBody := openRouterRequest{
		Model: model,
		Messages: []openRouterMessage{
			{Role: "user", Content: prompt},
		},
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return "", err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+o.apiKey)
	req.Header.Set("HTTP-Referer", "https://github.com/avnis/kb-system")
	req.Header.Set("X-Title", "KB System")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusTooManyRequests {
		return "", ErrRateLimited
	}

	if resp.StatusCode != http.StatusOK {
		var errData map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&errData)
		return "", fmt.Errorf("openrouter returned status code %d: %v", resp.StatusCode, errData)
	}

	var res openRouterResponse
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return "", err
	}

	if len(res.Choices) == 0 {
		return "", errors.New("openrouter returned zero choices")
	}

	return res.Choices[0].Message.Content, nil
}
