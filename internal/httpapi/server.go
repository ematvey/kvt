package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/ematvey/kvt/internal/config"
	"github.com/ematvey/kvt/internal/index"
	"github.com/ematvey/kvt/internal/ontology"
	"github.com/ematvey/kvt/internal/service"
)

type Server struct {
	svc *service.Service
	cfg config.Config
	mux *http.ServeMux
}

func NewServer(svc *service.Service, cfg config.Config) http.Handler {
	server := &Server{
		svc: svc,
		cfg: cfg,
		mux: http.NewServeMux(),
	}
	server.routes()
	return server.auth(server.mux)
}

func (s *Server) routes() {
	s.mux.HandleFunc("/health", s.handleHealth)
	s.mux.HandleFunc("/summary", s.handleSummary)
	s.mux.HandleFunc("/search", s.handleSearch)
	s.mux.HandleFunc("/grep", s.handleGrep)
	s.mux.HandleFunc("/concepts", s.handleConcepts)
	s.mux.HandleFunc("/concepts/", s.handleConceptPath)
	s.mux.HandleFunc("/history/", s.handleHistory)
	s.mux.HandleFunc("/log", s.handleLog)
	s.mux.HandleFunc("/types", s.handleTypes)
	s.mux.HandleFunc("/validate", s.handleValidate)
	s.mux.HandleFunc("/push", s.handlePush)
}

func (s *Server) auth(next http.Handler) http.Handler {
	if len(s.cfg.Auth.APIKeys) == 0 {
		return next
	}
	allowed := map[string]struct{}{}
	for _, key := range s.cfg.Auth.APIKeys {
		key = strings.TrimSpace(key)
		if key != "" {
			allowed[key] = struct{}{}
		}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := strings.TrimSpace(r.Header.Get("Authorization"))
		token := strings.TrimSpace(strings.TrimPrefix(header, "Bearer "))
		if header == token || token == "" {
			writeError(w, http.StatusUnauthorized, "missing bearer token", nil)
			return
		}
		if _, ok := allowed[token]; !ok {
			writeError(w, http.StatusUnauthorized, "invalid bearer token", nil)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	resp, err := s.svc.Health(r.Context())
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      resp.OK,
		"git":     resp.Git,
		"summary": summaryPayload(resp.Summary),
	})
}

func (s *Server) handleSummary(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	resp, err := s.svc.Summary(r.Context(), index.SummaryRequest{})
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, summaryPayload(resp))
}

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	var req searchRequest
	if !decodeRequest(w, r, &req) {
		return
	}
	resp, err := s.svc.Search(r.Context(), service.SearchRequest{
		Query:      req.Query,
		PathPrefix: req.PathPrefix,
		Limit:      req.Limit,
	})
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"hits":     resp.Hits,
		"degraded": resp.Degraded,
	})
}

func (s *Server) handleGrep(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	var req searchRequest
	if !decodeRequest(w, r, &req) {
		return
	}
	resp, err := s.svc.Grep(r.Context(), index.GrepRequest{
		Query:      req.Query,
		PathPrefix: req.PathPrefix,
		Limit:      req.Limit,
	})
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"matches": resp.Matches})
}

func (s *Server) handleConcepts(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		resp, err := s.svc.List(r.Context(), index.ListRequest{
			Type:       r.URL.Query().Get("type"),
			PathPrefix: r.URL.Query().Get("path_prefix"),
			FieldKey:   r.URL.Query().Get("field_key"),
			FieldValue: r.URL.Query().Get("field_value"),
			Limit:      intQuery(r, "limit"),
		})
		if err != nil {
			writeServiceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"documents": resp.Documents})
	case http.MethodPost:
		var req writeRequest
		if !decodeRequest(w, r, &req) {
			return
		}
		resp, err := s.svc.Write(r.Context(), service.WriteRequest{
			Path:           req.Path,
			Content:        req.Content,
			BaseHash:       req.BaseHash,
			Agent:          req.Agent,
			ValidationMode: validationMode(req.ValidationMode),
		})
		if err != nil {
			writeServiceError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, writePayload(resp))
	default:
		writeMethodNotAllowed(w, http.MethodGet, http.MethodPost)
	}
}

func (s *Server) handleConceptPath(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/concepts/")
	switch r.Method {
	case http.MethodGet:
		resp, err := s.svc.Read(r.Context(), service.ReadRequest{Path: path})
		if err != nil {
			writeServiceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, readPayload(resp))
	case http.MethodPatch:
		var req editRequest
		if !decodeRequest(w, r, &req) {
			return
		}
		resp, err := s.svc.Edit(r.Context(), service.EditRequest{
			Path:           path,
			BaseHash:       req.BaseHash,
			OldString:      req.OldString,
			NewString:      req.NewString,
			ReplaceAll:     req.ReplaceAll,
			Agent:          req.Agent,
			ValidationMode: validationMode(req.ValidationMode),
		})
		if err != nil {
			writeServiceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, writePayload(resp))
	case http.MethodDelete:
		var req deleteRequest
		if !decodeRequest(w, r, &req) {
			return
		}
		resp, err := s.svc.Delete(r.Context(), service.DeleteRequest{
			Path:     path,
			BaseHash: req.BaseHash,
			Agent:    req.Agent,
		})
		if err != nil {
			writeServiceError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"path":          resp.Path,
			"changed_paths": resp.ChangedPaths,
			"commit":        resp.Commit,
		})
	default:
		writeMethodNotAllowed(w, http.MethodGet, http.MethodPatch, http.MethodDelete)
	}
}

func (s *Server) handleHistory(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/history/")
	resp, err := s.svc.History(r.Context(), service.HistoryRequest{
		Path:   path,
		Cursor: r.URL.Query().Get("cursor"),
		Limit:  intQuery(r, "limit"),
	})
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"entries":     resp.Entries,
		"next_cursor": resp.NextCursor,
	})
}

func (s *Server) handleLog(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	resp, err := s.svc.Log(r.Context(), service.LogRequest{
		Cursor: r.URL.Query().Get("cursor"),
		Limit:  intQuery(r, "limit"),
	})
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"entries":     resp.Entries,
		"next_cursor": resp.NextCursor,
	})
}

func (s *Server) handleTypes(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodGet) {
		return
	}
	resp, err := s.svc.Types(r.Context())
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"types": resp.Types})
}

func (s *Server) handleValidate(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	var req validateRequest
	if !decodeRequest(w, r, &req) {
		return
	}
	resp, err := s.svc.Validate(r.Context(), service.ValidateRequest{
		ValidationMode: validationMode(req.ValidationMode),
	})
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"errors":   issuesPayload(resp.Errors),
		"warnings": issuesPayload(resp.Warnings),
	})
}

func (s *Server) handlePush(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	writeError(w, http.StatusNotImplemented, "push is implemented in the operations task", nil)
}

type writeRequest struct {
	Path           string `json:"path"`
	Content        string `json:"content"`
	BaseHash       string `json:"base_hash"`
	Agent          string `json:"agent"`
	ValidationMode string `json:"validation_mode"`
}

type editRequest struct {
	BaseHash       string `json:"base_hash"`
	OldString      string `json:"old_string"`
	NewString      string `json:"new_string"`
	ReplaceAll     bool   `json:"replace_all"`
	Agent          string `json:"agent"`
	ValidationMode string `json:"validation_mode"`
}

type deleteRequest struct {
	BaseHash string `json:"base_hash"`
	Agent    string `json:"agent"`
}

type searchRequest struct {
	Query      string `json:"query"`
	PathPrefix string `json:"path_prefix"`
	Limit      int    `json:"limit"`
}

type validateRequest struct {
	ValidationMode string `json:"validation_mode"`
}

func validationMode(raw string) service.ValidationMode {
	if strings.EqualFold(strings.TrimSpace(raw), string(service.ValidationModeAdvisory)) {
		return service.ValidationModeAdvisory
	}
	return service.ValidationModeStrict
}

func decodeRequest(w http.ResponseWriter, r *http.Request, out any) bool {
	if r.Body == nil {
		return true
	}
	defer r.Body.Close()
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(out); err != nil {
		if errors.Is(err, io.EOF) {
			return true
		}
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid JSON: %v", err), nil)
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeServiceError(w http.ResponseWriter, err error) {
	var conflict *service.ConflictError
	if errors.As(err, &conflict) {
		writeError(w, http.StatusConflict, err.Error(), map[string]any{
			"path":            conflict.Path,
			"base_hash":       conflict.BaseHash,
			"current_hash":    conflict.CurrentHash,
			"current_content": conflict.CurrentContent,
		})
		return
	}
	var validation *service.ValidationError
	if errors.As(err, &validation) {
		writeError(w, http.StatusUnprocessableEntity, err.Error(), map[string]any{
			"errors":   issuesPayload(validation.Errors),
			"warnings": issuesPayload(validation.Warnings),
		})
		return
	}
	if service.IsAmbiguousEdit(err) {
		writeError(w, http.StatusConflict, err.Error(), nil)
		return
	}
	if service.IsEditTargetNotFound(err) || os.IsNotExist(err) {
		writeError(w, http.StatusNotFound, err.Error(), nil)
		return
	}
	writeError(w, httpStatusForError(err), err.Error(), nil)
}

func httpStatusForError(err error) int {
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "invalid path"),
		strings.Contains(msg, "must point to a markdown concept"),
		strings.Contains(msg, "service-owned"),
		strings.Contains(msg, "is required"),
		strings.Contains(msg, "no searchable terms"),
		strings.Contains(msg, "invalid cursor"):
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}

func writeError(w http.ResponseWriter, status int, message string, extra map[string]any) {
	payload := map[string]any{"error": message}
	for key, value := range extra {
		payload[key] = value
	}
	writeJSON(w, status, payload)
}

func writeMethodNotAllowed(w http.ResponseWriter, methods ...string) {
	w.Header().Set("Allow", strings.Join(methods, ", "))
	writeError(w, http.StatusMethodNotAllowed, "method not allowed", nil)
}

func requireMethod(w http.ResponseWriter, r *http.Request, method string) bool {
	if r.Method == method {
		return true
	}
	writeMethodNotAllowed(w, method)
	return false
}

func intQuery(r *http.Request, key string) int {
	value := strings.TrimSpace(r.URL.Query().Get(key))
	if value == "" {
		return 0
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0
	}
	return parsed
}

func summaryPayload(resp index.SummaryResponse) map[string]any {
	return map[string]any{
		"document_count":          resp.DocumentCount,
		"counts_by_type":          resp.CountsByType,
		"vec_available":           resp.VecAvailable,
		"vec_status":              resp.VecStatus,
		"last_reconciled_at":      resp.LastReconciledAt,
		"embedding_pending_count": resp.EmbeddingPendingCount,
		"embedding_failed_count":  resp.EmbeddingFailedCount,
	}
}

func readPayload(resp service.ReadResponse) map[string]any {
	return map[string]any{
		"path":      resp.Path,
		"content":   resp.Content,
		"hash":      resp.Hash,
		"document":  resp.Document,
		"backlinks": resp.Backlinks,
	}
}

func writePayload(resp service.WriteResponse) map[string]any {
	return map[string]any{
		"path":          resp.Path,
		"content":       resp.Content,
		"hash":          resp.Hash,
		"timestamp":     resp.Timestamp,
		"document":      resp.Document,
		"warnings":      issuesPayload(resp.Warnings),
		"changed_paths": resp.ChangedPaths,
		"commit":        resp.Commit,
	}
}

func issuesPayload(issues []ontology.Issue) []map[string]any {
	out := make([]map[string]any, 0, len(issues))
	for _, issue := range issues {
		out = append(out, map[string]any{
			"path":    issue.Path.String(),
			"field":   issue.Field,
			"message": issue.Message,
		})
	}
	return out
}
