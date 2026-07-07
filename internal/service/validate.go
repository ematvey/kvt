package service

import (
	"context"

	"github.com/ematvey/kvt/internal/ontology"
)

func (s *Service) Validate(ctx context.Context, req ValidateRequest) (ValidateResponse, error) {
	_ = req
	if err := ctx.Err(); err != nil {
		return ValidateResponse{}, err
	}
	schema, err := ontology.Load(s.root)
	if err != nil {
		return ValidateResponse{}, err
	}
	report, err := ontology.ValidateVault(s.root, schema)
	if err != nil {
		return ValidateResponse{}, err
	}
	resp := ValidateResponse{
		Errors:   report.Errors,
		Warnings: report.Warnings,
	}
	if req.ValidationMode.ontologyMode() == ontology.Advisory {
		resp.Warnings = append(resp.Warnings, resp.Errors...)
		resp.Errors = nil
	}
	return resp, nil
}
