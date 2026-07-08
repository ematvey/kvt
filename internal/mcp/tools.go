package mcp

import (
	"context"
	"encoding/json"

	"github.com/ematvey/kvt/internal/access"
	"github.com/ematvey/kvt/internal/config"
	"github.com/ematvey/kvt/internal/gitops"
	"github.com/ematvey/kvt/internal/index"
	"github.com/ematvey/kvt/internal/ontology"
	"github.com/ematvey/kvt/internal/responsebudget"
	"github.com/ematvey/kvt/internal/service"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type emptyInput struct{}

type accessInput struct {
	ReadGlobs  []string `json:"read_globs,omitempty" jsonschema:"read allow glob patterns"`
	WriteGlobs []string `json:"write_globs,omitempty" jsonschema:"write allow glob patterns"`
	DenyGlobs  []string `json:"deny_globs,omitempty" jsonschema:"deny glob patterns"`
}

type searchInput struct {
	Query      string       `json:"query" jsonschema:"search query"`
	PathPrefix string       `json:"path_prefix,omitempty" jsonschema:"optional bundle-relative path prefix"`
	Limit      int          `json:"limit,omitempty" jsonschema:"maximum number of results"`
	Cursor     string       `json:"cursor,omitempty" jsonschema:"pagination cursor for grep results"`
	Access     *accessInput `json:"access,omitempty" jsonschema:"optional request-scoped path access policy"`
}

type listInput struct {
	Type       string       `json:"type,omitempty" jsonschema:"optional concept type filter"`
	PathPrefix string       `json:"path_prefix,omitempty" jsonschema:"optional bundle-relative path prefix"`
	FieldKey   string       `json:"field_key,omitempty" jsonschema:"optional frontmatter field key filter"`
	FieldValue string       `json:"field_value,omitempty" jsonschema:"optional frontmatter field value filter"`
	Limit      int          `json:"limit,omitempty" jsonschema:"maximum number of documents"`
	Cursor     string       `json:"cursor,omitempty" jsonschema:"pagination cursor"`
	Access     *accessInput `json:"access,omitempty" jsonschema:"optional request-scoped path access policy"`
}

type pathInput struct {
	Path      string       `json:"path" jsonschema:"bundle-relative markdown path"`
	StartLine int          `json:"start_line,omitempty" jsonschema:"1-based inclusive first line to read"`
	EndLine   int          `json:"end_line,omitempty" jsonschema:"1-based inclusive last line to read"`
	Access    *accessInput `json:"access,omitempty" jsonschema:"optional request-scoped path access policy"`
}

type pageInput struct {
	Cursor string       `json:"cursor,omitempty" jsonschema:"pagination cursor"`
	Limit  int          `json:"limit,omitempty" jsonschema:"page size"`
	Access *accessInput `json:"access,omitempty" jsonschema:"optional request-scoped path access policy"`
}

type historyInput struct {
	Path   string       `json:"path" jsonschema:"bundle-relative markdown path"`
	Cursor string       `json:"cursor,omitempty" jsonschema:"pagination cursor"`
	Limit  int          `json:"limit,omitempty" jsonschema:"page size"`
	Access *accessInput `json:"access,omitempty" jsonschema:"optional request-scoped path access policy"`
}

type writeInput struct {
	Path           string       `json:"path" jsonschema:"bundle-relative markdown path"`
	Content        string       `json:"content" jsonschema:"complete markdown content"`
	BaseHash       string       `json:"base_hash,omitempty" jsonschema:"current content hash for conflict detection"`
	Agent          string       `json:"agent,omitempty" jsonschema:"agent name for commit body"`
	ValidationMode string       `json:"validation_mode,omitempty" jsonschema:"strict or advisory"`
	Access         *accessInput `json:"access,omitempty" jsonschema:"optional request-scoped path access policy"`
}

type editInput struct {
	Path           string       `json:"path" jsonschema:"bundle-relative markdown path"`
	BaseHash       string       `json:"base_hash,omitempty" jsonschema:"current content hash for conflict detection"`
	OldString      string       `json:"old_string" jsonschema:"exact string to replace"`
	NewString      string       `json:"new_string" jsonschema:"replacement string"`
	ReplaceAll     bool         `json:"replace_all,omitempty" jsonschema:"replace every match instead of requiring uniqueness"`
	Agent          string       `json:"agent,omitempty" jsonschema:"agent name for commit body"`
	ValidationMode string       `json:"validation_mode,omitempty" jsonschema:"strict or advisory"`
	Access         *accessInput `json:"access,omitempty" jsonschema:"optional request-scoped path access policy"`
}

type deleteInput struct {
	Path     string       `json:"path" jsonschema:"bundle-relative markdown path"`
	BaseHash string       `json:"base_hash,omitempty" jsonschema:"current content hash for conflict detection"`
	Agent    string       `json:"agent,omitempty" jsonschema:"agent name for commit body"`
	Access   *accessInput `json:"access,omitempty" jsonschema:"optional request-scoped path access policy"`
}

type validateInput struct {
	ValidationMode string `json:"validation_mode,omitempty" jsonschema:"strict or advisory"`
}

type summaryOutput struct {
	DocumentCount         int              `json:"document_count"`
	CountsByType          map[string]int   `json:"counts_by_type"`
	VecAvailable          bool             `json:"vec_available"`
	VecStatus             string           `json:"vec_status"`
	LastReconciledAt      string           `json:"last_reconciled_at"`
	EmbeddingPendingCount int              `json:"embedding_pending_count"`
	EmbeddingFailedCount  int              `json:"embedding_failed_count"`
	Push                  pushStatusOutput `json:"push"`
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
	Matches         []grepMatchOutput `json:"matches"`
	NextCursor      string            `json:"next_cursor"`
	Truncated       bool              `json:"truncated"`
	BudgetTruncated bool              `json:"budget_truncated"`
}

type grepMatchOutput struct {
	Path    string `json:"path"`
	Ordinal int    `json:"ordinal"`
	Snippet string `json:"snippet"`
	Text    string `json:"text"`
}

type listOutput struct {
	Documents       []documentSummaryOutput `json:"documents"`
	NextCursor      string                  `json:"next_cursor"`
	Truncated       bool                    `json:"truncated"`
	BudgetTruncated bool                    `json:"budget_truncated"`
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
	Path      string        `json:"path"`
	Content   string        `json:"content"`
	Hash      string        `json:"hash"`
	Backlinks []linkOutput  `json:"backlinks"`
	Warnings  []issueOutput `json:"warnings"`
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
	Count    int                       `json:"count"`
}

type fieldDefOutput struct {
	Enum    []string `json:"enum,omitempty"`
	Pattern string   `json:"pattern,omitempty"`
	Ref     string   `json:"ref,omitempty"`
}

type logOutput struct {
	Entries         []logEntryOutput `json:"entries"`
	NextCursor      string           `json:"next_cursor"`
	Truncated       bool             `json:"truncated"`
	BudgetTruncated bool             `json:"budget_truncated"`
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
	Entries         []historyEntryOutput `json:"entries"`
	NextCursor      string               `json:"next_cursor"`
	Truncated       bool                 `json:"truncated"`
	BudgetTruncated bool                 `json:"budget_truncated"`
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

type pushStatusOutput struct {
	RemoteName   string `json:"remote_name"`
	Branch       string `json:"branch"`
	LastPushedAt string `json:"last_pushed_at"`
	LastError    string `json:"last_error"`
	CommitsAhead int    `json:"commits_ahead"`
}

type issueOutput struct {
	Path    string `json:"path"`
	Field   string `json:"field"`
	Message string `json:"message"`
}

func registerTools(server *Server, svc *service.Service, cfg config.Config) {
	addTool(server, "kvt_summary", "Return vault health-oriented summary counts and embedding status.", func(ctx context.Context, _ emptyInput) (summaryOutput, error) {
		resp, err := svc.Summary(ctx, index.SummaryRequest{})
		if err != nil {
			return summaryOutput{}, err
		}
		push := svc.PushStatus(ctx)
		return summaryOutput{
			DocumentCount:         resp.DocumentCount,
			CountsByType:          resp.CountsByType,
			VecAvailable:          resp.VecAvailable,
			VecStatus:             resp.VecStatus,
			LastReconciledAt:      resp.LastReconciledAt,
			EmbeddingPendingCount: resp.EmbeddingPendingCount,
			EmbeddingFailedCount:  resp.EmbeddingFailedCount,
			Push:                  pushStatusOutputFrom(push),
		}, nil
	})
	addTool(server, "kvt_howto", "Return concise KVT workflow guidance for coding agents.", func(ctx context.Context, _ emptyInput) (howtoOutput, error) {
		text, err := howtoText(ctx, svc)
		return howtoOutput{Text: text}, err
	})
	addTool(server, "kvt_search", "Use first for semantic or keyword discovery across the vault.", func(ctx context.Context, in searchInput) (searchOutput, error) {
		policy, err := accessPolicyFromInput(in.Access)
		if err != nil {
			return searchOutput{}, err
		}
		resp, err := svc.Search(ctx, service.SearchRequest{Query: in.Query, PathPrefix: in.PathPrefix, Limit: in.Limit, Access: policy})
		return searchOutputFrom(resp), err
	})
	addTool(server, "kvt_grep", "Use for exact content lookup when you know text that should appear.", func(ctx context.Context, in searchInput) (grepOutput, error) {
		policy, err := accessPolicyFromInput(in.Access)
		if err != nil {
			return grepOutput{}, err
		}
		resp, err := svc.Grep(ctx, service.GrepRequest{Query: in.Query, PathPrefix: in.PathPrefix, Limit: in.Limit, Cursor: in.Cursor, Access: policy})
		if err != nil {
			return grepOutput{}, err
		}
		return budgetGrepOutput(in.Cursor, resp, cfg.Limits.MaxResponseChars)
	})
	addTool(server, "kvt_list", "List concepts by type, path prefix, or frontmatter field filters.", func(ctx context.Context, in listInput) (listOutput, error) {
		policy, err := accessPolicyFromInput(in.Access)
		if err != nil {
			return listOutput{}, err
		}
		resp, err := svc.List(ctx, service.ListRequest{
			Type:       in.Type,
			PathPrefix: in.PathPrefix,
			FieldKey:   in.FieldKey,
			FieldValue: in.FieldValue,
			Limit:      in.Limit,
			Cursor:     in.Cursor,
			Access:     policy,
		})
		if err != nil {
			return listOutput{}, err
		}
		return budgetListOutput(in.Cursor, resp, cfg.Limits.MaxResponseChars)
	})
	addTool(server, "kvt_read", "Read one concept and return current content, hash, and backlinks.", func(ctx context.Context, in pathInput) (readOutput, error) {
		policy, err := accessPolicyFromInput(in.Access)
		if err != nil {
			return readOutput{}, err
		}
		resp, err := svc.Read(ctx, service.ReadRequest{Path: in.Path, StartLine: in.StartLine, EndLine: in.EndLine, Access: policy})
		return readOutputFrom(resp), err
	})
	addTool(server, "kvt_types", "List ontology types and field constraints.", func(ctx context.Context, _ emptyInput) (typesOutput, error) {
		resp, err := svc.Types(ctx)
		return typesOutputFrom(resp), err
	})
	addTool(server, "kvt_log", "Return paginated git commit history for the vault.", func(ctx context.Context, in pageInput) (logOutput, error) {
		policy, err := accessPolicyFromInput(in.Access)
		if err != nil {
			return logOutput{}, err
		}
		resp, err := svc.Log(ctx, service.LogRequest{Cursor: in.Cursor, Limit: in.Limit, Access: policy})
		if err != nil {
			return logOutput{}, err
		}
		return budgetLogOutput(in.Cursor, resp, cfg.Limits.MaxResponseChars)
	})
	addTool(server, "kvt_history", "Return paginated commit history and diffs for one concept.", func(ctx context.Context, in historyInput) (historyOutput, error) {
		policy, err := accessPolicyFromInput(in.Access)
		if err != nil {
			return historyOutput{}, err
		}
		resp, err := svc.History(ctx, service.HistoryRequest{Path: in.Path, Cursor: in.Cursor, Limit: in.Limit, Access: policy})
		if err != nil {
			return historyOutput{}, err
		}
		return budgetHistoryOutput(in.Cursor, resp, cfg.Limits.MaxResponseChars)
	})
	addTool(server, "kvt_write", "Write complete concept content; use base_hash when updating an existing concept.", func(ctx context.Context, in writeInput) (writeOutput, error) {
		policy, err := accessPolicyFromInput(in.Access)
		if err != nil {
			return writeOutput{}, err
		}
		resp, err := svc.Write(ctx, service.WriteRequest{
			Path:           in.Path,
			Content:        in.Content,
			BaseHash:       in.BaseHash,
			Agent:          in.Agent,
			ValidationMode: validationMode(in.ValidationMode),
			Access:         policy,
		})
		return writeOutputFrom(resp), err
	})
	addTool(server, "kvt_edit", "Edit a concept by exact string replacement; read first and pass base_hash.", func(ctx context.Context, in editInput) (writeOutput, error) {
		policy, err := accessPolicyFromInput(in.Access)
		if err != nil {
			return writeOutput{}, err
		}
		resp, err := svc.Edit(ctx, service.EditRequest{
			Path:           in.Path,
			BaseHash:       in.BaseHash,
			OldString:      in.OldString,
			NewString:      in.NewString,
			ReplaceAll:     in.ReplaceAll,
			Agent:          in.Agent,
			ValidationMode: validationMode(in.ValidationMode),
			Access:         policy,
		})
		return writeOutputFrom(resp), err
	})
	addTool(server, "kvt_delete", "Delete a concept; pass base_hash to avoid stale deletes.", func(ctx context.Context, in deleteInput) (deleteOutput, error) {
		policy, err := accessPolicyFromInput(in.Access)
		if err != nil {
			return deleteOutput{}, err
		}
		resp, err := svc.Delete(ctx, service.DeleteRequest{Path: in.Path, BaseHash: in.BaseHash, Agent: in.Agent, Access: policy})
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

func accessPolicyFromInput(in *accessInput) (*access.Policy, error) {
	if in == nil {
		return nil, nil
	}
	return access.New(in.ReadGlobs, in.WriteGlobs, in.DenyGlobs)
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

func budgetGrepOutput(cursor string, resp index.GrepResponse, maxChars int) (grepOutput, error) {
	page, err := responsebudget.ApplyTextItems(resp.Matches, cursor, resp.NextCursor, maxChars,
		func(match index.GrepMatch) string {
			return match.Text
		},
		func(match index.GrepMatch, text string) index.GrepMatch {
			match.Text = text
			match.Snippet = text
			return match
		},
		func(matches []index.GrepMatch, next string, truncated bool, budgetTruncated bool) ([]byte, error) {
			return json.Marshal(grepOutputFromMatches(matches, next, truncated, budgetTruncated))
		},
	)
	if err != nil {
		return grepOutput{}, err
	}
	return grepOutputFromMatches(page.Items, page.NextCursor, page.Truncated, page.BudgetTruncated), nil
}

func grepOutputFromMatches(matches []index.GrepMatch, next string, truncated bool, budgetTruncated bool) grepOutput {
	out := grepOutput{NextCursor: next, Truncated: truncated, BudgetTruncated: budgetTruncated}
	for _, match := range matches {
		out.Matches = append(out.Matches, grepMatchOutput{
			Path:    match.Path,
			Ordinal: match.Ordinal,
			Snippet: match.Snippet,
			Text:    match.Text,
		})
	}
	return out
}

func budgetListOutput(cursor string, resp index.ListResponse, maxChars int) (listOutput, error) {
	page, err := responsebudget.ApplyItems(resp.Documents, cursor, resp.NextCursor, maxChars, func(documents []index.DocumentSummary, next string, truncated bool, budgetTruncated bool) ([]byte, error) {
		return json.Marshal(listOutputFromDocuments(documents, next, truncated, budgetTruncated))
	})
	if err != nil {
		return listOutput{}, err
	}
	return listOutputFromDocuments(page.Items, page.NextCursor, page.Truncated, page.BudgetTruncated), nil
}

func listOutputFromDocuments(documents []index.DocumentSummary, next string, truncated bool, budgetTruncated bool) listOutput {
	out := listOutput{NextCursor: next, Truncated: truncated, BudgetTruncated: budgetTruncated}
	for _, doc := range documents {
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
		Warnings:  issuesOutputFrom(resp.Warnings),
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
			Count:    typ.Count,
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

func budgetLogOutput(cursor string, resp gitops.LogPage, maxChars int) (logOutput, error) {
	page, err := responsebudget.ApplyItems(resp.Entries, cursor, resp.NextCursor, maxChars, func(entries []gitops.LogEntry, next string, truncated bool, budgetTruncated bool) ([]byte, error) {
		return json.Marshal(logOutputFromEntries(entries, next, truncated, budgetTruncated))
	})
	if err != nil {
		return logOutput{}, err
	}
	return logOutputFromEntries(page.Items, page.NextCursor, page.Truncated, page.BudgetTruncated), nil
}

func logOutputFromEntries(entries []gitops.LogEntry, next string, truncated bool, budgetTruncated bool) logOutput {
	out := logOutput{NextCursor: next, Truncated: truncated, BudgetTruncated: budgetTruncated}
	for _, entry := range entries {
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

func budgetHistoryOutput(cursor string, resp gitops.HistoryPage, maxChars int) (historyOutput, error) {
	page, err := responsebudget.ApplyTextItems(resp.Entries, cursor, resp.NextCursor, maxChars,
		func(entry gitops.HistoryEntry) string {
			return entry.Diff
		},
		func(entry gitops.HistoryEntry, text string) gitops.HistoryEntry {
			entry.Diff = text
			return entry
		},
		func(entries []gitops.HistoryEntry, next string, truncated bool, budgetTruncated bool) ([]byte, error) {
			return json.Marshal(historyOutputFromEntries(entries, next, truncated, budgetTruncated))
		},
	)
	if err != nil {
		return historyOutput{}, err
	}
	return historyOutputFromEntries(page.Items, page.NextCursor, page.Truncated, page.BudgetTruncated), nil
}

func historyOutputFromEntries(entries []gitops.HistoryEntry, next string, truncated bool, budgetTruncated bool) historyOutput {
	out := historyOutput{NextCursor: next, Truncated: truncated, BudgetTruncated: budgetTruncated}
	for _, entry := range entries {
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

func pushStatusOutputFrom(status service.PushStatus) pushStatusOutput {
	return pushStatusOutput{
		RemoteName:   status.RemoteName,
		Branch:       status.Branch,
		LastPushedAt: status.LastPushedAt,
		LastError:    status.LastError,
		CommitsAhead: status.CommitsAhead,
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
