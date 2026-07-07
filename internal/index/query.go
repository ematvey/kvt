package index

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

var ErrVectorUnavailable = errors.New("vector search unavailable")
var ErrStaleEmbedding = errors.New("stale embedding job")

type ListRequest struct {
	Type       string
	PathPrefix string
	FieldKey   string
	FieldValue string
	Limit      int
}

type DocumentSummary struct {
	Path        string
	Hash        string
	Title       string
	Type        string
	Description string
	Timestamp   string
}

type ListResponse struct {
	Documents []DocumentSummary
}

type GrepRequest struct {
	Query      string
	PathPrefix string
	Limit      int
}

type SearchRequest struct {
	Query      string
	PathPrefix string
	Limit      int
}

type SearchHit struct {
	Path    string
	Title   string
	Type    string
	Ordinal int
	Snippet string
	Text    string
	Score   float64
}

type VectorRequest struct {
	Embedding  []float32
	PathPrefix string
	Limit      int
}

type ChunkEmbedding struct {
	Ordinal   int
	Vector    []float32
	UpdatedAt string
}

type EmbeddingJobDocument struct {
	Path      string
	Timestamp string
	Chunks    []Chunk
}

type GrepMatch struct {
	Path    string
	Ordinal int
	Snippet string
	Text    string
}

type GrepResponse struct {
	Matches []GrepMatch
}

type SummaryRequest struct{}

type SummaryResponse struct {
	DocumentCount         int
	CountsByType          map[string]int
	VecAvailable          bool
	VecStatus             string
	LastReconciledAt      string
	EmbeddingPendingCount int
	EmbeddingFailedCount  int
}

func (db *DB) List(ctx context.Context, req ListRequest) (ListResponse, error) {
	if err := ctx.Err(); err != nil {
		return ListResponse{}, err
	}

	query := `
		SELECT d.path, d.hash, d.title, d.type, d.description, d.timestamp
		FROM kb_docs d
	`
	where := []string{}
	args := []any{}
	if req.Type != "" {
		where = append(where, "d.type = ?")
		args = append(args, req.Type)
	}
	if req.PathPrefix != "" {
		where = append(where, "d.path LIKE ?")
		args = append(args, req.PathPrefix+"%")
	}
	if req.FieldKey != "" {
		clause := `
			EXISTS (
				SELECT 1 FROM kb_fields f
				WHERE f.path = d.path AND f.key = ?
			)
		`
		args = append(args, req.FieldKey)
		if req.FieldValue != "" {
			clause = `
				EXISTS (
					SELECT 1 FROM kb_fields f
					WHERE f.path = d.path AND f.key = ? AND f.value = ?
				)
			`
			args = append(args, req.FieldValue)
		}
		where = append(where, clause)
	}
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY d.path ASC"
	if limit := normalizeLimit(req.Limit, 100); limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", limit)
	}

	rows, err := db.sql.QueryContext(ctx, query, args...)
	if err != nil {
		return ListResponse{}, err
	}
	defer rows.Close()

	resp := ListResponse{Documents: []DocumentSummary{}}
	for rows.Next() {
		var doc DocumentSummary
		if err := rows.Scan(&doc.Path, &doc.Hash, &doc.Title, &doc.Type, &doc.Description, &doc.Timestamp); err != nil {
			return ListResponse{}, err
		}
		resp.Documents = append(resp.Documents, doc)
	}
	return resp, rows.Err()
}

func (db *DB) Grep(ctx context.Context, req GrepRequest) (GrepResponse, error) {
	if err := ctx.Err(); err != nil {
		return GrepResponse{}, err
	}
	if strings.TrimSpace(req.Query) == "" {
		return GrepResponse{}, fmt.Errorf("query is required")
	}

	query := `
		SELECT path, CAST(ordinal AS INTEGER), text, text
		FROM kb_fts
		WHERE kb_fts MATCH ?
	`
	args := []any{req.Query}
	if req.PathPrefix != "" {
		query += " AND path LIKE ?"
		args = append(args, req.PathPrefix+"%")
	}
	query += " ORDER BY path ASC, CAST(ordinal AS INTEGER) ASC"
	query += fmt.Sprintf(" LIMIT %d", normalizeLimit(req.Limit, 20))

	rows, err := db.sql.QueryContext(ctx, query, args...)
	if err != nil {
		return GrepResponse{}, err
	}
	defer rows.Close()

	resp := GrepResponse{Matches: []GrepMatch{}}
	for rows.Next() {
		var match GrepMatch
		if err := rows.Scan(&match.Path, &match.Ordinal, &match.Snippet, &match.Text); err != nil {
			return GrepResponse{}, err
		}
		resp.Matches = append(resp.Matches, match)
	}
	return resp, rows.Err()
}

func (db *DB) Backlinks(ctx context.Context, docPath string) ([]Link, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	rows, err := db.sql.QueryContext(ctx, `
		SELECT from_path, to_path, kind, field
		FROM kb_links
		WHERE to_path = ?
		ORDER BY from_path ASC, kind ASC, field ASC
	`, docPath)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	links := []Link{}
	for rows.Next() {
		var link Link
		if err := rows.Scan(&link.FromPath, &link.ToPath, &link.Kind, &link.Field); err != nil {
			return nil, err
		}
		links = append(links, link)
	}
	return links, rows.Err()
}

func (db *DB) SearchKeywords(ctx context.Context, req SearchRequest) ([]SearchHit, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.Query) == "" {
		return nil, fmt.Errorf("query is required")
	}

	query := `
		SELECT f.path, d.title, d.type, CAST(f.ordinal AS INTEGER), snippet(kb_fts, 3, '', '', ' ... ', 16), c.text, bm25(kb_fts)
		FROM kb_fts f
		JOIN kb_docs d ON d.path = f.path
		JOIN kb_chunks c ON c.path = f.path AND c.ordinal = CAST(f.ordinal AS INTEGER)
		WHERE kb_fts MATCH ?
	`
	args := []any{req.Query}
	if req.PathPrefix != "" {
		query += " AND f.path LIKE ?"
		args = append(args, req.PathPrefix+"%")
	}
	query += " ORDER BY bm25(kb_fts), f.path ASC, CAST(f.ordinal AS INTEGER) ASC"
	query += fmt.Sprintf(" LIMIT %d", normalizeLimit(req.Limit, 20))

	rows, err := db.sql.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	hits := []SearchHit{}
	for rows.Next() {
		var hit SearchHit
		if err := rows.Scan(&hit.Path, &hit.Title, &hit.Type, &hit.Ordinal, &hit.Snippet, &hit.Text, &hit.Score); err != nil {
			return nil, err
		}
		hit.Score = -hit.Score
		if strings.TrimSpace(hit.Snippet) == "" {
			hit.Snippet = hit.Text
		}
		hits = append(hits, hit)
	}
	return hits, rows.Err()
}

func (db *DB) SearchVector(ctx context.Context, req VectorRequest) ([]SearchHit, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !db.vecAvailable {
		return nil, ErrVectorUnavailable
	}
	if len(req.Embedding) == 0 {
		return nil, fmt.Errorf("embedding is required")
	}

	limit := normalizeLimit(req.Limit, 20)
	query, args := vectorSearchStatement(req.Embedding, limit, req.PathPrefix)

	rows, err := db.sql.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	hits := []SearchHit{}
	for rows.Next() {
		var hit SearchHit
		if err := rows.Scan(&hit.Path, &hit.Title, &hit.Type, &hit.Ordinal, &hit.Snippet, &hit.Text, &hit.Score); err != nil {
			return nil, err
		}
		hit.Score = -hit.Score
		hits = append(hits, hit)
	}
	return hits, rows.Err()
}

func vectorSearchStatement(embedding []float32, candidateLimit int, pathPrefix string) (string, []any) {
	query := `
		SELECT v.path, d.title, d.type, CAST(v.ordinal AS INTEGER), c.text, c.text, v.distance
		FROM kb_vec v
		JOIN kb_docs d ON d.path = v.path
		JOIN kb_doc_embeddings e ON e.path = d.path AND e.state = 'ready' AND e.updated_at = d.timestamp
		JOIN kb_chunks c ON c.path = v.path AND c.ordinal = CAST(v.ordinal AS INTEGER)
		WHERE v.embedding MATCH ? AND k = ?
	`
	args := []any{vectorLiteral(embedding), candidateLimit}
	if strings.TrimSpace(pathPrefix) != "" {
		query += " AND v.path >= ? AND v.path < ?"
		args = append(args, pathPrefix, pathPrefixUpperBound(pathPrefix))
	}
	query += " ORDER BY v.distance ASC"
	return query, args
}

func pathPrefixUpperBound(prefix string) string {
	if prefix == "" {
		return ""
	}
	bytes := []byte(prefix)
	for i := len(bytes) - 1; i >= 0; i-- {
		if bytes[i] != 0xff {
			bytes[i]++
			return string(bytes[:i+1])
		}
	}
	return prefix + "\xff"
}

func (db *DB) UpsertEmbeddings(ctx context.Context, docPath string, chunks []ChunkEmbedding) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !db.vecAvailable {
		return ErrVectorUnavailable
	}
	if len(chunks) == 0 {
		return fmt.Errorf("embedding payload is empty")
	}
	expectedTimestamp := strings.TrimSpace(chunks[0].UpdatedAt)
	if expectedTimestamp == "" {
		return fmt.Errorf("embedding timestamp is required")
	}
	for i, chunk := range chunks {
		if len(chunk.Vector) == 0 {
			return fmt.Errorf("embedding %d is empty", i)
		}
		if strings.TrimSpace(chunk.UpdatedAt) != expectedTimestamp {
			return fmt.Errorf("embedding %d timestamp mismatch", i)
		}
	}

	tx, err := db.sql.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var currentTimestamp string
	if err := tx.QueryRowContext(ctx, `SELECT timestamp FROM kb_docs WHERE path = ?`, docPath).Scan(&currentTimestamp); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrStaleEmbedding
		}
		return err
	}
	if currentTimestamp != expectedTimestamp {
		return ErrStaleEmbedding
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM kb_vec WHERE path = ?`, docPath); err != nil {
		return err
	}
	for _, chunk := range chunks {
		chunkID := fmt.Sprintf("%s#%d", docPath, chunk.Ordinal)
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO kb_vec(chunk_id, path, ordinal, embedding) VALUES(?, ?, ?, ?)
		`, chunkID, docPath, chunk.Ordinal, vectorLiteral(chunk.Vector)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (db *DB) PendingEmbeddingDocuments(ctx context.Context, includeFailed bool) ([]EmbeddingJobDocument, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	states := []string{"pending"}
	if includeFailed {
		states = append(states, "failed")
	}
	placeholders := make([]string, 0, len(states))
	args := make([]any, 0, len(states))
	for _, state := range states {
		placeholders = append(placeholders, "?")
		args = append(args, state)
	}
	query := fmt.Sprintf(`
		SELECT d.path, d.timestamp, c.ordinal, c.text, c.embed_text
		FROM kb_docs d
		JOIN kb_doc_embeddings e ON e.path = d.path AND e.updated_at = d.timestamp
		JOIN kb_chunks c ON c.path = d.path
		WHERE e.state IN (%s)
		ORDER BY d.path ASC, c.ordinal ASC
	`, strings.Join(placeholders, ", "))
	rows, err := db.sql.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	documents := []EmbeddingJobDocument{}
	indexByPath := map[string]int{}
	for rows.Next() {
		var docPath string
		var timestamp string
		var chunk Chunk
		if err := rows.Scan(&docPath, &timestamp, &chunk.Ordinal, &chunk.Text, &chunk.EmbedText); err != nil {
			return nil, err
		}
		idx, ok := indexByPath[docPath]
		if !ok {
			idx = len(documents)
			indexByPath[docPath] = idx
			documents = append(documents, EmbeddingJobDocument{
				Path:      docPath,
				Timestamp: timestamp,
			})
		}
		documents[idx].Chunks = append(documents[idx].Chunks, chunk)
	}
	return documents, rows.Err()
}

func (db *DB) MarkEmbeddingState(ctx context.Context, docPath string, state string, lastError string, updatedAt string) error {
	var currentTimestamp string
	if err := db.sql.QueryRowContext(ctx, `SELECT timestamp FROM kb_docs WHERE path = ?`, docPath).Scan(&currentTimestamp); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrStaleEmbedding
		}
		return err
	}
	if currentTimestamp != updatedAt {
		return ErrStaleEmbedding
	}
	_, err := db.sql.ExecContext(ctx, `
		INSERT INTO kb_doc_embeddings(path, state, last_error, updated_at)
		VALUES(?, ?, ?, ?)
		ON CONFLICT(path) DO UPDATE SET
			state = excluded.state,
			last_error = excluded.last_error,
			updated_at = excluded.updated_at
	`, docPath, state, lastError, updatedAt)
	return err
}

func (db *DB) VectorAvailable() bool {
	return db.vecAvailable
}

func (db *DB) Summary(ctx context.Context, _ SummaryRequest) (SummaryResponse, error) {
	if err := ctx.Err(); err != nil {
		return SummaryResponse{}, err
	}

	resp := SummaryResponse{
		CountsByType: map[string]int{},
		VecAvailable: db.vecAvailable,
		VecStatus:    db.vecStatus,
	}

	if err := db.sql.QueryRowContext(ctx, `SELECT COUNT(*) FROM kb_docs`).Scan(&resp.DocumentCount); err != nil {
		return SummaryResponse{}, err
	}

	rows, err := db.sql.QueryContext(ctx, `
		SELECT type, COUNT(*)
		FROM kb_docs
		GROUP BY type
		ORDER BY type ASC
	`)
	if err != nil {
		return SummaryResponse{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var typ string
		var count int
		if err := rows.Scan(&typ, &count); err != nil {
			return SummaryResponse{}, err
		}
		resp.CountsByType[typ] = count
	}
	if err := rows.Err(); err != nil {
		return SummaryResponse{}, err
	}

	if value, err := db.meta(ctx, "last_reconcile_at"); err == nil {
		resp.LastReconciledAt = value
	} else if err != nil && err != sql.ErrNoRows {
		return SummaryResponse{}, err
	}
	if value, err := db.meta(ctx, "vec_status"); err == nil {
		resp.VecStatus = value
	} else if err != nil && err != sql.ErrNoRows {
		return SummaryResponse{}, err
	}

	embeddingRows, err := db.sql.QueryContext(ctx, `
		SELECT e.state, COUNT(*)
		FROM kb_doc_embeddings e
		JOIN kb_docs d ON d.path = e.path AND d.timestamp = e.updated_at
		WHERE e.state IN ('pending', 'failed')
		GROUP BY e.state
	`)
	if err != nil {
		return SummaryResponse{}, err
	}
	defer embeddingRows.Close()
	for embeddingRows.Next() {
		var state string
		var count int
		if err := embeddingRows.Scan(&state, &count); err != nil {
			return SummaryResponse{}, err
		}
		switch state {
		case "pending":
			resp.EmbeddingPendingCount = count
		case "failed":
			resp.EmbeddingFailedCount = count
		}
	}
	if err := embeddingRows.Err(); err != nil {
		return SummaryResponse{}, err
	}
	return resp, nil
}

func vectorLiteral(vector []float32) string {
	parts := make([]string, 0, len(vector))
	for _, value := range vector {
		parts = append(parts, fmt.Sprintf("%g", value))
	}
	return "[" + strings.Join(parts, ",") + "]"
}
