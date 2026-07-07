package vault

import (
	"path"
	"regexp"
	"strings"

	"github.com/ematvey/kvt/internal/frontmatter"
	"github.com/ematvey/kvt/internal/ontology"
	"github.com/ematvey/kvt/internal/pathutil"
)

type Link struct {
	From  pathutil.Path
	To    pathutil.Path
	Kind  string
	Field string
}

var markdownLinkPattern = regexp.MustCompile(`\[[^]]+\]\(([^)]+)\)`)

func ExtractLinks(from pathutil.Path, doc frontmatter.Document, schema ontology.Schema) []Link {
	links := make([]Link, 0)
	for _, match := range markdownLinkPattern.FindAllSubmatch(doc.Body, -1) {
		target := strings.TrimSpace(string(match[1]))
		resolved, ok := resolveLink(from, target)
		if !ok {
			continue
		}
		links = append(links, Link{
			From: from,
			To:   resolved,
			Kind: "body",
		})
	}

	docType, _ := doc.Fields["type"].(string)
	typeDef, ok := schema.Types[docType]
	if !ok {
		return links
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
		links = append(links, Link{
			From:  from,
			To:    target,
			Kind:  "ref",
			Field: field,
		})
	}
	return links
}

func resolveLink(from pathutil.Path, raw string) (pathutil.Path, bool) {
	target := strings.TrimSpace(raw)
	if target == "" || strings.Contains(target, "://") || strings.HasPrefix(target, "#") {
		return "", false
	}
	if idx := strings.IndexAny(target, "#?"); idx >= 0 {
		target = target[:idx]
	}
	if target == "" {
		return "", false
	}
	dir := path.Dir(from.String())
	if dir == "." {
		dir = ""
	}
	if strings.HasPrefix(target, "/") {
		target = strings.TrimPrefix(target, "/")
	} else {
		target = path.Clean(path.Join(dir, target))
	}
	normalized, err := pathutil.Normalize(target)
	if err != nil {
		return "", false
	}
	return normalized, true
}
