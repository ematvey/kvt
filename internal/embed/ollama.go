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

type ollama struct {
	baseURL    string
	model      string
	dimensions int
	client     *http.Client
}

func NewOllama(baseURL string, model string, dimensions int) Embedder {
	return &ollama{
		baseURL:    strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		model:      strings.TrimSpace(model),
		dimensions: dimensions,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

func (o *ollama) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	if o.baseURL == "" {
		return nil, &ConfigError{Message: "ollama base URL is required"}
	}
	if o.model == "" {
		return nil, &ConfigError{Message: "ollama model is required"}
	}

	body, err := json.Marshal(map[string]any{
		"model": o.model,
		"input": texts,
	})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.baseURL+"/api/embed", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := o.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("ollama embeddings: status %s", resp.Status)
	}

	var payload struct {
		Embeddings [][]float32 `json:"embeddings"`
		Embedding  []float32   `json:"embedding"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}
	vectors := payload.Embeddings
	if len(vectors) == 0 && len(payload.Embedding) > 0 {
		vectors = [][]float32{payload.Embedding}
	}
	if err := validateDimensions(vectors, o.dimensions); err != nil {
		return nil, err
	}
	return vectors, nil
}
