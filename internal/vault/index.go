package vault

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/ematvey/kvt/internal/frontmatter"
	"github.com/ematvey/kvt/internal/pathutil"
)

type dirEntry struct {
	name        string
	linkLabel   string
	linkTarget  string
	description string
}

func RegenerateIndexes(root string, affected pathutil.Path, limit int, rootOKFVersion string) ([]string, error) {
	dirs := ancestorDirs(affected)
	changed := make([]string, 0, len(dirs))
	for _, dir := range dirs {
		content, err := renderIndex(root, dir, limit, rootOKFVersion)
		if err != nil {
			return nil, err
		}
		indexPath := filepath.Join(root, filepath.FromSlash(indexRelPath(dir)))
		existing, err := os.ReadFile(indexPath)
		if err == nil && bytes.Equal(existing, content) {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(indexPath), 0o755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(indexPath, content, 0o644); err != nil {
			return nil, err
		}
		normalized, err := pathutil.Normalize(indexRelPath(dir))
		if err != nil {
			return nil, err
		}
		changed = append(changed, normalized.String())
	}
	return changed, nil
}

func ancestorDirs(affected pathutil.Path) []string {
	dir := pathDir(affected.String())
	dirs := []string{}
	for {
		dirs = append(dirs, dir)
		if dir == "" {
			break
		}
		dir = pathDir(dir)
	}
	return dirs
}

func renderIndex(root string, dir string, limit int, rootOKFVersion string) ([]byte, error) {
	fullDir := filepath.Join(root, filepath.FromSlash(dir))
	entries, err := os.ReadDir(fullDir)
	if err != nil {
		return nil, err
	}
	subdirs := make([]dirEntry, 0)
	concepts := make([]dirEntry, 0)
	for _, entry := range entries {
		name := entry.Name()
		if name == ".git" || name == ".kvt" || name == "index.md" {
			continue
		}
		if entry.IsDir() {
			subdirs = append(subdirs, dirEntry{
				name:       name,
				linkLabel:  name + "/",
				linkTarget: joinIndexTarget(dir, name),
			})
			continue
		}
		if filepath.Ext(name) != ".md" {
			continue
		}
		doc, err := readIndexDoc(filepath.Join(fullDir, name))
		if err != nil {
			return nil, err
		}
		title, _ := doc.Fields["title"].(string)
		if title == "" {
			title = strings.TrimSuffix(name, ".md")
		}
		description, _ := doc.Fields["description"].(string)
		concepts = append(concepts, dirEntry{
			name:        name,
			linkLabel:   title,
			linkTarget:  name,
			description: description,
		})
	}

	sort.Slice(subdirs, func(i, j int) bool { return subdirs[i].name < subdirs[j].name })
	sort.Slice(concepts, func(i, j int) bool { return concepts[i].name < concepts[j].name })

	fields := map[string]any{"type": "Index"}
	heading := "# Index\n"
	if dir == "" {
		fields["okf_version"] = rootOKFVersion
	} else {
		heading = "# " + dir + "/\n"
	}

	var body strings.Builder
	body.WriteString(heading)
	if len(subdirs) > 0 {
		body.WriteString("\n## Directories\n\n")
		writeEntries(&body, subdirs, limit)
	}
	if len(concepts) > 0 {
		body.WriteString("\n## Concepts\n\n")
		writeEntries(&body, concepts, limit)
	}

	return frontmatter.Render(frontmatter.Document{
		Fields: fields,
		Body:   []byte(body.String()),
	})
}

func readIndexDoc(path string) (frontmatter.Document, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return frontmatter.Document{}, err
	}
	return frontmatter.Parse(data)
}

func writeEntries(body *strings.Builder, entries []dirEntry, limit int) {
	maxEntries := len(entries)
	if limit > 0 && limit < maxEntries {
		maxEntries = limit
	}
	for i := 0; i < maxEntries; i++ {
		entry := entries[i]
		body.WriteString("- [")
		body.WriteString(entry.linkLabel)
		body.WriteString("](")
		body.WriteString(entry.linkTarget)
		body.WriteString(")")
		if entry.description != "" {
			body.WriteString(" - ")
			body.WriteString(entry.description)
		}
		body.WriteByte('\n')
	}
	if maxEntries < len(entries) {
		fmt.Fprintf(body, "- ... and %d more\n", len(entries)-maxEntries)
	}
}

func joinIndexTarget(dir string, child string) string {
	if dir == "" {
		return filepath.ToSlash(filepath.Join(child, "index.md"))
	}
	return filepath.ToSlash(filepath.Join(child, "index.md"))
}

func indexRelPath(dir string) string {
	if dir == "" {
		return "index.md"
	}
	return filepath.ToSlash(filepath.Join(dir, "index.md"))
}

func pathDir(value string) string {
	value = filepath.ToSlash(filepath.Dir(filepath.FromSlash(value)))
	if value == "." {
		return ""
	}
	return value
}
