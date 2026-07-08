package service

import (
	"context"
	"sort"

	"github.com/ematvey/kvt/internal/access"
	"github.com/ematvey/kvt/internal/gitops"
	"github.com/ematvey/kvt/internal/index"
	"github.com/ematvey/kvt/internal/ontology"
)

type HealthResponse struct {
	OK      bool
	Git     gitops.WorktreeStatus
	Summary index.SummaryResponse
	Push    PushStatus
}

type LogRequest struct {
	Cursor string
	Limit  int
	Access *access.Policy
}

type HistoryRequest struct {
	Path   string
	Cursor string
	Limit  int
	Access *access.Policy
}

type TypeInfo struct {
	Name     string
	Required []string
	Optional []string
	Fields   map[string]ontology.FieldDef
	Count    int
}

type TypesResponse struct {
	Types []TypeInfo
}

func (s *Service) Health(ctx context.Context) (HealthResponse, error) {
	if err := ctx.Err(); err != nil {
		return HealthResponse{}, err
	}
	status, err := s.git.Status(s.cfg.Git.Branch)
	if err != nil {
		return HealthResponse{}, err
	}
	summary, err := s.Summary(ctx, index.SummaryRequest{})
	if err != nil {
		return HealthResponse{}, err
	}
	return HealthResponse{
		OK:      status.BranchOK && !status.Detached,
		Git:     status,
		Summary: summary,
		Push:    s.PushStatus(ctx),
	}, nil
}

func (s *Service) Log(ctx context.Context, req LogRequest) (gitops.LogPage, error) {
	if err := ctx.Err(); err != nil {
		return gitops.LogPage{}, err
	}
	page, err := s.git.Log(req.Cursor, req.Limit)
	if err != nil {
		return gitops.LogPage{}, err
	}
	if req.Access == nil || access.LogAllowed(req.Access) {
		return page, nil
	}

	// Filter unauthorized file paths from each entry
	filtered := make([]gitops.LogEntry, 0, len(page.Entries))
	for _, entry := range page.Entries {
		entry.Files = access.FilterStrings(entry.Files, req.Access, access.Read)
		if len(entry.Files) == 0 {
			entry.FileSummary = ""
		}
		filtered = append(filtered, entry)
	}
	page.Entries = filtered
	return page, nil
}

func (s *Service) History(ctx context.Context, req HistoryRequest) (gitops.HistoryPage, error) {
	if err := ctx.Err(); err != nil {
		return gitops.HistoryPage{}, err
	}
	docPath, err := normalizeConceptPath(req.Path, s.cfg.Server.IndexMode)
	if err != nil {
		return gitops.HistoryPage{}, err
	}
	if err := access.CheckRead(req.Access, docPath.String()); err != nil {
		return gitops.HistoryPage{}, err
	}
	return s.git.History(docPath.String(), req.Cursor, req.Limit)
}

func (s *Service) Types(ctx context.Context) (TypesResponse, error) {
	if err := ctx.Err(); err != nil {
		return TypesResponse{}, err
	}
	schema, err := ontology.Load(s.root)
	if err != nil {
		return TypesResponse{}, err
	}
	summary, err := s.Summary(ctx, index.SummaryRequest{})
	if err != nil {
		return TypesResponse{}, err
	}
	names := make([]string, 0, len(schema.Types))
	for name := range schema.Types {
		names = append(names, name)
	}
	sort.Strings(names)
	resp := TypesResponse{Types: make([]TypeInfo, 0, len(names))}
	for _, name := range names {
		def := schema.Types[name]
		resp.Types = append(resp.Types, TypeInfo{
			Name:     name,
			Required: append([]string(nil), def.Required...),
			Optional: append([]string(nil), def.Optional...),
			Fields:   def.Fields,
			Count:    summary.CountsByType[name],
		})
	}
	return resp, nil
}
