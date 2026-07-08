package ontology

import (
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/ematvey/kvt/internal/frontmatter"
	"github.com/ematvey/kvt/internal/pathutil"
	"gopkg.in/yaml.v3"
)

type UnknownTypePolicy string

const (
	UnknownAllow  UnknownTypePolicy = "allow"
	UnknownWarn   UnknownTypePolicy = "warn"
	UnknownReject UnknownTypePolicy = "reject"
)

type Mode int

const (
	Strict Mode = iota
	Advisory
)

type Schema struct {
	Types        map[string]TypeDef `yaml:"types"`
	Rules        []Rule             `yaml:"rules"`
	UnknownTypes UnknownTypePolicy  `yaml:"unknown_types"`
}

type TypeDef struct {
	Required []string            `yaml:"required"`
	Optional []string            `yaml:"optional"`
	Fields   map[string]FieldDef `yaml:"fields"`
}

type FieldDef struct {
	Enum    []string `yaml:"enum"`
	Pattern string   `yaml:"pattern"`
	Ref     string   `yaml:"ref"`
}

type Rule struct {
	Path string `yaml:"path"`
	Type string `yaml:"type"`
}

type Issue struct {
	Path    pathutil.Path
	Field   string
	Message string
}

type ValidationResult struct {
	Errors   []Issue
	Warnings []Issue
}

type ValidationReport struct {
	Errors   []Issue
	Warnings []Issue
}

type vaultDoc struct {
	path pathutil.Path
	doc  frontmatter.Document
	typ  string
}

func Load(root string) (Schema, error) {
	schema := Schema{
		Types:        map[string]TypeDef{},
		UnknownTypes: UnknownWarn,
	}
	data, err := os.ReadFile(filepath.Join(root, "_ontology.yaml"))
	if err != nil {
		if os.IsNotExist(err) {
			return schema, nil
		}
		return Schema{}, err
	}
	if err := yaml.Unmarshal(data, &schema); err != nil {
		return Schema{}, fmt.Errorf("parse ontology: %w", err)
	}
	if schema.Types == nil {
		schema.Types = map[string]TypeDef{}
	}
	if schema.UnknownTypes == "" {
		schema.UnknownTypes = UnknownWarn
	}
	return schema, nil
}

func ValidateDocument(schema Schema, docPath pathutil.Path, doc frontmatter.Document, mode Mode) ValidationResult {
	result := ValidationResult{}
	docType, _ := doc.Fields["type"].(string)
	if strings.TrimSpace(docType) == "" {
		addViolation(&result, mode, docPath, "type", "missing required field")
		docType = ""
	}

	typeDef, knownType := schema.Types[docType]
	if docType != "" && !knownType {
		switch unknownPolicy(schema) {
		case UnknownReject:
			addViolation(&result, mode, docPath, "type", fmt.Sprintf("unknown type %q", docType))
		case UnknownWarn:
			result.Warnings = append(result.Warnings, Issue{Path: docPath, Field: "type", Message: fmt.Sprintf("unknown type %q", docType)})
		}
	}

	for _, rule := range schema.Rules {
		if !matchRule(rule.Path, docPath.String()) {
			continue
		}
		if docType != rule.Type {
			addViolation(&result, mode, docPath, "type", fmt.Sprintf("path rule %q requires type %q", rule.Path, rule.Type))
		}
	}

	if !knownType {
		return result
	}

	for _, field := range typeDef.Required {
		if isMissing(doc.Fields[field]) {
			addViolation(&result, mode, docPath, field, "missing required field")
		}
	}

	for field, def := range typeDef.Fields {
		value, ok := doc.Fields[field]
		if !ok || isMissing(value) {
			continue
		}
		text, ok := value.(string)
		if !ok {
			addViolation(&result, mode, docPath, field, "field must be a string")
			continue
		}
		if len(def.Enum) > 0 && !contains(def.Enum, text) {
			addViolation(&result, mode, docPath, field, fmt.Sprintf("value must be one of %s", strings.Join(def.Enum, ", ")))
		}
		if def.Pattern != "" {
			re, err := regexp.Compile(def.Pattern)
			if err != nil || !re.MatchString(text) {
				addViolation(&result, mode, docPath, field, fmt.Sprintf("value must match %q", def.Pattern))
			}
		}
		if def.Ref != "" {
			if _, err := pathutil.Normalize(text); err != nil {
				addViolation(&result, mode, docPath, field, err.Error())
			}
		}
	}

	return result
}

func ValidateVault(root string, schema Schema) (ValidationReport, error) {
	report := ValidationReport{}
	docs := map[pathutil.Path]vaultDoc{}

	err := filepath.WalkDir(root, func(filePath string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" || d.Name() == ".kvt" {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(d.Name()) != ".md" {
			return nil
		}
		rel, err := filepath.Rel(root, filePath)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if !pathutil.IsConceptMarkdownPathWithIndex(rel, true) {
			return nil
		}
		docPath, err := pathutil.Normalize(rel)
		if err != nil {
			report.Warnings = append(report.Warnings, Issue{
				Field:   "path",
				Message: err.Error(),
			})
			return nil
		}
		data, err := os.ReadFile(filePath)
		if err != nil {
			return err
		}
		doc, err := frontmatter.Parse(data)
		if err != nil {
			report.Errors = append(report.Errors, Issue{
				Path:    docPath,
				Field:   "frontmatter",
				Message: err.Error(),
			})
			return nil
		}
		result := ValidateDocument(schema, docPath, doc, Strict)
		report.Errors = append(report.Errors, result.Errors...)
		report.Warnings = append(report.Warnings, result.Warnings...)
		docType, _ := doc.Fields["type"].(string)
		docs[docPath] = vaultDoc{path: docPath, doc: doc, typ: docType}
		return nil
	})
	if err != nil {
		return ValidationReport{}, err
	}

	for _, doc := range docs {
		for _, link := range extractBodyLinks(doc.path, doc.doc.Body) {
			if link.err != nil {
				report.Errors = append(report.Errors, Issue{
					Path:    doc.path,
					Field:   "body",
					Message: link.err.Error(),
				})
				continue
			}
			if _, ok := docs[link.target]; ok {
				continue
			}
			readableIndex, err := readableIndexExists(root, link.target)
			if err != nil {
				return ValidationReport{}, err
			}
			if readableIndex {
				continue
			}
			report.Errors = append(report.Errors, Issue{
				Path:    doc.path,
				Field:   "body",
				Message: fmt.Sprintf("broken link to %q", link.target),
			})
		}

		typeDef, ok := schema.Types[doc.typ]
		if !ok {
			continue
		}
		for field, def := range typeDef.Fields {
			if def.Ref == "" {
				continue
			}
			raw, ok := doc.doc.Fields[field].(string)
			if !ok || strings.TrimSpace(raw) == "" {
				continue
			}
			target, err := pathutil.Normalize(raw)
			if err != nil {
				continue
			}
			targetDoc, ok := docs[target]
			if !ok {
				report.Errors = append(report.Errors, Issue{
					Path:    doc.path,
					Field:   field,
					Message: fmt.Sprintf("missing ref target %q", target),
				})
				continue
			}
			if def.Ref != "" && targetDoc.typ != def.Ref {
				report.Errors = append(report.Errors, Issue{
					Path:    doc.path,
					Field:   field,
					Message: fmt.Sprintf("ref target %q must have type %q", target, def.Ref),
				})
			}
		}
	}

	return report, nil
}

func unknownPolicy(schema Schema) UnknownTypePolicy {
	if schema.UnknownTypes == "" {
		return UnknownWarn
	}
	return schema.UnknownTypes
}

func addViolation(result *ValidationResult, mode Mode, docPath pathutil.Path, field string, message string) {
	issue := Issue{Path: docPath, Field: field, Message: message}
	if mode == Advisory {
		result.Warnings = append(result.Warnings, issue)
		return
	}
	result.Errors = append(result.Errors, issue)
}

func isMissing(value any) bool {
	if value == nil {
		return true
	}
	if text, ok := value.(string); ok {
		return strings.TrimSpace(text) == ""
	}
	return false
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func matchRule(pattern string, candidate string) bool {
	re := regexp.QuoteMeta(pattern)
	re = strings.ReplaceAll(re, `\*\*`, `.*`)
	re = strings.ReplaceAll(re, `\*`, `[^/]*`)
	ok, err := regexp.MatchString("^"+re+"$", candidate)
	return err == nil && ok
}

var markdownLinkPattern = regexp.MustCompile(`\[[^]]+\]\(([^)]+)\)`)

type bodyLink struct {
	target pathutil.Path
	err    error
}

func extractBodyLinks(from pathutil.Path, body []byte) []bodyLink {
	matches := markdownLinkPattern.FindAllSubmatch(body, -1)
	links := make([]bodyLink, 0, len(matches))
	for _, match := range matches {
		target := strings.TrimSpace(string(match[1]))
		if target == "" || strings.Contains(target, "://") || strings.HasPrefix(target, "#") {
			continue
		}
		resolved, err := resolveBodyLink(from, target)
		if err != nil {
			links = append(links, bodyLink{err: err})
			continue
		}
		links = append(links, bodyLink{target: resolved})
	}
	return links
}

func resolveBodyLink(from pathutil.Path, raw string) (pathutil.Path, error) {
	clean := raw
	if idx := strings.IndexAny(clean, "#?"); idx >= 0 {
		clean = clean[:idx]
	}
	if clean == "" {
		return "", nil
	}
	if strings.Contains(clean, "\\") {
		return "", fmt.Errorf("invalid internal link %q: path must use forward slashes", raw)
	}
	if strings.Contains(clean, "//") {
		return "", fmt.Errorf("invalid internal link %q: path has empty segment", raw)
	}
	dir := path.Dir(from.String())
	if dir == "." {
		dir = ""
	}
	candidate := clean
	if !strings.HasPrefix(candidate, "/") {
		candidate = path.Clean(path.Join(dir, candidate))
	} else {
		candidate = strings.TrimPrefix(candidate, "/")
	}
	normalized, err := pathutil.Normalize(candidate)
	if err != nil {
		return "", err
	}
	return normalized, nil
}

func readableIndexExists(root string, target pathutil.Path) (bool, error) {
	if path.Base(target.String()) != "index.md" {
		return false, nil
	}
	info, err := os.Stat(filepath.Join(root, filepath.FromSlash(target.String())))
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return !info.IsDir(), nil
}
