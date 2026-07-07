package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ematvey/kvt/internal/config"
	"github.com/ematvey/kvt/internal/rerank"
	"github.com/ematvey/kvt/internal/testutil"
)

func TestWriteSearchesWithFTSFallbackWhenEmbeddingsFail(t *testing.T) {
	testutil.RequireGit(t)
	h := newServiceHarnessWithDeps(t, Deps{
		Embedder: failingEmbedder{err: errors.New("embedder down")},
		Reranker: failingServiceReranker{err: errors.New("reranker down")},
	})

	if _, err := h.service.Write(t.Context(), WriteRequest{
		Path:    "systems/db.md",
		Content: "---\ntype: System\ntitle: DB\ndescription: Primary database\n---\nThe primary database serves production traffic.\n",
		Agent:   "test-agent",
	}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	got, err := h.service.Search(t.Context(), SearchRequest{Query: "production database", Limit: 5})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(got.Hits) != 1 || got.Hits[0].Path != "systems/db.md" {
		t.Fatalf("hits = %#v", got.Hits)
	}
	if !containsReason(got.Degraded, "vector") {
		t.Fatalf("degraded = %#v", got.Degraded)
	}
	if !containsReason(got.Degraded, "rerank") {
		t.Fatalf("degraded = %#v", got.Degraded)
	}
}

func containsReason(items []string, want string) bool {
	for _, item := range items {
		if strings.Contains(strings.ToLower(item), want) {
			return true
		}
	}
	return false
}

type failingEmbedder struct {
	err error
}

func (f failingEmbedder) Embed(context.Context, []string) ([][]float32, error) {
	return nil, f.err
}

type failingServiceReranker struct {
	err error
}

func (f failingServiceReranker) Rerank(context.Context, string, []rerank.Candidate) ([]rerank.Score, error) {
	return nil, f.err
}

func newServiceHarnessWithDeps(t *testing.T, deps Deps) serviceHarness {
	t.Helper()
	root := t.TempDir()
	if _, err := Init(t.Context(), InitRequest{VaultPath: root, Defaults: true}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	cfg, err := config.Load(root, "")
	if err != nil {
		t.Fatalf("Load config: %v", err)
	}
	nowValues := []time.Time{
		time.Date(2026, 7, 7, 12, 0, 0, 0, time.UTC),
		time.Date(2026, 7, 7, 12, 0, 1, 0, time.UTC),
		time.Date(2026, 7, 7, 12, 0, 2, 0, time.UTC),
		time.Date(2026, 7, 7, 12, 0, 3, 0, time.UTC),
	}
	index := 0
	deps.Now = func() time.Time {
		if index >= len(nowValues) {
			return nowValues[len(nowValues)-1]
		}
		now := nowValues[index]
		index++
		return now
	}
	svc, err := New(root, cfg, deps)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return serviceHarness{root: root, service: svc}
}
