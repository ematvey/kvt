package search

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/ematvey/kvt/internal/embed"
	"github.com/ematvey/kvt/internal/rerank"
)

type KeywordRequest struct {
	Query      string
	PathPrefix string
	Limit      int
}

type VectorRequest struct {
	Embedding  []float32
	PathPrefix string
	Limit      int
}

type KeywordSearcher interface {
	SearchKeywords(ctx context.Context, req KeywordRequest) ([]Hit, error)
}

type VectorSearcher interface {
	SearchVector(ctx context.Context, req VectorRequest) ([]Hit, error)
}

type SearchRequest struct {
	Query      string
	PathPrefix string
	Limit      int
	FTSWeight  float64
	VecWeight  float64
	Keyword    KeywordSearcher
	Vector     VectorSearcher
	Embedder   embed.Embedder
	Reranker   rerank.Reranker
	UseRerank  bool
	RerankTopK int
}

type SearchResponse struct {
	Hits     []Hit
	Degraded []string
}

func Search(ctx context.Context, req SearchRequest) (SearchResponse, error) {
	if err := ctx.Err(); err != nil {
		return SearchResponse{}, err
	}
	if strings.TrimSpace(req.Query) == "" {
		return SearchResponse{}, fmt.Errorf("query is required")
	}
	if req.Keyword == nil {
		return SearchResponse{}, fmt.Errorf("keyword searcher is required")
	}

	limit := req.Limit
	if limit <= 0 {
		limit = 10
	}

	ftsHits, err := req.Keyword.SearchKeywords(ctx, KeywordRequest{
		Query:      req.Query,
		PathPrefix: req.PathPrefix,
		Limit:      limit,
	})
	if err != nil {
		return SearchResponse{}, err
	}

	resp := SearchResponse{}
	lists := []RankedList{{
		Weight: weightOrDefault(req.FTSWeight, 0.5),
		Hits:   ftsHits,
	}}

	if req.Vector == nil || req.Embedder == nil {
		resp.Degraded = append(resp.Degraded, "vector unavailable")
	} else {
		vectors, err := req.Embedder.Embed(ctx, []string{req.Query})
		if err != nil {
			resp.Degraded = append(resp.Degraded, "vector degraded: "+err.Error())
		} else if len(vectors) == 0 {
			resp.Degraded = append(resp.Degraded, "vector degraded: no query embedding returned")
		} else {
			vectorHits, err := req.Vector.SearchVector(ctx, VectorRequest{
				Embedding:  vectors[0],
				PathPrefix: req.PathPrefix,
				Limit:      limit,
			})
			if err != nil {
				resp.Degraded = append(resp.Degraded, "vector degraded: "+err.Error())
			} else if len(vectorHits) > 0 {
				lists = append(lists, RankedList{
					Weight: weightOrDefault(req.VecWeight, 0.5),
					Hits:   vectorHits,
				})
			}
		}
	}

	fused := FuseRRF(lists, 60)
	if len(fused) > limit {
		fused = fused[:limit]
	}

	if req.UseRerank {
		if req.Reranker == nil {
			resp.Degraded = append(resp.Degraded, "rerank unavailable")
		} else {
			topK := req.RerankTopK
			if topK <= 0 || topK > len(fused) {
				topK = len(fused)
			}
			candidates := make([]rerank.Candidate, 0, topK)
			for _, hit := range fused[:topK] {
				candidates = append(candidates, rerank.Candidate{
					DocPath: hit.DocPath,
					Title:   hit.Title,
					Text:    bestRerankText(hit),
				})
			}
			scores, err := req.Reranker.Rerank(ctx, req.Query, candidates)
			if err != nil {
				resp.Degraded = append(resp.Degraded, "rerank degraded: "+err.Error())
			} else {
				fused = applyRerank(fused, topK, scores)
			}
		}
	}

	resp.Hits = fused
	return resp, nil
}

func bestRerankText(hit Hit) string {
	for _, candidate := range []string{hit.Text, hit.Snippet, hit.Title, hit.DocPath} {
		if strings.TrimSpace(candidate) != "" {
			return candidate
		}
	}
	return hit.DocPath
}

func weightOrDefault(value float64, fallback float64) float64 {
	if value > 0 {
		return value
	}
	return fallback
}

func applyRerank(hits []Hit, topK int, scores []rerank.Score) []Hit {
	if len(scores) == 0 || topK == 0 {
		return hits
	}
	type scoredHit struct {
		hit   Hit
		score float64
	}
	replacements := make([]scoredHit, 0, len(scores))
	seen := map[int]struct{}{}
	for _, score := range scores {
		if score.Index < 0 || score.Index >= topK {
			continue
		}
		if _, ok := seen[score.Index]; ok {
			continue
		}
		seen[score.Index] = struct{}{}
		replacements = append(replacements, scoredHit{hit: hits[score.Index], score: score.Score})
	}
	if len(replacements) == 0 {
		return hits
	}
	sort.SliceStable(replacements, func(i int, j int) bool {
		if replacements[i].score == replacements[j].score {
			return replacements[i].hit.DocPath < replacements[j].hit.DocPath
		}
		return replacements[i].score > replacements[j].score
	})

	reordered := make([]Hit, 0, len(hits))
	used := map[string]struct{}{}
	for _, replacement := range replacements {
		hit := replacement.hit
		hit.Score = replacement.score
		reordered = append(reordered, hit)
		used[hit.DocPath] = struct{}{}
	}
	for _, hit := range hits[:topK] {
		if _, ok := used[hit.DocPath]; ok {
			continue
		}
		reordered = append(reordered, hit)
	}
	reordered = append(reordered, hits[topK:]...)

	out := make([]Hit, len(reordered))
	copy(out, reordered)
	if len(out) == 0 {
		return hits
	}
	return out
}
