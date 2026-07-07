package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ematvey/kvt/internal/config"
	"github.com/ematvey/kvt/internal/embed"
	"github.com/ematvey/kvt/internal/frontmatter"
	"github.com/ematvey/kvt/internal/gitops"
	"github.com/ematvey/kvt/internal/index"
	"github.com/ematvey/kvt/internal/ontology"
	"github.com/ematvey/kvt/internal/pathutil"
	"github.com/ematvey/kvt/internal/rerank"
)

type Deps struct {
	Now                   func() time.Time
	Embedder              embed.Embedder
	Reranker              rerank.Reranker
	EmbeddingMaxAttempts  int
	EmbeddingBackoffDelay func(attempt int) time.Duration
}

type Service struct {
	root                  string
	cfg                   config.Config
	git                   gitops.Client
	index                 *index.DB
	now                   func() time.Time
	embedder              embed.Embedder
	reranker              rerank.Reranker
	embeddingMaxAttempts  int
	embeddingBackoffDelay func(attempt int) time.Duration
	writerMu              sync.Mutex
	lastTimestamp         time.Time
	embedQueue            chan embeddingJob
}

type conceptState struct {
	path    pathutil.Path
	content []byte
	hash    string
}

type preparedDocument struct {
	document  frontmatter.Document
	content   []byte
	hash      string
	indexed   index.IndexedDocument
	timestamp string
	warnings  []ontology.Issue
}

type embeddingJob struct {
	path      string
	timestamp string
	hash      string
	chunks    []index.Chunk
}

func New(root string, cfg config.Config, deps Deps) (*Service, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, fmt.Errorf("vault root is required")
	}
	now := deps.Now
	if now == nil {
		now = time.Now
	}
	if err := os.MkdirAll(filepath.Join(root, ".kvt"), 0o755); err != nil {
		return nil, err
	}
	embedder := deps.Embedder
	if embedder == nil {
		embedder = buildEmbedder(cfg)
	}
	reranker := deps.Reranker
	if reranker == nil {
		reranker = buildReranker(cfg)
	}
	embeddingMaxAttempts := deps.EmbeddingMaxAttempts
	if embeddingMaxAttempts <= 0 {
		embeddingMaxAttempts = 3
	}
	embeddingBackoffDelay := deps.EmbeddingBackoffDelay
	if embeddingBackoffDelay == nil {
		embeddingBackoffDelay = func(attempt int) time.Duration {
			if attempt <= 0 {
				attempt = 1
			}
			return time.Duration(1<<uint(attempt-1)) * 250 * time.Millisecond
		}
	}
	indexDB, err := index.Open(filepath.Join(root, ".kvt", "index.db"), index.Options{
		EnableVector:    embedder != nil,
		VectorDimension: cfg.Embedder.Dimensions,
		VectorModel:     cfg.Embedder.Model,
		VectorType:      cfg.Embedder.Type,
		VectorBaseURL:   cfg.Embedder.BaseURL,
	})
	if err != nil {
		return nil, err
	}
	svc := &Service{
		root:                  root,
		cfg:                   cfg,
		git:                   gitops.New(root),
		index:                 indexDB,
		now:                   now,
		embedder:              embedder,
		reranker:              reranker,
		embeddingMaxAttempts:  embeddingMaxAttempts,
		embeddingBackoffDelay: embeddingBackoffDelay,
	}
	if embedder != nil && indexDB.VectorAvailable() {
		svc.embedQueue = make(chan embeddingJob, 64)
		go svc.runEmbeddingWorker()
		go func() {
			_ = svc.enqueuePendingEmbeddings(context.Background())
		}()
	}
	return svc, nil
}

func normalizeConceptPath(raw string) (pathutil.Path, error) {
	normalized, err := pathutil.Normalize(strings.TrimSpace(raw))
	if err != nil {
		return "", err
	}
	if filepath.Ext(normalized.String()) != ".md" {
		return "", fmt.Errorf("path %q must point to a markdown concept", normalized.String())
	}
	if path.Base(normalized.String()) == "index.md" {
		return "", fmt.Errorf("path %q is service-owned", normalized.String())
	}
	return normalized, nil
}

func (s *Service) fullPath(docPath pathutil.Path) string {
	return filepath.Join(s.root, filepath.FromSlash(docPath.String()))
}

func (s *Service) readState(docPath pathutil.Path) (conceptState, error) {
	content, err := os.ReadFile(s.fullPath(docPath))
	if err != nil {
		return conceptState{}, err
	}
	return conceptState{
		path:    docPath,
		content: content,
		hash:    frontmatter.Hash(content),
	}, nil
}

func (s *Service) commitMutation(message string, agent string, paths []string) (CommitInfo, error) {
	body := ""
	if strings.TrimSpace(agent) != "" {
		body = "Agent: " + strings.TrimSpace(agent)
	}
	result, err := s.git.Commit(gitops.CommitOptions{
		Message:     message,
		Body:        body,
		Paths:       appendUniquePaths(nil, paths...),
		AuthorName:  s.cfg.Git.AuthorName,
		AuthorEmail: s.cfg.Git.AuthorEmail,
	})
	if err != nil {
		return CommitInfo{}, err
	}
	if !result.Changed {
		return CommitInfo{}, fmt.Errorf("mutation produced no git commit")
	}
	return CommitInfo{
		Hash:      result.Hash,
		ShortHash: result.ShortHash,
	}, nil
}

func checkBaseHash(docPath pathutil.Path, baseHash string, current conceptState, err error) error {
	if strings.TrimSpace(baseHash) == "" {
		return nil
	}
	baseHash = strings.TrimSpace(baseHash)
	if err != nil {
		if os.IsNotExist(err) {
			return &ConflictError{
				Path:           docPath.String(),
				BaseHash:       baseHash,
				CurrentHash:    "",
				CurrentContent: "",
			}
		}
		return err
	}
	if baseHash == current.hash {
		return nil
	}
	return &ConflictError{
		Path:           docPath.String(),
		BaseHash:       baseHash,
		CurrentHash:    current.hash,
		CurrentContent: string(current.content),
	}
}

func (s *Service) prepareDocument(docPath pathutil.Path, rawContent string, validationMode ValidationMode, timestampFloor time.Time) (preparedDocument, error) {
	doc, err := frontmatter.Parse([]byte(rawContent))
	if err != nil {
		return preparedDocument{}, err
	}
	doc = frontmatter.WithTimestamp(doc, s.nextTimestampAfter(timestampFloor))

	schema, err := ontology.Load(s.root)
	if err != nil {
		return preparedDocument{}, err
	}
	mode := validationMode.ontologyMode()
	validation := ontology.ValidateDocument(schema, docPath, doc, mode)
	refValidation, err := s.validateDocumentRefs(schema, docPath, doc, mode)
	if err != nil {
		return preparedDocument{}, err
	}
	validation.Errors = append(validation.Errors, refValidation.Errors...)
	validation.Warnings = append(validation.Warnings, refValidation.Warnings...)
	if len(validation.Errors) > 0 {
		return preparedDocument{}, &ValidationError{
			Path:     docPath.String(),
			Errors:   validation.Errors,
			Warnings: validation.Warnings,
		}
	}

	rendered, err := frontmatter.Render(doc)
	if err != nil {
		return preparedDocument{}, err
	}
	hash := frontmatter.Hash(rendered)
	timestamp, _ := doc.Fields["timestamp"].(string)
	return preparedDocument{
		document:  doc,
		content:   rendered,
		hash:      hash,
		indexed:   index.BuildIndexedDocument(schema, docPath, doc, hash),
		timestamp: timestamp,
		warnings:  validation.Warnings,
	}, nil
}

func (s *Service) nextTimestampAfter(after time.Time) time.Time {
	now := s.now().UTC()
	floor := after.UTC()
	if s.lastTimestamp.After(floor) {
		floor = s.lastTimestamp
	}
	if !floor.IsZero() && !now.After(floor) {
		now = floor.Add(time.Nanosecond)
	}
	s.lastTimestamp = now
	return now
}

func (s *Service) validateDocumentRefs(schema ontology.Schema, docPath pathutil.Path, doc frontmatter.Document, mode ontology.Mode) (ontology.ValidationResult, error) {
	result := ontology.ValidationResult{}
	docType, _ := doc.Fields["type"].(string)
	typeDef, ok := schema.Types[docType]
	if !ok {
		return result, nil
	}
	for field, def := range typeDef.Fields {
		if def.Ref == "" {
			continue
		}
		raw, ok := doc.Fields[field].(string)
		if !ok || strings.TrimSpace(raw) == "" {
			continue
		}
		target, err := pathutil.Normalize(raw)
		if err != nil {
			continue
		}
		targetDoc, err := s.refTargetDocument(docPath, doc, target)
		if err != nil {
			if os.IsNotExist(err) {
				addValidationIssue(&result, mode, docPath, field, fmt.Sprintf("missing ref target %q", target.String()))
				continue
			}
			return ontology.ValidationResult{}, err
		}
		targetType, _ := targetDoc.Fields["type"].(string)
		if targetType != def.Ref {
			addValidationIssue(&result, mode, docPath, field, fmt.Sprintf("ref target %q must have type %q", target.String(), def.Ref))
		}
	}
	return result, nil
}

func (s *Service) refTargetDocument(docPath pathutil.Path, doc frontmatter.Document, target pathutil.Path) (frontmatter.Document, error) {
	if target == docPath {
		return doc, nil
	}
	state, err := s.readState(target)
	if err != nil {
		return frontmatter.Document{}, err
	}
	targetDoc, err := frontmatter.Parse(state.content)
	if err != nil {
		return frontmatter.Document{}, err
	}
	return targetDoc, nil
}

func buildEmbedder(cfg config.Config) embed.Embedder {
	switch strings.ToLower(strings.TrimSpace(cfg.Embedder.Type)) {
	case "", "off", "disabled":
		return nil
	case "openai", "openai-compatible":
		return embed.NewOpenAICompatible(
			cfg.Embedder.BaseURL,
			cfg.Embedder.Model,
			os.Getenv(strings.TrimSpace(cfg.Embedder.APIKeyEnv)),
			cfg.Embedder.Dimensions,
		)
	case "ollama":
		return embed.NewOllama(cfg.Embedder.BaseURL, cfg.Embedder.Model, cfg.Embedder.Dimensions)
	default:
		return nil
	}
}

func buildReranker(cfg config.Config) rerank.Reranker {
	if !cfg.Search.Rerank {
		return nil
	}
	if strings.TrimSpace(cfg.LLM.BaseURL) == "" || strings.TrimSpace(cfg.LLM.Model) == "" {
		return nil
	}
	return rerank.NewOpenAICompatible(
		cfg.LLM.BaseURL,
		cfg.LLM.Model,
		os.Getenv(strings.TrimSpace(cfg.LLM.APIKeyEnv)),
	)
}

func (s *Service) enqueueEmbedding(doc preparedDocument) {
	if s.embedQueue == nil {
		return
	}
	job := embeddingJob{
		path:      doc.indexed.Path,
		timestamp: doc.timestamp,
		hash:      doc.hash,
		chunks:    append([]index.Chunk(nil), doc.indexed.Chunks...),
	}
	select {
	case s.embedQueue <- job:
	default:
		_ = s.index.MarkEmbeddingState(context.Background(), doc.indexed.Path, "failed", "embedding queue full", doc.timestamp, doc.hash)
	}
}

func (s *Service) enqueueIndexedEmbedding(doc index.IndexedDocument) {
	if s.embedQueue == nil {
		return
	}
	job := embeddingJob{
		path:      doc.Path,
		timestamp: doc.Timestamp,
		hash:      doc.Hash,
		chunks:    append([]index.Chunk(nil), doc.Chunks...),
	}
	select {
	case s.embedQueue <- job:
	default:
		_ = s.index.MarkEmbeddingState(context.Background(), doc.Path, "failed", "embedding queue full", doc.Timestamp, doc.Hash)
	}
}

func (s *Service) enqueuePendingEmbeddings(ctx context.Context) error {
	if s.embedQueue == nil {
		return nil
	}
	documents, err := s.index.PendingEmbeddingDocuments(ctx, true)
	if err != nil {
		return err
	}
	return s.enqueueEmbeddingDocuments(ctx, documents)
}

func (s *Service) enqueueEmbeddingDocuments(ctx context.Context, documents []index.EmbeddingJobDocument) error {
	if s.embedQueue == nil {
		return nil
	}
	for _, doc := range documents {
		job := embeddingJob{
			path:      doc.Path,
			timestamp: doc.Timestamp,
			hash:      doc.Hash,
			chunks:    append([]index.Chunk(nil), doc.Chunks...),
		}
		select {
		case s.embedQueue <- job:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

func (s *Service) runEmbeddingWorker() {
	for job := range s.embedQueue {
		texts := make([]string, 0, len(job.chunks))
		ordinals := make([]int, 0, len(job.chunks))
		for _, chunk := range job.chunks {
			text := strings.TrimSpace(chunk.EmbedText)
			if text == "" {
				text = strings.TrimSpace(chunk.Text)
			}
			if text == "" {
				_ = s.index.MarkEmbeddingState(context.Background(), job.path, "failed", "empty chunk text", job.timestamp, job.hash)
				break
			}
			texts = append(texts, text)
			ordinals = append(ordinals, chunk.Ordinal)
		}
		if len(texts) != len(job.chunks) {
			if len(texts) != 0 {
				_ = s.index.MarkEmbeddingState(context.Background(), job.path, "failed", "embedding job has empty chunk text", job.timestamp, job.hash)
			}
			continue
		}
		if len(texts) == 0 {
			_ = s.index.MarkEmbeddingState(context.Background(), job.path, "failed", "embedding job has no chunks", job.timestamp, job.hash)
			continue
		}
		vectors, err := s.embedWithRetries(context.Background(), texts)
		if err != nil {
			_ = s.index.MarkEmbeddingState(context.Background(), job.path, "failed", err.Error(), job.timestamp, job.hash)
			continue
		}
		if len(vectors) != len(texts) {
			_ = s.index.MarkEmbeddingState(
				context.Background(),
				job.path,
				"failed",
				fmt.Sprintf("embedding response count mismatch: got %d vectors for %d chunks", len(vectors), len(texts)),
				job.timestamp,
				job.hash,
			)
			continue
		}
		payload := make([]index.ChunkEmbedding, 0, len(vectors))
		for i, vector := range vectors {
			payload = append(payload, index.ChunkEmbedding{
				Ordinal:   ordinals[i],
				Vector:    vector,
				UpdatedAt: job.timestamp,
				Hash:      job.hash,
			})
		}
		if err := s.index.UpsertEmbeddings(context.Background(), job.path, payload); err != nil {
			if !errors.Is(err, index.ErrStaleEmbedding) {
				_ = s.index.MarkEmbeddingState(context.Background(), job.path, "failed", err.Error(), job.timestamp, job.hash)
			}
			continue
		}
		_ = s.index.MarkEmbeddingState(context.Background(), job.path, "ready", "", job.timestamp, job.hash)
	}
}

func (s *Service) embedWithRetries(ctx context.Context, texts []string) ([][]float32, error) {
	attempts := s.embeddingMaxAttempts
	if attempts <= 0 {
		attempts = 1
	}
	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		vectors, err := s.embedder.Embed(ctx, texts)
		if err == nil {
			return vectors, nil
		}
		lastErr = err
		if attempt == attempts {
			break
		}
		delay := time.Duration(0)
		if s.embeddingBackoffDelay != nil {
			delay = s.embeddingBackoffDelay(attempt)
		}
		if delay <= 0 {
			continue
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	return nil, lastErr
}

func addValidationIssue(result *ontology.ValidationResult, mode ontology.Mode, docPath pathutil.Path, field string, message string) {
	issue := ontology.Issue{Path: docPath, Field: field, Message: message}
	if mode == ontology.Advisory {
		result.Warnings = append(result.Warnings, issue)
		return
	}
	result.Errors = append(result.Errors, issue)
}

func timestampFromState(current conceptState, err error) time.Time {
	if err != nil {
		return time.Time{}
	}
	doc, err := frontmatter.Parse(current.content)
	if err != nil {
		return time.Time{}
	}
	switch value := doc.Fields["timestamp"].(type) {
	case string:
		timestamp, err := time.Parse(time.RFC3339Nano, value)
		if err != nil {
			return time.Time{}
		}
		return timestamp.UTC()
	case time.Time:
		return value.UTC()
	default:
		return time.Time{}
	}
}
