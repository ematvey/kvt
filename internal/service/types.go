package service

import (
	"errors"
	"fmt"
	"strings"

	"github.com/ematvey/kvt/internal/access"
	"github.com/ematvey/kvt/internal/frontmatter"
	"github.com/ematvey/kvt/internal/index"
	"github.com/ematvey/kvt/internal/ontology"
)

type ReadRequest struct {
	Path      string
	StartLine int
	EndLine   int
	Access    *access.Policy
}

type ReadResponse struct {
	Path      string
	Content   string
	Hash      string
	Document  frontmatter.Document
	Backlinks []index.Link
	Warnings  []ontology.Issue
}

type WriteRequest struct {
	Path           string
	Content        string
	BaseHash       string
	Agent          string
	AgentID        string
	Summary        string
	ValidationMode ValidationMode
	Access         *access.Policy
}

type EditRequest struct {
	Path           string
	BaseHash       string
	OldString      string
	NewString      string
	ReplaceAll     bool
	Agent          string
	AgentID        string
	Summary        string
	ValidationMode ValidationMode
	Access         *access.Policy
}

type DeleteRequest struct {
	Path     string
	BaseHash string
	Agent    string
	AgentID  string
	Access   *access.Policy
}

type ValidateRequest struct {
	ValidationMode ValidationMode
}

type ValidationMode string

const (
	ValidationModeStrict   ValidationMode = "strict"
	ValidationModeAdvisory ValidationMode = "advisory"
)

func (m ValidationMode) ontologyMode() ontology.Mode {
	if m == ValidationModeAdvisory {
		return ontology.Advisory
	}
	return ontology.Strict
}

type CommitInfo struct {
	Hash      string
	ShortHash string
}

type WriteResponse struct {
	Path         string
	Content      string
	Hash         string
	Timestamp    string
	Document     frontmatter.Document
	Warnings     []ontology.Issue
	ChangedPaths []string
	Commit       CommitInfo
}

type DeleteResponse struct {
	Path         string
	ChangedPaths []string
	Commit       CommitInfo
}

type PushRequest struct {
	RemoteName string
	Branch     string
}

type PushResponse struct {
	RemoteName    string
	Branch        string
	PushedCommits int
	PushedAt      string
}

type PushStatus struct {
	RemoteName   string
	Branch       string
	LastPushedAt string
	LastError    string
	CommitsAhead int
}

type SearchRequest struct {
	Query      string
	PathPrefix string
	Limit      int
	Access     *access.Policy
}

type ListRequest struct {
	Type       string
	PathPrefix string
	FieldKey   string
	FieldValue string
	Limit      int
	Cursor     string
	Access     *access.Policy
}

type GrepRequest struct {
	Query      string
	PathPrefix string
	Limit      int
	Cursor     string
	Access     *access.Policy
}

type SearchHit struct {
	Path    string
	Title   string
	Type    string
	Snippet string
	Score   float64
}

type SearchResponse struct {
	Hits     []SearchHit
	Degraded []string
}

type ValidateResponse struct {
	Errors   []ontology.Issue
	Warnings []ontology.Issue
}

type ConflictError struct {
	Path           string
	BaseHash       string
	CurrentHash    string
	CurrentContent string
}

func (e *ConflictError) Error() string {
	if e == nil {
		return "<nil>"
	}
	return fmt.Sprintf("stale base hash for %s: have %q want %q", e.Path, e.BaseHash, e.CurrentHash)
}

func IsConflict(err error) bool {
	var target *ConflictError
	return errors.As(err, &target)
}

type AmbiguousEditError struct {
	Path       string
	OldString  string
	MatchCount int
}

func (e *AmbiguousEditError) Error() string {
	if e == nil {
		return "<nil>"
	}
	return fmt.Sprintf("edit target %q is ambiguous in %s (%d matches)", e.OldString, e.Path, e.MatchCount)
}

func IsAmbiguousEdit(err error) bool {
	var target *AmbiguousEditError
	return errors.As(err, &target)
}

type EditTargetNotFoundError struct {
	Path      string
	OldString string
}

func (e *EditTargetNotFoundError) Error() string {
	if e == nil {
		return "<nil>"
	}
	return fmt.Sprintf("edit target %q not found in %s", e.OldString, e.Path)
}

func IsEditTargetNotFound(err error) bool {
	var target *EditTargetNotFoundError
	return errors.As(err, &target)
}

type ValidationError struct {
	Path     string
	Errors   []ontology.Issue
	Warnings []ontology.Issue
}

func (e *ValidationError) Error() string {
	if e == nil {
		return "<nil>"
	}
	if len(e.Errors) == 0 {
		return "validation failed"
	}
	first := e.Errors[0]
	location := e.Path
	if first.Path.String() != "" {
		location = first.Path.String()
	}
	return fmt.Sprintf("validation failed for %s: %s", location, strings.TrimSpace(first.Message))
}
