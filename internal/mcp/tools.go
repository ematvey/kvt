package mcp

import (
	"context"

	"github.com/ematvey/kvt/internal/gitops"
	"github.com/ematvey/kvt/internal/index"
	"github.com/ematvey/kvt/internal/ontology"
	"github.com/ematvey/kvt/internal/service"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type emptyInput struct{}

type searchInput struct {
	Query      string `json:"query" jsonschema:"search query"`
	PathPrefix string `json:"path_prefix,omitempty" jsonschema:"optional bundle-relative path prefix"`
	Limit      int    `json:"limit,omitempty" jsonschema:"maximum number of results"`
}

type listInput struct {
	Type       string `json:"type,omitempty" jsonschema:"optional concept type filter"`
	PathPrefix string `json:"path_prefix,omitempty" jsonschema:"optional bundle-relative path prefix"`
	FieldKey   string `json:"field_key,omitempty" jsonschema:"optional frontmatter field key filter"`
	FieldValue string `json:"field_value,omitempty" jsonschema:"optional frontmatter field value filter"`
	Limit      int    `json:"limit,omitempty" jsonschema:"maximum number of documents"`
}

type pathInput struct {
	Path string `json:"path" jsonschema:"bundle-relative markdown path"`
}

type pageInput struct {
	Cursor string `json:"cursor,omitempty" jsonschema:"pagination cursor"`
	Limit  int    `json:"limit,omitempty" jsonschema:"page size"`
}

type historyInput struct {
	Path   string `json:"path" jsonschema:"bundle-relative markdown path"`
	Cursor string `json:"cursor,omitempty" jsonschema:"pagination cursor"`
	Limit  int    `json:"limit,omitempty" jsonschema:"page size"`
}

type writeInput struct {
	Path           string `json:"path" jsonschema:"bundle-relative markdown path"`
	Content        string `json:"content" jsonschema:"complete markdown content"`
	BaseHash       string `json:"base_hash,omitempty" jsonschema:"current content hash for conflict detection"`
	Agent          string `json:"agent,omitempty" jsonschema:"agent name for commit body"`
	ValidationMode string `json:"validation_mode,omitempty" jsonschema:"strict or advisory"`
}

type editInput struct {
	Path           string `json:"path" jsonschema:"bundle-relative markdown path"`
	BaseHash       string `json:"base_hash,omitempty" jsonschema:"current content hash for conflict detection"`
	OldString      string `json:"old_string" jsonschema:"exact string to replace"`
	NewString      string `json:"new_string" jsonschema:"replacement string"`
	ReplaceAll     bool   `json:"replace_all,omitempty" jsonschema:"replace every match instead of requiring uniqueness"`
	Agent          string `json:"agent,omitempty" jsonschema:"agent name for commit body"`
	ValidationMode string `json:"validation_mode,omitempty" jsonschema:"strict or advisory"`
}

type deleteInput struct {
	Path     string `json:"path" jsonschema:"bundle-relative markdown path"`
	BaseHash string `json:"base_hash,omitempty" jsonschema:"current content hash for conflict detection"`
	Agent    string `json:"agent,omitempty" jsonschema:"agent name for commit body"`
}

type validateInput struct {
	ValidationMode string `json:"validation_mode,omitempty" jsonschema:"strict or advisory"`
}

type summaryOutput struct {
	DocumentCount         int            `json:"document_count"`
	CountsByType          map[string]int `json:"counts_by_type"`
	VecAvailable          bool           `json:"vec_available"`
	VecStatus             string         `json:"vec_status"`
	LastReconciledAt      string         `json:"last_reconciled_at"`
	EmbeddingPendingCount int            `json:"embedding_pending_count"`
	EmbeddingFailedCount  int            `json:"embedding_failed_count"`
}

type howtoOutput struct {
	Text string `json:"text"`
}

type searchOutput struct {
	Hits     []searchHitOutput `json:"hits"`
	Degraded []string          `json:"degraded"`
}

type searchHitOutput struct {
	Path    string  `json:"path"`
	Title   string  `json:"title"`
	Type    string  `json:"type"`
	Snippet string  `json:"snippet"`
	Score   float64 `json:"score"`
}

type grepOutput struct {
	Matches []grepMatchOutput `json:"matches"`
}

type grepMatchOutput struct {
	Path    string `json:"path"`
	Ordinal int    `json:"ordinal"`
	Snippet string `json:"snippet"`
	Text    string `json:"text"`
}

type listOutput struct {
	Documents []documentSummaryOutput `json:"documents"`
}

type documentSummaryOutput struct {
	Path        string `json:"path"`
	Hash        string `json:"hash"`
	Title       string `json:"title"`
	Type        string `json:"type"`
	Description string `json:"description"`
	Timestamp   string `json:"timestamp"`
}

type readOutput struct {
	Path      string       `json:"path"`
	Content   string       `json:"content"`
	Hash      string       `json:"hash"`
	Backlinks []linkOutput `json:"backlinks"`
}

type linkOutput struct {
	FromPath string `json:"from_path"`
	ToPath   string `json:"to_path"`
	Kind     string `json:"kind"`
	Field    string `json:"field"`
}

type typesOutput struct {
	Types []typeInfoOutput `json:"types"`
}

type typeInfoOutput struct {
	Name     string                    `json:"name"`
	Required []string                  `json:"required"`
	Optional []string                  `json:"optional"`
	Fields   map[string]fieldDefOutput `json:"fields"`
}

type fieldDefOutput struct {
	Enum    []string `json:"enum,omitempty"`
	Pattern string   `json:"pattern,omitempty"`
	Ref     string   `json:"ref,omitempty"`
}

type logOutput struct {
	Entries    []logEntryOutput `json:"entries"`
	NextCursor string           `json:"next_cursor"`
}

type logEntryOutput struct {
	Hash        string   `json:"hash"`
	ShortHash   string   `json:"short_hash"`
	Timestamp   string   `json:"timestamp"`
	Author      string   `json:"author"`
	Subject     string   `json:"subject"`
	Files       []string `json:"files"`
	FileSummary string   `json:"file_summary"`
}

type historyOutput struct {
	Entries    []historyEntryOutput `json:"entries"`
	NextCursor string               `json:"next_cursor"`
}

type historyEntryOutput struct {
	Hash      string `json:"hash"`
	ShortHash string `json:"short_hash"`
	Timestamp string `json:"timestamp"`
	Author    string `json:"author"`
	Subject   string `json:"subject"`
	Diff      string `json:"diff"`
}

type writeOutput struct {
	Path         string        `json:"path"`
	Content      string        `json:"content"`
	Hash         string        `json:"hash"`
	Timestamp    string        `json:"timestamp"`
	Warnings     []issueOutput `json:"warnings"`
	ChangedPaths []string      `json:"changed_paths"`
	Commit       commitOutput  `json:"commit"`
}

type deleteOutput struct {
	Path         string       `json:"path"`
	ChangedPaths []string     `json:"changed_paths"`
	Commit       commitOutput `json:"commit"`
}

type commitOutput struct {
	Hash      string `json:"hash"`
	ShortHash string `json:"short_hash"`
}

type validateOutput struct {
	Errors   []issueOutput `json:"errors"`
	Warnings []issueOutput `json:"warnings"`
}

type issueOutput struct {
	Path    string `json:"path"`
	Field   string `json:"field"`
	Message string `json:"message"`
}

func registerTools(server *Server, svc *service.Service) {
	addTool(server, "kvt_summary", "Return vault health-oriented summary counts and embedding status.", func(ctx context.Context, _ emptyInput) (summaryOutput, error) {
		resp, err := svc.Summary(ctx, index.SummaryRequest{})
		return summaryOutput{
			DocumentCount:         resp.DocumentCount,
			CountsByType:          resp.CountsByType,
			VecAvailable:          resp.VecAvailable,
			VecStatus:             resp.VecStatus,
			LastReconciledAt:      resp.LastReconciledAt,
			EmbeddingPendingCount: resp.EmbeddingPendingCount,
			EmbeddingFailedCount:  resp.EmbeddingFailedCount,
		}, err
	})
	addTool(server, "kvt_howto", "Return concise KVT workflow guidance for coding agents.", func(context.Context, emptyInput) (howtoOutput, error) {
		return howtoOutput{Text: DefaultHowto()}, nil
	})
	addTool(server, "kvt_search", "Use first for semantic or keyword discovery across the vault.", func(ctx context.Context, in searchInput) (searchOutput, error) {
		resp, err := svc.Search(ctx, service.SearchRequest{Query: in.Query, PathPrefix: in.PathPrefix, Limit: in.Limit})
		return searchOutputFrom(resp), err
	})
	addTool(server, "kvt_grep", "Use for exact content lookup when you know text that should appear.", func(ctx context.Context, in searchInput) (grepOutput, error) {
		resp, err := svc.Grep(ctx, index.GrepRequest{Query: in.Query, PathPrefix: in.PathPrefix, Limit: in.Limit})
		return grepOutputFrom(resp), err
	})
	addTool(server, "kvt_list", "List concepts by type, path prefix, or frontmatter field filters.", func(ctx context.Context, in listInput) (listOutput, error) {
		resp, err := svc.List(ctx, index.ListRequest{
			Type:       in.Type,
			PathPrefix: in.PathPrefix,
			FieldKey:   in.FieldKey,
			FieldValue: in.FieldValue,
			Limit:      in.Limit,
		})
		return listOutputFrom(resp), err
	})
	addTool(server, "kvt_read", "Read one concept and return current content, hash, and backlinks.", func(ctx context.Context, in pathInput) (readOutput, error) {
		resp, err := svc.Read(ctx, service.ReadRequest{Path: in.Path})
		return readOutputFrom(resp), err
	})
	addTool(server, "kvt_types", "List ontology types and field constraints.", func(ctx context.Context, _ emptyInput) (typesOutput, error) {
		resp, err := svc.Types(ctx)
		return typesOutputFrom(resp), err
	})
	addTool(server, "kvt_log", "Return paginated git commit history for the vault.", func(ctx context.Context, in pageInput) (logOutput, error) {
		resp, err := svc.Log(ctx, service.LogRequest{Cursor: in.Cursor, Limit: in.Limit})
		return logOutputFrom(resp), err
	})
	addTool(server, "kvt_history", "Return paginated commit history and diffs for one concept.", func(ctx context.Context, in historyInput) (historyOutput, error) {
		resp, err := svc.History(ctx, service.HistoryRequest{Path: in.Path, Cursor: in.Cursor, Limit: in.Limit})
		return historyOutputFrom(resp), err
	})
	addTool(server, "kvt_write", "Write complete concept content; use base_hash when updating an existing concept.", func(ctx context.Context, in writeInput) (writeOutput, error) {
		resp, err := svc.Write(ctx, service.WriteRequest{
			Path:           in.Path,
			Content:        in.Content,
			BaseHash:       in.BaseHash,
			Agent:          in.Agent,
			ValidationMode: validationMode(in.ValidationMode),
		})
		return writeOutputFrom(resp), err
	})
	addTool(server, "kvt_edit", "Edit a concept by exact string replacement; read first and pass base_hash.", func(ctx context.Context, in editInput) (writeOutput, error) {
		resp, err := svc.Edit(ctx, service.EditRequest{
			Path:           in.Path,
			BaseHash:       in.BaseHash,
			OldString:      in.OldString,
			NewString:      in.NewString,
			ReplaceAll:     in.ReplaceAll,
			Agent:          in.Agent,
			ValidationMode: validationMode(in.ValidationMode),
		})
		return writeOutputFrom(resp), err
	})
	addTool(server, "kvt_delete", "Delete a concept; pass base_hash to avoid stale deletes.", func(ctx context.Context, in deleteInput) (deleteOutput, error) {
		resp, err := svc.Delete(ctx, service.DeleteRequest{Path: in.Path, BaseHash: in.BaseHash, Agent: in.Agent})
		return deleteOutputFrom(resp), err
	})
	addTool(server, "kvt_validate", "Run ontology and link validation.", func(ctx context.Context, in validateInput) (validateOutput, error) {
		resp, err := svc.Validate(ctx, service.ValidateRequest{ValidationMode: validationMode(in.ValidationMode)})
		return validateOutputFrom(resp), err
	})
}

func addTool[In, Out any](server *Server, name string, description string, handler func(context.Context, In) (Out, error)) {
	server.toolNames[name] = true
	mcpsdk.AddTool(server.sdk, &mcpsdk.Tool{
		Name:        name,
		Description: description,
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in In) (*mcpsdk.CallToolResult, Out, error) {
		out, err := handler(ctx, in)
		return nil, out, err
	})
}

func validationMode(raw string) service.ValidationMode {
	if raw == string(service.ValidationModeAdvisory) {
		return service.ValidationModeAdvisory
	}
	return service.ValidationModeStrict
}

func searchOutputFrom(resp service.SearchResponse) searchOutput {
	out := searchOutput{Degraded: append([]string(nil), resp.Degraded...)}
	for _, hit := range resp.Hits {
		out.Hits = append(out.Hits, searchHitOutput{
			Path:    hit.Path,
			Title:   hit.Title,
			Type:    hit.Type,
			Snippet: hit.Snippet,
			Score:   hit.Score,
		})
	}
	return out
}

func grepOutputFrom(resp index.GrepResponse) grepOutput {
	out := grepOutput{}
	for _, match := range resp.Matches {
		out.Matches = append(out.Matches, grepMatchOutput{
			Path:    match.Path,
			Ordinal: match.Ordinal,
			Snippet: match.Snippet,
			Text:    match.Text,
		})
	}
	return out
}

func listOutputFrom(resp index.ListResponse) listOutput {
	out := listOutput{}
	for _, doc := range resp.Documents {
		out.Documents = append(out.Documents, documentSummaryOutput{
			Path:        doc.Path,
			Hash:        doc.Hash,
			Title:       doc.Title,
			Type:        doc.Type,
			Description: doc.Description,
			Timestamp:   doc.Timestamp,
		})
	}
	return out
}

func readOutputFrom(resp service.ReadResponse) readOutput {
	return readOutput{
		Path:      resp.Path,
		Content:   resp.Content,
		Hash:      resp.Hash,
		Backlinks: linksOutputFrom(resp.Backlinks),
	}
}

func typesOutputFrom(resp service.TypesResponse) typesOutput {
	out := typesOutput{}
	for _, typ := range resp.Types {
		fields := map[string]fieldDefOutput{}
		for name, field := range typ.Fields {
			fields[name] = fieldDefOutputFrom(field)
		}
		out.Types = append(out.Types, typeInfoOutput{
			Name:     typ.Name,
			Required: append([]string(nil), typ.Required...),
			Optional: append([]string(nil), typ.Optional...),
			Fields:   fields,
		})
	}
	return out
}

func fieldDefOutputFrom(field ontology.FieldDef) fieldDefOutput {
	return fieldDefOutput{
		Enum:    append([]string(nil), field.Enum...),
		Pattern: field.Pattern,
		Ref:     field.Ref,
	}
}

func logOutputFrom(resp gitops.LogPage) logOutput {
	out := logOutput{NextCursor: resp.NextCursor}
	for _, entry := range resp.Entries {
		out.Entries = append(out.Entries, logEntryOutput{
			Hash:        entry.Hash,
			ShortHash:   entry.ShortHash,
			Timestamp:   entry.Timestamp,
			Author:      entry.Author,
			Subject:     entry.Subject,
			Files:       append([]string(nil), entry.Files...),
			FileSummary: entry.FileSummary,
		})
	}
	return out
}

func historyOutputFrom(resp gitops.HistoryPage) historyOutput {
	out := historyOutput{NextCursor: resp.NextCursor}
	for _, entry := range resp.Entries {
		out.Entries = append(out.Entries, historyEntryOutput{
			Hash:      entry.Hash,
			ShortHash: entry.ShortHash,
			Timestamp: entry.Timestamp,
			Author:    entry.Author,
			Subject:   entry.Subject,
			Diff:      entry.Diff,
		})
	}
	return out
}

func writeOutputFrom(resp service.WriteResponse) writeOutput {
	return writeOutput{
		Path:         resp.Path,
		Content:      resp.Content,
		Hash:         resp.Hash,
		Timestamp:    resp.Timestamp,
		Warnings:     issuesOutputFrom(resp.Warnings),
		ChangedPaths: append([]string(nil), resp.ChangedPaths...),
		Commit:       commitOutputFrom(resp.Commit),
	}
}

func deleteOutputFrom(resp service.DeleteResponse) deleteOutput {
	return deleteOutput{
		Path:         resp.Path,
		ChangedPaths: append([]string(nil), resp.ChangedPaths...),
		Commit:       commitOutputFrom(resp.Commit),
	}
}

func validateOutputFrom(resp service.ValidateResponse) validateOutput {
	return validateOutput{
		Errors:   issuesOutputFrom(resp.Errors),
		Warnings: issuesOutputFrom(resp.Warnings),
	}
}

func commitOutputFrom(commit service.CommitInfo) commitOutput {
	return commitOutput{Hash: commit.Hash, ShortHash: commit.ShortHash}
}

func issuesOutputFrom(issues []ontology.Issue) []issueOutput {
	out := make([]issueOutput, 0, len(issues))
	for _, issue := range issues {
		out = append(out, issueOutput{
			Path:    issue.Path.String(),
			Field:   issue.Field,
			Message: issue.Message,
		})
	}
	return out
}

func linksOutputFrom(links []index.Link) []linkOutput {
	out := make([]linkOutput, 0, len(links))
	for _, link := range links {
		out = append(out, linkOutput{
			FromPath: link.FromPath,
			ToPath:   link.ToPath,
			Kind:     link.Kind,
			Field:    link.Field,
		})
	}
	return out
}
