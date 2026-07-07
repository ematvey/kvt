package embed

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type openAICompatible struct {
	baseURL    string
	model      string
	apiKey     string
	dimensions int
	client     *http.Client
}

func NewOpenAICompatible(baseURL string, model string, apiKey string, dimensions int) Embedder {
	return &openAICompatible{
		baseURL:    strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		model:      strings.TrimSpace(model),
		apiKey:     strings.TrimSpace(apiKey),
		dimensions: dimensions,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

func (c *openAICompatible) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	if c.baseURL == "" {
		return nil, &ConfigError{Message: "openai-compatible embedder base URL is required"}
	}
	if c.model == "" {
		return nil, &ConfigError{Message: "openai-compatible embedder model is required"}
	}

	body, err := json.Marshal(map[string]any{
		"model": c.model,
		"input": texts,
	})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("openai-compatible embeddings: status %s", resp.Status)
	}

	var payload struct {
		Data []struct {
			Index     int       `json:"index"`
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}
	vectors := make([][]float32, len(payload.Data))
	for _, item := range payload.Data {
		if item.Index < 0 || item.Index >= len(vectors) {
			return nil, fmt.Errorf("embedding index %d out of range", item.Index)
		}
		vectors[item.Index] = item.Embedding
	}
	if err := validateDimensions(vectors, c.dimensions); err != nil {
		return nil, err
	}
	return vectors, nil
}
