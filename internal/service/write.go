package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func (s *Service) Write(ctx context.Context, req WriteRequest) (WriteResponse, error) {
	if err := ctx.Err(); err != nil {
		return WriteResponse{}, err
	}
	docPath, err := normalizeConceptPath(req.Path)
	if err != nil {
		return WriteResponse{}, err
	}

	s.writerMu.Lock()
	defer s.writerMu.Unlock()

	current, currentErr := s.readState(docPath)
	if currentErr != nil && !os.IsNotExist(currentErr) {
		return WriteResponse{}, currentErr
	}
	if err := checkBaseHash(docPath, req.BaseHash, current, currentErr); err != nil {
		return WriteResponse{}, err
	}

	prepared, err := s.prepareDocument(docPath, req.Content, req.ValidationMode, timestampFromState(current, currentErr))
	if err != nil {
		return WriteResponse{}, err
	}
	if err := os.MkdirAll(filepath.Dir(s.fullPath(docPath)), 0o755); err != nil {
		return WriteResponse{}, err
	}
	if err := os.WriteFile(s.fullPath(docPath), prepared.content, 0o644); err != nil {
		return WriteResponse{}, err
	}

	changedPaths := []string{docPath.String()}
	indexPaths, err := regenerateIndexes(s.root, docPath)
	if err != nil {
		return WriteResponse{}, err
	}
	changedPaths = appendUniquePaths(changedPaths, indexPaths...)
	if err := s.index.ApplyDocument(ctx, prepared.indexed); err != nil {
		return WriteResponse{}, err
	}
	s.enqueueEmbedding(prepared)

	commit, err := s.commitMutation(fmt.Sprintf("Write %s", docPath.String()), req.Agent, changedPaths)
	if err != nil {
		return WriteResponse{}, err
	}

	return WriteResponse{
		Path:         docPath.String(),
		Content:      string(prepared.content),
		Hash:         prepared.hash,
		Timestamp:    prepared.timestamp,
		Document:     prepared.document,
		Warnings:     prepared.warnings,
		ChangedPaths: changedPaths,
		Commit:       commit,
	}, nil
}

func (s *Service) Edit(ctx context.Context, req EditRequest) (WriteResponse, error) {
	if err := ctx.Err(); err != nil {
		return WriteResponse{}, err
	}
	docPath, err := normalizeConceptPath(req.Path)
	if err != nil {
		return WriteResponse{}, err
	}
	if req.OldString == "" {
		return WriteResponse{}, fmt.Errorf("old string is required")
	}

	s.writerMu.Lock()
	defer s.writerMu.Unlock()

	current, err := s.readState(docPath)
	if err != nil {
		return WriteResponse{}, err
	}
	if err := checkBaseHash(docPath, req.BaseHash, current, nil); err != nil {
		return WriteResponse{}, err
	}

	matches := strings.Count(string(current.content), req.OldString)
	switch {
	case matches == 0:
		return WriteResponse{}, &EditTargetNotFoundError{
			Path:      docPath.String(),
			OldString: req.OldString,
		}
	case matches > 1 && !req.ReplaceAll:
		return WriteResponse{}, &AmbiguousEditError{
			Path:       docPath.String(),
			OldString:  req.OldString,
			MatchCount: matches,
		}
	}

	replacements := 1
	if req.ReplaceAll {
		replacements = -1
	}
	updatedContent := strings.Replace(string(current.content), req.OldString, req.NewString, replacements)

	prepared, err := s.prepareDocument(docPath, updatedContent, req.ValidationMode, timestampFromState(current, nil))
	if err != nil {
		return WriteResponse{}, err
	}
	if err := os.WriteFile(s.fullPath(docPath), prepared.content, 0o644); err != nil {
		return WriteResponse{}, err
	}

	changedPaths := []string{docPath.String()}
	indexPaths, err := regenerateIndexes(s.root, docPath)
	if err != nil {
		return WriteResponse{}, err
	}
	changedPaths = appendUniquePaths(changedPaths, indexPaths...)
	if err := s.index.ApplyDocument(ctx, prepared.indexed); err != nil {
		return WriteResponse{}, err
	}
	s.enqueueEmbedding(prepared)

	commit, err := s.commitMutation(fmt.Sprintf("Edit %s", docPath.String()), req.Agent, changedPaths)
	if err != nil {
		return WriteResponse{}, err
	}

	return WriteResponse{
		Path:         docPath.String(),
		Content:      string(prepared.content),
		Hash:         prepared.hash,
		Timestamp:    prepared.timestamp,
		Document:     prepared.document,
		Warnings:     prepared.warnings,
		ChangedPaths: changedPaths,
		Commit:       commit,
	}, nil
}

func (s *Service) Delete(ctx context.Context, req DeleteRequest) (DeleteResponse, error) {
	if err := ctx.Err(); err != nil {
		return DeleteResponse{}, err
	}
	docPath, err := normalizeConceptPath(req.Path)
	if err != nil {
		return DeleteResponse{}, err
	}

	s.writerMu.Lock()
	defer s.writerMu.Unlock()

	current, err := s.readState(docPath)
	if err != nil {
		return DeleteResponse{}, err
	}
	if err := checkBaseHash(docPath, req.BaseHash, current, nil); err != nil {
		return DeleteResponse{}, err
	}
	if err := os.Remove(s.fullPath(docPath)); err != nil {
		return DeleteResponse{}, err
	}

	changedPaths := []string{docPath.String()}
	indexPaths, err := regenerateIndexes(s.root, docPath)
	if err != nil {
		return DeleteResponse{}, err
	}
	changedPaths = appendUniquePaths(changedPaths, indexPaths...)
	if err := s.index.RemoveDocument(ctx, docPath.String()); err != nil {
		return DeleteResponse{}, err
	}

	commit, err := s.commitMutation(fmt.Sprintf("Delete %s", docPath.String()), req.Agent, changedPaths)
	if err != nil {
		return DeleteResponse{}, err
	}

	return DeleteResponse{
		Path:         docPath.String(),
		ChangedPaths: changedPaths,
		Commit:       commit,
	}, nil
}
