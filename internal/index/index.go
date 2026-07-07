package index

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	sqlitevec "github.com/asg017/sqlite-vec-go-bindings/cgo"
	_ "github.com/mattn/go-sqlite3"
)

type Options struct{}

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
	Ordinal int
	Text    string
}

type Link struct {
	FromPath string
	ToPath   string
	Kind     string
	Field    string
}

var registerVec sync.Once

func Open(path string, _ Options) (*DB, error) {
	if path == "" {
		return nil, fmt.Errorf("index path is required")
	}
	registerVec.Do(sqlitevec.Auto)

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	sqlDB, err := sql.Open("sqlite3", path+"?_busy_timeout=5000&_foreign_keys=on")
	if err != nil {
		return nil, err
	}
	db := &DB{
		sql:       sqlDB,
		vecStatus: "unavailable",
	}
	if err := db.init(); err != nil {
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
