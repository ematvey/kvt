package index

import (
	"context"
	"fmt"
)

const baseSchema = `
PRAGMA journal_mode=WAL;

CREATE TABLE IF NOT EXISTS kb_docs (
	path TEXT PRIMARY KEY,
	hash TEXT NOT NULL,
	title TEXT NOT NULL,
	type TEXT NOT NULL,
	description TEXT NOT NULL,
	timestamp TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS kb_chunks (
	path TEXT NOT NULL,
	ordinal INTEGER NOT NULL,
	text TEXT NOT NULL,
	PRIMARY KEY (path, ordinal)
);

CREATE VIRTUAL TABLE IF NOT EXISTS kb_fts USING fts4(
	path,
	ordinal,
	title,
	text
);

CREATE TABLE IF NOT EXISTS kb_links (
	from_path TEXT NOT NULL,
	to_path TEXT NOT NULL,
	kind TEXT NOT NULL,
	field TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS kb_links_to_path_idx ON kb_links (to_path, from_path);
CREATE INDEX IF NOT EXISTS kb_links_from_path_idx ON kb_links (from_path);

CREATE TABLE IF NOT EXISTS kb_fields (
	path TEXT NOT NULL,
	key TEXT NOT NULL,
	value TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS kb_fields_lookup_idx ON kb_fields (key, value, path);
CREATE INDEX IF NOT EXISTS kb_fields_path_idx ON kb_fields (path);

CREATE TABLE IF NOT EXISTS kb_meta (
	key TEXT PRIMARY KEY,
	value TEXT NOT NULL
);
`

func (db *DB) init() error {
	if _, err := db.sql.Exec(baseSchema); err != nil {
		return err
	}
	if err := db.setMeta(context.Background(), "schema_version", "1"); err != nil {
		return err
	}
	return db.initVectorSupport()
}

func (db *DB) initVectorSupport() error {
	db.vecAvailable = false
	db.vecStatus = "unavailable"

	var version string
	if err := db.sql.QueryRow(`SELECT vec_version()`).Scan(&version); err != nil {
		status := "unavailable: " + err.Error()
		db.vecStatus = status
		if metaErr := db.setMeta(context.Background(), "vec_status", status); metaErr != nil {
			return metaErr
		}
		return nil
	}

	if _, err := db.sql.Exec(`CREATE VIRTUAL TABLE IF NOT EXISTS kb_vec USING vec0(path text primary key, embedding float[1])`); err != nil {
		status := "unavailable: " + err.Error()
		db.vecStatus = status
		if metaErr := db.setMeta(context.Background(), "vec_status", status); metaErr != nil {
			return metaErr
		}
		return nil
	}

	db.vecAvailable = true
	db.vecStatus = "available: " + version
	return db.setMeta(context.Background(), "vec_status", db.vecStatus)
}

func (db *DB) setMeta(ctx context.Context, key string, value string) error {
	_, err := db.sql.ExecContext(ctx, `
		INSERT INTO kb_meta(key, value) VALUES(?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value
	`, key, value)
	return err
}

func (db *DB) meta(ctx context.Context, key string) (string, error) {
	var value string
	err := db.sql.QueryRowContext(ctx, `SELECT value FROM kb_meta WHERE key = ?`, key).Scan(&value)
	if err != nil {
		return "", err
	}
	return value, nil
}

func normalizeLimit(limit int, fallback int) int {
	if limit > 0 {
		return limit
	}
	return fallback
}

func required(value string, field string) error {
	if value == "" {
		return fmt.Errorf("%s is required", field)
	}
	return nil
}
