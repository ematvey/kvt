package service

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ematvey/kvt/internal/config"
	"github.com/ematvey/kvt/internal/frontmatter"
	"github.com/ematvey/kvt/internal/gitops"
	"github.com/ematvey/kvt/internal/ontology"
	"github.com/ematvey/kvt/internal/pathutil"
)

type Deps struct {
	Now func() time.Time
}

type Service struct {
	root          string
	cfg           config.Config
	git           gitops.Client
	now           func() time.Time
	writerMu      sync.Mutex
	lastTimestamp time.Time
}

type conceptState struct {
	path    pathutil.Path
	content []byte
	hash    string
}

type preparedDocument struct {
	document  frontmatter.Document
	content   []byte
	hash      string
	timestamp string
	warnings  []ontology.Issue
}

func New(root string, cfg config.Config, deps Deps) (*Service, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, fmt.Errorf("vault root is required")
	}
	now := deps.Now
	if now == nil {
		now = time.Now
	}
	return &Service{
		root: root,
		cfg:  cfg,
		git:  gitops.New(root),
		now:  now,
	}, nil
}

func normalizeConceptPath(raw string) (pathutil.Path, error) {
	normalized, err := pathutil.Normalize(strings.TrimSpace(raw))
	if err != nil {
		return "", err
	}
	if filepath.Ext(normalized.String()) != ".md" {
		return "", fmt.Errorf("path %q must point to a markdown concept", normalized.String())
	}
	if path.Base(normalized.String()) == "index.md" {
		return "", fmt.Errorf("path %q is service-owned", normalized.String())
	}
	return normalized, nil
}

func (s *Service) fullPath(docPath pathutil.Path) string {
	return filepath.Join(s.root, filepath.FromSlash(docPath.String()))
}

func (s *Service) readState(docPath pathutil.Path) (conceptState, error) {
	content, err := os.ReadFile(s.fullPath(docPath))
	if err != nil {
		return conceptState{}, err
	}
	return conceptState{
		path:    docPath,
		content: content,
		hash:    frontmatter.Hash(content),
	}, nil
}

func (s *Service) commitMutation(message string, agent string, paths []string) (CommitInfo, error) {
	body := ""
	if strings.TrimSpace(agent) != "" {
		body = "Agent: " + strings.TrimSpace(agent)
	}
	result, err := s.git.Commit(gitops.CommitOptions{
		Message:     message,
		Body:        body,
		Paths:       appendUniquePaths(nil, paths...),
		AuthorName:  s.cfg.Git.AuthorName,
		AuthorEmail: s.cfg.Git.AuthorEmail,
	})
	if err != nil {
		return CommitInfo{}, err
	}
	if !result.Changed {
		return CommitInfo{}, fmt.Errorf("mutation produced no git commit")
	}
	return CommitInfo{
		Hash:      result.Hash,
		ShortHash: result.ShortHash,
	}, nil
}

func checkBaseHash(docPath pathutil.Path, baseHash string, current conceptState, err error) error {
	if strings.TrimSpace(baseHash) == "" {
		return nil
	}
	baseHash = strings.TrimSpace(baseHash)
	if err != nil {
		if os.IsNotExist(err) {
			return &ConflictError{
				Path:           docPath.String(),
				BaseHash:       baseHash,
				CurrentHash:    "",
				CurrentContent: "",
			}
		}
		return err
	}
	if baseHash == current.hash {
		return nil
	}
	return &ConflictError{
		Path:           docPath.String(),
		BaseHash:       baseHash,
		CurrentHash:    current.hash,
		CurrentContent: string(current.content),
	}
}

func (s *Service) prepareDocument(docPath pathutil.Path, rawContent string) (preparedDocument, error) {
	doc, err := frontmatter.Parse([]byte(rawContent))
	if err != nil {
		return preparedDocument{}, err
	}
	doc = frontmatter.WithTimestamp(doc, s.nextTimestamp())

	schema, err := ontology.Load(s.root)
	if err != nil {
		return preparedDocument{}, err
	}
	validation := ontology.ValidateDocument(schema, docPath, doc, ontology.Strict)
	if len(validation.Errors) > 0 {
		return preparedDocument{}, &ValidationError{
			Path:     docPath.String(),
			Errors:   validation.Errors,
			Warnings: validation.Warnings,
		}
	}

	rendered, err := frontmatter.Render(doc)
	if err != nil {
		return preparedDocument{}, err
	}
	timestamp, _ := doc.Fields["timestamp"].(string)
	return preparedDocument{
		document:  doc,
		content:   rendered,
		hash:      frontmatter.Hash(rendered),
		timestamp: timestamp,
		warnings:  validation.Warnings,
	}, nil
}

func (s *Service) nextTimestamp() time.Time {
	now := s.now().UTC()
	if !s.lastTimestamp.IsZero() && !now.After(s.lastTimestamp) {
		now = s.lastTimestamp.Add(time.Nanosecond)
	}
	s.lastTimestamp = now
	return now
}
