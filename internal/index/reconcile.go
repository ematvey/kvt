package index

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ematvey/kvt/internal/frontmatter"
	"github.com/ematvey/kvt/internal/ontology"
	"github.com/ematvey/kvt/internal/pathutil"
	"github.com/ematvey/kvt/internal/vault"
)

type ReconcileResult struct {
	Applied int
	Removed int
}

func (db *DB) Reconcile(ctx context.Context, root string) (ReconcileResult, error) {
	if err := ctx.Err(); err != nil {
		return ReconcileResult{}, err
	}
	schema, err := ontology.Load(root)
	if err != nil {
		return ReconcileResult{}, err
	}

	existing, err := db.docHashes(ctx)
	if err != nil {
		return ReconcileResult{}, err
	}
	seen := map[string]struct{}{}
	result := ReconcileResult{}

	err = filepath.WalkDir(root, func(filePath string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() {
			if entry.Name() == ".git" || entry.Name() == ".kvt" {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(entry.Name()) != ".md" || entry.Name() == "index.md" {
			return nil
		}
		rel, err := filepath.Rel(root, filePath)
		if err != nil {
			return err
		}
		normalized, err := pathutil.Normalize(filepath.ToSlash(rel))
		if err != nil {
			return err
		}
		data, err := os.ReadFile(filePath)
		if err != nil {
			return err
		}
		docHash := frontmatter.Hash(data)
		seen[normalized.String()] = struct{}{}
		if existing[normalized.String()] == docHash {
			return nil
		}
		doc, err := frontmatter.Parse(data)
		if err != nil {
			return err
		}
		if err := db.ApplyDocument(ctx, BuildIndexedDocument(schema, normalized, doc, docHash)); err != nil {
			return err
		}
		result.Applied++
		return nil
	})
	if err != nil {
		return ReconcileResult{}, err
	}

	paths := make([]string, 0, len(existing))
	for docPath := range existing {
		paths = append(paths, docPath)
	}
	sort.Strings(paths)
	for _, docPath := range paths {
		if _, ok := seen[docPath]; ok {
			continue
		}
		if err := db.RemoveDocument(ctx, docPath); err != nil {
			return ReconcileResult{}, err
		}
		result.Removed++
	}

	if err := db.setMeta(ctx, "last_reconcile_at", time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		return ReconcileResult{}, err
	}
	return result, nil
}

func (db *DB) docHashes(ctx context.Context) (map[string]string, error) {
	rows, err := db.sql.QueryContext(ctx, `SELECT path, hash FROM kb_docs`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	hashes := map[string]string{}
	for rows.Next() {
		var path string
		var hash string
		if err := rows.Scan(&path, &hash); err != nil {
			return nil, err
		}
		hashes[path] = hash
	}
	return hashes, rows.Err()
}

func BuildIndexedDocument(schema ontology.Schema, docPath pathutil.Path, doc frontmatter.Document, hash string) IndexedDocument {
	title, _ := doc.Fields["title"].(string)
	typ, _ := doc.Fields["type"].(string)
	description, _ := doc.Fields["description"].(string)
	timestamp, _ := doc.Fields["timestamp"].(string)

	links := vault.ExtractLinks(docPath, doc, schema)
	indexLinks := make([]Link, 0, len(links))
	for _, link := range links {
		indexLinks = append(indexLinks, Link{
			FromPath: link.From.String(),
			ToPath:   link.To.String(),
			Kind:     link.Kind,
			Field:    link.Field,
		})
	}

	return IndexedDocument{
		Path:        docPath.String(),
		Hash:        hash,
		Title:       title,
		Type:        typ,
		Description: description,
		Timestamp:   timestamp,
		Fields:      extractFields(doc.Fields),
		Chunks:      buildChunks(doc, title, typ, description),
		Links:       indexLinks,
	}
}

func extractFields(fields map[string]any) map[string][]string {
	if len(fields) == 0 {
		return nil
	}
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	out := map[string][]string{}
	for _, key := range keys {
		switch value := fields[key].(type) {
		case string:
			if strings.TrimSpace(value) != "" {
				out[key] = append(out[key], value)
			}
		case []string:
			for _, item := range value {
				if strings.TrimSpace(item) != "" {
					out[key] = append(out[key], item)
				}
			}
		case []any:
			for _, item := range value {
				text, ok := item.(string)
				if ok && strings.TrimSpace(text) != "" {
					out[key] = append(out[key], text)
				}
			}
		}
		if len(out[key]) == 0 {
			delete(out, key)
		}
	}
	return out
}

func buildChunks(doc frontmatter.Document, title string, typ string, description string) []Chunk {
	chunks := []Chunk{}
	headerParts := []string{}
	if title != "" {
		headerParts = append(headerParts, title)
	}
	if typ != "" {
		headerParts = append(headerParts, typ)
	}
	if description != "" {
		headerParts = append(headerParts, description)
	}
	if len(headerParts) > 0 {
		chunks = append(chunks, Chunk{
			Ordinal: len(chunks),
			Text:    strings.Join(headerParts, " "),
		})
	}

	body := strings.TrimSpace(string(doc.Body))
	if body != "" {
		sections := splitBodyChunks(body)
		for _, section := range sections {
			chunks = append(chunks, Chunk{
				Ordinal: len(chunks),
				Text:    section,
			})
		}
	}
	return chunks
}

func splitBodyChunks(body string) []string {
	parts := strings.Split(body, "\n\n")
	chunks := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		chunks = append(chunks, strings.Join(strings.Fields(part), " "))
	}
	if len(chunks) == 0 && strings.TrimSpace(body) != "" {
		return []string{strings.Join(strings.Fields(body), " ")}
	}
	return chunks
}
