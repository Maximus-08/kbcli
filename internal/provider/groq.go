package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
)

var ErrRateLimited = errors.New("provider rate limited (429)")

type GroqProvider struct {
	apiKey string
}

func NewGroq(apiKey string) *GroqProvider {
	return &GroqProvider{apiKey: apiKey}
}

func (g *GroqProvider) Name() string {
	return "Groq"
}

func (g *GroqProvider) Available(ctx context.Context) (bool, error) {
	if g.apiKey == "" {
		return false, nil
	}
	req, err := http.NewRequestWithContext(ctx, "GET", "https://api.groq.com/openai/v1/models", nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("Authorization", "Bearer "+g.apiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()

	return resp.StatusCode == http.StatusOK, nil
}

type groqMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type groqRequest struct {
	Model    string        `json:"model"`
	Messages []groqMessage `json:"messages"`
}

type groqResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

func (g *GroqProvider) Generate(ctx context.Context, model string, prompt string) (string, error) {
	if g.apiKey == "" {
		return "", errors.New("Groq API key not configured")
	}

	url := "https://api.groq.com/openai/v1/chat/completions"
	reqBody := groqRequest{
		Model: model,
		Messages: []groqMessage{
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
		return "", fmt.Errorf("groq returned status code %d: %v", resp.StatusCode, errData)
	}

	var res groqResponse
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		return "", err
	}

	if len(res.Choices) == 0 {
		return "", errors.New("groq returned zero choices")
	}

	return res.Choices[0].Message.Content, nil
}
