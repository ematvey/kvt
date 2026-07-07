package index

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
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
	embed_text TEXT NOT NULL DEFAULT '',
	PRIMARY KEY (path, ordinal)
);

CREATE VIRTUAL TABLE IF NOT EXISTS kb_fts USING fts5(
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

CREATE TABLE IF NOT EXISTS kb_doc_embeddings (
	path TEXT PRIMARY KEY,
	state TEXT NOT NULL,
	last_error TEXT NOT NULL,
	updated_at TEXT NOT NULL,
	FOREIGN KEY(path) REFERENCES kb_docs(path) ON DELETE CASCADE
);
`

func (db *DB) init(opts Options) error {
	if _, err := db.sql.Exec(baseSchema); err != nil {
		return err
	}
	if err := db.ensureColumn("kb_chunks", "embed_text", "ALTER TABLE kb_chunks ADD COLUMN embed_text TEXT NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	if err := db.setMeta(context.Background(), "schema_version", "1"); err != nil {
		return err
	}
	return db.initVectorSupport(opts)
}

func (db *DB) initVectorSupport(opts Options) error {
	db.vecAvailable = false
	db.vecStatus = "unavailable"
	if !opts.EnableVector {
		db.vecStatus = "unavailable: disabled"
		return db.setMeta(context.Background(), "vec_status", db.vecStatus)
	}
	if opts.VectorDimension <= 0 {
		db.vecStatus = "unavailable: vector dimension required"
		return db.setMeta(context.Background(), "vec_status", db.vecStatus)
	}

	var version string
	if err := db.sql.QueryRow(`SELECT vec_version()`).Scan(&version); err != nil {
		status := "unavailable: " + err.Error()
		db.vecStatus = status
		if metaErr := db.setMeta(context.Background(), "vec_status", status); metaErr != nil {
			return metaErr
		}
		return nil
	}

	provenanceChanged, err := db.vectorProvenanceChanged(context.Background(), opts)
	if err != nil {
		return err
	}
	if provenanceChanged {
		if _, err := db.sql.Exec(`DROP TABLE IF EXISTS kb_vec`); err != nil {
			return err
		}
	}

	statement, err := vectorTableStatement(opts.VectorDimension)
	if err != nil {
		return err
	}
	if _, err := db.sql.Exec(statement); err != nil {
		status := "unavailable: " + err.Error()
		db.vecStatus = status
		if metaErr := db.setMeta(context.Background(), "vec_status", status); metaErr != nil {
			return metaErr
		}
		return nil
	}

	db.vecAvailable = true
	db.vecStatus = "available: " + version
	if err := db.refreshVectorProvenance(context.Background(), opts); err != nil {
		return err
	}
	return db.setMeta(context.Background(), "vec_status", db.vecStatus)
}

func vectorTableStatement(dimension int) (string, error) {
	if dimension <= 0 {
		return "", fmt.Errorf("vector dimension required")
	}
	return fmt.Sprintf(
		`CREATE VIRTUAL TABLE IF NOT EXISTS kb_vec USING vec0(chunk_id text primary key, path text, ordinal integer, embedding float[%d] distance_metric=cosine)`,
		dimension,
	), nil
}

func (db *DB) refreshVectorProvenance(ctx context.Context, opts Options) error {
	changed, err := db.vectorProvenanceChanged(ctx, opts)
	if err != nil {
		return err
	}
	disabledDirty := false
	if !changed {
		disabledDirty, err = db.hasDisabledCurrentEmbeddings(ctx)
		if err != nil {
			return err
		}
	}
	if changed || disabledDirty {
		if _, err := db.sql.ExecContext(ctx, `DELETE FROM kb_vec`); err != nil {
			return err
		}
		if _, err := db.sql.ExecContext(ctx, `
			UPDATE kb_doc_embeddings
			SET
				state = 'pending',
				last_error = '',
				updated_at = (SELECT d.timestamp FROM kb_docs d WHERE d.path = kb_doc_embeddings.path)
			WHERE EXISTS (SELECT 1 FROM kb_docs d WHERE d.path = kb_doc_embeddings.path)
		`); err != nil {
			return err
		}
	} else if err := db.cleanupOrphanVectorRows(ctx); err != nil {
		return err
	}
	if err := db.setMeta(ctx, "embedder_model", strings.TrimSpace(opts.VectorModel)); err != nil {
		return err
	}
	return db.setMeta(ctx, "embedder_dimensions", strconv.Itoa(opts.VectorDimension))
}

func (db *DB) hasDisabledCurrentEmbeddings(ctx context.Context) (bool, error) {
	var count int
	if err := db.sql.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM kb_doc_embeddings e
		JOIN kb_docs d ON d.path = e.path AND d.timestamp = e.updated_at
		WHERE e.state = 'disabled'
	`).Scan(&count); err != nil {
		return false, err
	}
	return count > 0, nil
}

func (db *DB) cleanupOrphanVectorRows(ctx context.Context) error {
	_, err := db.sql.ExecContext(ctx, `
		DELETE FROM kb_vec
		WHERE path NOT IN (SELECT path FROM kb_docs)
	`)
	return err
}

func (db *DB) vectorProvenanceChanged(ctx context.Context, opts Options) (bool, error) {
	wantModel := strings.TrimSpace(opts.VectorModel)
	wantDimensions := strconv.Itoa(opts.VectorDimension)

	gotModel, err := db.meta(ctx, "embedder_model")
	if err != nil && err != sql.ErrNoRows {
		return false, err
	}
	if err == sql.ErrNoRows || gotModel != wantModel {
		return true, nil
	}

	gotDimensions, err := db.meta(ctx, "embedder_dimensions")
	if err != nil && err != sql.ErrNoRows {
		return false, err
	}
	return err == sql.ErrNoRows || gotDimensions != wantDimensions, nil
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

func (db *DB) ensureColumn(table string, column string, alter string) error {
	rows, err := db.sql.Query(fmt.Sprintf("PRAGMA table_info(%s)", table))
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var cid int
		var name string
		var typ string
		var notNull int
		var dflt any
		var pk int
		if err := rows.Scan(&cid, &name, &typ, &notNull, &dflt, &pk); err != nil {
			return err
		}
		if name == column {
			return nil
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	_, err = db.sql.Exec(alter)
	return err
}
