package search

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ematvey/kvt/internal/embed"
	"github.com/ematvey/kvt/internal/rerank"
)

func TestRRFWeightsRanksWithoutScoreNormalization(t *testing.T) {
	got := FuseRRF([]RankedList{
		{Weight: 0.5, Hits: []Hit{{DocPath: "a"}, {DocPath: "b"}}},
		{Weight: 0.5, Hits: []Hit{{DocPath: "b"}, {DocPath: "c"}}},
	}, 60)
	if len(got) < 3 {
		t.Fatalf("len(got) = %d", len(got))
	}
	if got[0].DocPath != "b" {
		t.Fatalf("top = %#v", got)
	}
}

func TestSearchFallsBackToFTSWhenVectorAndRerankDegrade(t *testing.T) {
	resp, err := Search(t.Context(), SearchRequest{
		Query:     "primary database",
		Limit:     5,
		FTSWeight: 0.7,
		VecWeight: 0.3,
		Keyword: stubKeywordSearcher{hits: []Hit{
			{DocPath: "systems/db.md", Title: "DB", Snippet: "primary database", Text: "primary database"},
		}},
		Vector: stubVectorSearcher{
			err: errors.New("vector search unavailable"),
		},
		Embedder: stubEmbedder{
			err: errors.New("embedder down"),
		},
		Reranker: stubReranker{
			err: errors.New("reranker down"),
		},
		UseRerank:  true,
		RerankTopK: 5,
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(resp.Hits) != 1 || resp.Hits[0].DocPath != "systems/db.md" {
		t.Fatalf("hits = %#v", resp.Hits)
	}
	if !containsDegraded(resp.Degraded, "vector") {
		t.Fatalf("degraded = %#v", resp.Degraded)
	}
	if !containsDegraded(resp.Degraded, "rerank") {
		t.Fatalf("degraded = %#v", resp.Degraded)
	}
}

func containsDegraded(items []string, want string) bool {
	for _, item := range items {
		if strings.Contains(strings.ToLower(item), want) {
			return true
		}
	}
	return false
}

type stubKeywordSearcher struct {
	hits []Hit
	err  error
}

func (s stubKeywordSearcher) SearchKeywords(_ context.Context, _ KeywordRequest) ([]Hit, error) {
	return s.hits, s.err
}

type stubVectorSearcher struct {
	hits []Hit
	err  error
}

func (s stubVectorSearcher) SearchVector(_ context.Context, _ VectorRequest) ([]Hit, error) {
	return s.hits, s.err
}

type stubEmbedder struct {
	vectors [][]float32
	err     error
}

func (s stubEmbedder) Embed(_ context.Context, _ []string) ([][]float32, error) {
	if s.err != nil {
		return nil, s.err
	}
	if len(s.vectors) == 0 {
		return [][]float32{{1, 0}}, nil
	}
	return s.vectors, nil
}

var _ embed.Embedder = stubEmbedder{}

type stubReranker struct {
	scores []rerank.Score
	err    error
}

func (s stubReranker) Rerank(_ context.Context, _ string, _ []rerank.Candidate) ([]rerank.Score, error) {
	return s.scores, s.err
}
