package index

import (
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	_ "github.com/ncruces/go-sqlite3/driver"
	_ "github.com/ncruces/go-sqlite3/embed"
)

type Options struct {
	EnableVector    bool
	VectorDimension int
}

type DB struct {
	sql          *sql.DB
	vecAvailable bool
	vecStatus    string
}

type IndexedDocument struct {
	Path        string
	Hash        string
	Title       string
	Type        string
	Description string
	Timestamp   string
	Fields      map[string][]string
	Chunks      []Chunk
	Links       []Link
}

type Chunk struct {
	Ordinal   int
	Text      string
	EmbedText string
}

type Link struct {
	FromPath string
	ToPath   string
	Kind     string
	Field    string
}

func Open(path string, opts Options) (*DB, error) {
	if path == "" {
		return nil, fmt.Errorf("index path is required")
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	dsn := url.URL{
		Scheme: "file",
		Path:   path,
	}
	query := dsn.Query()
	query.Add("_pragma", "busy_timeout(5000)")
	query.Add("_pragma", "foreign_keys(1)")
	dsn.RawQuery = query.Encode()
	sqlDB, err := sql.Open("sqlite3", dsn.String())
	if err != nil {
		return nil, err
	}
	db := &DB{
		sql:       sqlDB,
		vecStatus: "unavailable",
	}
	if err := db.init(opts); err != nil {
		_ = sqlDB.Close()
		return nil, err
	}
	return db, nil
}

func (db *DB) Close() error {
	if db == nil || db.sql == nil {
		return nil
	}
	return db.sql.Close()
}
