package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/ematvey/kvt/internal/access"
	"github.com/ematvey/kvt/internal/frontmatter"
	"github.com/ematvey/kvt/internal/ontology"
)

func (s *Service) Read(ctx context.Context, req ReadRequest) (ReadResponse, error) {
	if err := ctx.Err(); err != nil {
		return ReadResponse{}, err
	}
	docPath, err := normalizeConceptPath(req.Path, s.cfg.Server.IndexMode)
	if err != nil {
		return ReadResponse{}, err
	}
	if err := access.CheckRead(req.Access, docPath.String()); err != nil {
		return ReadResponse{}, err
	}
	state, err := s.readState(docPath)
	if err != nil {
		return ReadResponse{}, err
	}
	doc, err := frontmatter.Parse(state.content)
	if err != nil {
		return ReadResponse{}, err
	}
	backlinks, err := s.index.Backlinks(ctx, docPath.String())
	if err != nil {
		return ReadResponse{}, err
	}
	if req.Access != nil {
		filtered := backlinks[:0]
		for _, link := range backlinks {
			if access.CanRead(req.Access, link.FromPath) {
				filtered = append(filtered, link)
			}
		}
		backlinks = filtered
	}
	schema, err := ontology.Load(s.root)
	if err != nil {
		return ReadResponse{}, err
	}
	validation := ontology.ValidateDocument(schema, docPath, doc, ontology.Advisory)
	refValidation, err := s.validateDocumentRefs(schema, docPath, doc, ontology.Advisory)
	if err != nil {
		return ReadResponse{}, err
	}
	validation.Warnings = append(validation.Warnings, refValidation.Warnings...)
	content := string(state.content)
	if req.StartLine > 0 || req.EndLine > 0 {
		content, err = lineRange(content, req.StartLine, req.EndLine)
		if err != nil {
			return ReadResponse{}, err
		}
	}
	return ReadResponse{
		Path:      docPath.String(),
		Content:   content,
		Hash:      state.hash,
		Document:  doc,
		Backlinks: backlinks,
		Warnings:  validation.Warnings,
	}, nil
}

func lineRange(content string, start int, end int) (string, error) {
	if start <= 0 {
		start = 1
	}
	lines := strings.Split(content, "\n")
	if end <= 0 || end > len(lines) {
		end = len(lines)
	}
	if start > len(lines) {
		return "", nil
	}
	if start > end {
		return "", fmt.Errorf("start_line must be <= end_line")
	}
	return strings.Join(lines[start-1:end], "\n"), nil
}
