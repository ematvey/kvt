package index

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

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
	DocumentCount    int
	CountsByType     map[string]int
	VecAvailable     bool
	VecStatus        string
	LastReconciledAt string
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
	return resp, nil
}
