package service

import (
	"context"

	"github.com/ematvey/kvt/internal/index"
)

func (s *Service) List(ctx context.Context, req index.ListRequest) (index.ListResponse, error) {
	return s.index.List(ctx, req)
}

func (s *Service) Grep(ctx context.Context, req index.GrepRequest) (index.GrepResponse, error) {
	return s.index.Grep(ctx, req)
}

func (s *Service) Summary(ctx context.Context, req index.SummaryRequest) (index.SummaryResponse, error) {
	return s.index.Summary(ctx, req)
}

func (s *Service) Reconcile(ctx context.Context) (index.ReconcileResult, error) {
	if err := ctx.Err(); err != nil {
		return index.ReconcileResult{}, err
	}
	s.writerMu.Lock()
	defer s.writerMu.Unlock()
	return s.index.Reconcile(ctx, s.root)
}
