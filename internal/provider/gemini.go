package provider

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

type GeminiProvider struct {
	apiKey string
}

func NewGemini(apiKey string) *GeminiProvider {
	return &GeminiProvider{apiKey: apiKey}
}

func (g *GeminiProvider) Name() string {
	return "Gemini"
}

func (g *GeminiProvider) Available(ctx context.Context) (bool, error) {
	if g.apiKey == "" {
		return false, nil
	}
	url := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models?key=%s", g.apiKey)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return false, err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()

	return resp.StatusCode == http.StatusOK, nil
}

type geminiMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

type geminiRequest struct {
	Model           string          `json:"model"`
	Messages        []geminiMessage `json:"messages"`
	ReasoningEffort string          `json:"reasoning_effort,omitempty"`
}

type geminiResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

func (g *GeminiProvider) Generate(ctx context.Context, model string, prompt string) (string, error) {
	if g.apiKey == "" {
		return "", errors.New("Gemini API key not configured")
	}

	url := "https://generativelanguage.googleapis.com/v1beta/openai/chat/completions"
	reqBody := geminiRequest{
		Model: model,
		Messages: []geminiMessage{
			{Role: "user", Content: prompt},
		},
	}

	// Enable maximum thinking/reasoning effort for Gemini 2.5/3.x models
	if strings.Contains(model, "gemini-3") || strings.Contains(model, "gemini-2.5") {
		reqBody.ReasoningEffort = "high"
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
	req.Header.Set("Authorization", "Bearer "+g.apiKey)

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
		return "", fmt.Errorf("gemini returned status code %d: %v", resp.StatusCode, errData)
	}

	var res geminiResponse
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return "", err
	}

	if len(res.Choices) == 0 {
		return "", errors.New("gemini returned zero choices")
	}

	return res.Choices[0].Message.Content, nil
}

func (g *GeminiProvider) GenerateMultimodal(ctx context.Context, model string, prompt string, images [][]byte, mimeTypes []string) (string, error) {
	if g.apiKey == "" {
		return "", errors.New("Gemini API key not configured")
	}

	url := "https://generativelanguage.googleapis.com/v1beta/openai/chat/completions"

	type textPart struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}

	type imagePart struct {
		URL string `json:"url"`
	}

	type imageURLPart struct {
		Type     string    `json:"type"`
		ImageURL imagePart `json:"image_url"`
	}

	var contentParts []any
	contentParts = append(contentParts, textPart{
		Type: "text",
		Text: prompt,
	})

	for i, imgData := range images {
		mime := "image/png"
		if i < len(mimeTypes) && mimeTypes[i] != "" {
			mime = mimeTypes[i]
		}
		b64Data := base64.StdEncoding.EncodeToString(imgData)
		dataURL := fmt.Sprintf("data:%s;base64,%s", mime, b64Data)
		contentParts = append(contentParts, imageURLPart{
			Type: "image_url",
			ImageURL: imagePart{
				URL: dataURL,
			},
		})
	}

	reqBody := geminiRequest{
		Model: model,
		Messages: []geminiMessage{
			{
				Role:    "user",
				Content: contentParts,
			},
		},
	}

	if strings.Contains(model, "gemini-2.0-flash") || strings.Contains(model, "gemini-2.5-flash") {
		reqBody.ReasoningEffort = "high"
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
	req.Header.Set("Authorization", "Bearer "+g.apiKey)

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
		return "", fmt.Errorf("gemini returned status code %d for multimodal request: %v", resp.StatusCode, errData)
	}

	var res geminiResponse
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return "", err
	}

	if len(res.Choices) == 0 {
		return "", errors.New("gemini returned zero choices for multimodal request")
	}

	return res.Choices[0].Message.Content, nil
}
