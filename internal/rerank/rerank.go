package rerank

import "context"

type Candidate struct {
	DocPath string
	Title   string
	Text    string
}

type Score struct {
	Index int     `json:"index"`
	Score float64 `json:"score"`
}

type Reranker interface {
	Rerank(ctx context.Context, query string, candidates []Candidate) ([]Score, error)
}
