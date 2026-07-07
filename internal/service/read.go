package service

import (
	"context"

	"github.com/ematvey/kvt/internal/frontmatter"
)

func (s *Service) Read(ctx context.Context, req ReadRequest) (ReadResponse, error) {
	if err := ctx.Err(); err != nil {
		return ReadResponse{}, err
	}
	docPath, err := normalizeConceptPath(req.Path)
	if err != nil {
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
	return ReadResponse{
		Path:      docPath.String(),
		Content:   string(state.content),
		Hash:      state.hash,
		Document:  doc,
		Backlinks: backlinks,
	}, nil
}
