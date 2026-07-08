package service

import (
	"context"

	"github.com/ematvey/kvt/internal/access"
	"github.com/ematvey/kvt/internal/index"
	searchpkg "github.com/ematvey/kvt/internal/search"
)

func (s *Service) List(ctx context.Context, req ListRequest) (index.ListResponse, error) {
	resp, err := s.index.List(ctx, index.ListRequest{
		Type:       req.Type,
		PathPrefix: req.PathPrefix,
		FieldKey:   req.FieldKey,
		FieldValue: req.FieldValue,
		Limit:      req.Limit,
		Cursor:     req.Cursor,
	})
	if err != nil {
		return index.ListResponse{}, err
	}
	if req.Access == nil {
		return resp, nil
	}
	filtered := resp.Documents[:0]
	for _, doc := range resp.Documents {
		if access.CanRead(req.Access, doc.Path) {
			filtered = append(filtered, doc)
		}
	}
	resp.Documents = filtered
	return resp, nil
}

func (s *Service) Grep(ctx context.Context, req GrepRequest) (index.GrepResponse, error) {
	resp, err := s.index.Grep(ctx, index.GrepRequest{
		Query:      req.Query,
		PathPrefix: req.PathPrefix,
		Limit:      req.Limit,
		Cursor:     req.Cursor,
	})
	if err != nil {
		return index.GrepResponse{}, err
	}
	if req.Access == nil {
		return resp, nil
	}
	filtered := resp.Matches[:0]
	for _, match := range resp.Matches {
		if access.CanRead(req.Access, match.Path) {
			filtered = append(filtered, match)
		}
	}
	resp.Matches = filtered
	return resp, nil
}

func (s *Service) Summary(ctx context.Context, req index.SummaryRequest) (index.SummaryResponse, error) {
	return s.index.Summary(ctx, req)
}

func (s *Service) Reconcile(ctx context.Context) (index.ReconcileResult, error) {
	return s.reconcile(ctx, false)
}

func (s *Service) Rebuild(ctx context.Context) (index.ReconcileResult, error) {
	return s.reconcile(ctx, true)
}

func (s *Service) reconcile(ctx context.Context, force bool) (index.ReconcileResult, error) {
	if err := ctx.Err(); err != nil {
		return index.ReconcileResult{}, err
	}
	s.writerMu.Lock()
	defer s.writerMu.Unlock()
	var result index.ReconcileResult
	var err error
	if force {
		result, err = s.index.Rebuild(ctx, s.root)
	} else {
		result, err = s.index.Reconcile(ctx, s.root)
	}
	if err != nil {
		return index.ReconcileResult{}, err
	}
	documents := make([]index.EmbeddingJobDocument, 0, len(result.AppliedDocuments))
	for _, doc := range result.AppliedDocuments {
		documents = append(documents, index.EmbeddingJobDocument{
			Path:      doc.Path,
			Timestamp: doc.Timestamp,
			Hash:      doc.Hash,
			Chunks:    append([]index.Chunk(nil), doc.Chunks...),
		})
	}
	if err := s.enqueueEmbeddingDocuments(ctx, documents); err != nil {
		return index.ReconcileResult{}, err
	}
	return result, nil
}

func (s *Service) Search(ctx context.Context, req SearchRequest) (SearchResponse, error) {
	result, err := searchpkg.Search(ctx, searchpkg.SearchRequest{
		Query:      req.Query,
		PathPrefix: req.PathPrefix,
		Limit:      req.Limit,
		FTSWeight:  s.cfg.Search.FTSWeight,
		VecWeight:  s.cfg.Search.VecWeight,
		Keyword:    keywordSearcher{db: s.index},
		Vector:     vectorSearcher{db: s.index},
		Embedder:   s.embedder,
		Reranker:   s.reranker,
		UseRerank:  s.cfg.Search.Rerank,
		RerankTopK: s.cfg.Search.RerankTopK,
	})
	if err != nil {
		return SearchResponse{}, err
	}

	resp := SearchResponse{
		Hits:     make([]SearchHit, 0, len(result.Hits)),
		Degraded: append([]string(nil), result.Degraded...),
	}
	for _, hit := range result.Hits {
		if !access.CanRead(req.Access, hit.DocPath) {
			continue
		}
		resp.Hits = append(resp.Hits, SearchHit{
			Path:    hit.DocPath,
			Title:   hit.Title,
			Type:    hit.Type,
			Snippet: hit.Snippet,
			Score:   hit.Score,
		})
	}
	return resp, nil
}

type keywordSearcher struct {
	db *index.DB
}

func (k keywordSearcher) SearchKeywords(ctx context.Context, req searchpkg.KeywordRequest) ([]searchpkg.Hit, error) {
	hits, err := k.db.SearchKeywords(ctx, index.SearchRequest{
		Query:      req.Query,
		PathPrefix: req.PathPrefix,
		Limit:      req.Limit,
	})
	if err != nil {
		return nil, err
	}
	out := make([]searchpkg.Hit, 0, len(hits))
	for _, hit := range hits {
		out = append(out, searchpkg.Hit{
			DocPath:      hit.Path,
			Title:        hit.Title,
			Type:         hit.Type,
			Snippet:      hit.Snippet,
			Text:         hit.Text,
			ChunkOrdinal: hit.Ordinal,
			Score:        hit.Score,
		})
	}
	return out, nil
}

type vectorSearcher struct {
	db *index.DB
}

func (v vectorSearcher) SearchVector(ctx context.Context, req searchpkg.VectorRequest) ([]searchpkg.Hit, error) {
	hits, err := v.db.SearchVector(ctx, index.VectorRequest{
		Embedding:  req.Embedding,
		PathPrefix: req.PathPrefix,
		Limit:      req.Limit,
	})
	if err != nil {
		return nil, err
	}
	out := make([]searchpkg.Hit, 0, len(hits))
	for _, hit := range hits {
		out = append(out, searchpkg.Hit{
			DocPath:      hit.Path,
			Title:        hit.Title,
			Type:         hit.Type,
			Snippet:      hit.Snippet,
			Text:         hit.Text,
			ChunkOrdinal: hit.Ordinal,
			Score:        hit.Score,
		})
	}
	return out, nil
}

func (v vectorSearcher) VectorAvailable() bool {
	return v.db.VectorAvailable()
}
