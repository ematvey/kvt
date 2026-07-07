package index

import (
	"context"
	"database/sql"
	"path"
)

func (db *DB) ApplyDocument(ctx context.Context, doc IndexedDocument) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := required(doc.Path, "path"); err != nil {
		return err
	}
	if path.Base(doc.Path) == "index.md" {
		return nil
	}

	tx, err := db.sql.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if err := deletePathRows(ctx, tx, doc.Path, false); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO kb_docs(path, hash, title, type, description, timestamp)
		VALUES(?, ?, ?, ?, ?, ?)
	`, doc.Path, doc.Hash, doc.Title, doc.Type, doc.Description, doc.Timestamp); err != nil {
		return err
	}
	for _, chunk := range doc.Chunks {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO kb_chunks(path, ordinal, text) VALUES(?, ?, ?)
		`, doc.Path, chunk.Ordinal, chunk.Text); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO kb_fts(path, ordinal, title, text) VALUES(?, ?, ?, ?)
		`, doc.Path, chunk.Ordinal, doc.Title, chunk.Text); err != nil {
			return err
		}
	}
	for key, values := range doc.Fields {
		for _, value := range values {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO kb_fields(path, key, value) VALUES(?, ?, ?)
			`, doc.Path, key, value); err != nil {
				return err
			}
		}
	}
	for _, link := range doc.Links {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO kb_links(from_path, to_path, kind, field) VALUES(?, ?, ?, ?)
		`, link.FromPath, link.ToPath, link.Kind, link.Field); err != nil {
			return err
		}
	}

	return tx.Commit()
}

func (db *DB) RemoveDocument(ctx context.Context, docPath string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := required(docPath, "path"); err != nil {
		return err
	}

	tx, err := db.sql.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if err := deletePathRows(ctx, tx, docPath, true); err != nil {
		return err
	}
	return tx.Commit()
}

func deletePathRows(ctx context.Context, tx interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}, docPath string, removeInbound bool) error {
	statements := []string{
		`DELETE FROM kb_docs WHERE path = ?`,
		`DELETE FROM kb_chunks WHERE path = ?`,
		`DELETE FROM kb_fts WHERE path = ?`,
		`DELETE FROM kb_fields WHERE path = ?`,
		`DELETE FROM kb_links WHERE from_path = ?`,
	}
	if removeInbound {
		statements = append(statements, `DELETE FROM kb_links WHERE to_path = ?`)
	}
	for _, statement := range statements {
		if _, err := tx.ExecContext(ctx, statement, docPath); err != nil {
			return err
		}
	}
	return nil
}
