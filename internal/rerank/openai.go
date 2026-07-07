package rerank

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
	baseURL string
	model   string
	apiKey  string
	client  *http.Client
}

func NewOpenAICompatible(baseURL string, model string, apiKey string) Reranker {
	return &openAICompatible{
		baseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		model:   strings.TrimSpace(model),
		apiKey:  strings.TrimSpace(apiKey),
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

func (c *openAICompatible) Rerank(ctx context.Context, query string, candidates []Candidate) ([]Score, error) {
	if strings.TrimSpace(query) == "" {
		return nil, fmt.Errorf("query is required")
	}
	if len(candidates) == 0 {
		return nil, nil
	}
	if c.baseURL == "" {
		return nil, fmt.Errorf("openai-compatible reranker base URL is required")
	}
	if c.model == "" {
		return nil, fmt.Errorf("openai-compatible reranker model is required")
	}

	payload, err := json.Marshal(map[string]any{
		"model": c.model,
		"messages": []map[string]any{
			{
				"role":    "system",
				"content": "Return only JSON. Rank the candidates by relevance to the query as an array of objects with keys index and score.",
			},
			{
				"role":    "user",
				"content": rerankPrompt(query, candidates),
			},
		},
		"temperature": 0,
	})
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/chat/completions", bytes.NewReader(payload))
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
		return nil, fmt.Errorf("openai-compatible rerank: status %s", resp.Status)
	}

	var body struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}
	if len(body.Choices) == 0 {
		return nil, fmt.Errorf("openai-compatible rerank: no choices returned")
	}

	var scores []Score
	if err := json.Unmarshal([]byte(body.Choices[0].Message.Content), &scores); err != nil {
		return nil, fmt.Errorf("parse rerank response: %w", err)
	}
	return scores, nil
}

func rerankPrompt(query string, candidates []Candidate) string {
	items := make([]map[string]any, 0, len(candidates))
	for i, candidate := range candidates {
		items = append(items, map[string]any{
			"index":    i,
			"doc_path": candidate.DocPath,
			"title":    candidate.Title,
			"text":     candidate.Text,
		})
	}
	payload, _ := json.Marshal(map[string]any{
		"query":      query,
		"candidates": items,
	})
	return string(payload)
}
