package service

import (
	"context"
	"fmt"
	"strings"
	"time"
)

func (s *Service) Push(ctx context.Context, req PushRequest) (PushResponse, error) {
	if err := ctx.Err(); err != nil {
		return PushResponse{}, err
	}
	remote, branch := s.pushTarget(req)

	s.writerMu.Lock()
	defer s.writerMu.Unlock()
	s.pushExecMu.Lock()
	defer s.pushExecMu.Unlock()

	status, err := s.git.Status(branch)
	if err != nil {
		s.recordPushError(err, nil)
		return PushResponse{}, err
	}
	if !status.BranchOK {
		err := fmt.Errorf("vault is on branch %q, expected %q", status.Branch, status.ExpectedBranch)
		if status.Detached {
			err = fmt.Errorf("vault is on a detached HEAD, expected branch %q", status.ExpectedBranch)
		}
		s.recordPushError(err, nil)
		return PushResponse{}, err
	}

	ahead, err := s.git.CountAhead(remote, branch)
	if err != nil {
		s.recordPushError(err, nil)
		return PushResponse{}, err
	}
	if err := s.git.Push(remote, branch); err != nil {
		s.recordPushError(err, &ahead)
		return PushResponse{}, err
	}
	pushedAt := s.now().UTC().Format(time.RFC3339Nano)
	s.recordPushSuccess(pushedAt)
	return PushResponse{
		RemoteName:    remote,
		Branch:        branch,
		PushedCommits: ahead,
		PushedAt:      pushedAt,
	}, nil
}

func (s *Service) PushStatus(ctx context.Context) PushStatus {
	remote, branch := s.pushTarget(PushRequest{})
	s.pushMu.Lock()
	lastPushedAt := s.lastPushAt
	lastError := s.lastPushError
	ahead := s.commitsAhead
	s.pushMu.Unlock()

	if err := ctx.Err(); err != nil {
		lastError = err.Error()
	}
	return PushStatus{
		RemoteName:   remote,
		Branch:       branch,
		LastPushedAt: lastPushedAt,
		LastError:    lastError,
		CommitsAhead: ahead,
	}
}

func (s *Service) noteLocalCommit() {
	s.pushMu.Lock()
	s.commitsAhead++
	s.pushMu.Unlock()
}

func (s *Service) recordPushError(err error, commitsAhead *int) {
	s.pushMu.Lock()
	s.lastPushError = err.Error()
	if commitsAhead != nil {
		s.commitsAhead = *commitsAhead
	}
	s.pushMu.Unlock()
}

func (s *Service) recordPushSuccess(pushedAt string) {
	s.pushMu.Lock()
	s.lastPushAt = pushedAt
	s.lastPushError = ""
	s.commitsAhead = 0
	s.pushMu.Unlock()
}

func (s *Service) scheduleAutoPush() {
	switch strings.ToLower(strings.TrimSpace(s.cfg.Git.Push)) {
	case "on_change":
		go s.pushWithRetry(context.Background(), PushRequest{})
	case "debounced":
		interval := parseDebounceInterval(s.cfg.Git.DebounceInterval)
		s.pushMu.Lock()
		if s.pushTimer != nil {
			s.pushTimer.Stop()
		}
		s.pushTimer = time.AfterFunc(interval, func() {
			s.pushWithRetry(context.Background(), PushRequest{})
		})
		s.pushMu.Unlock()
	}
}

func (s *Service) pushWithRetry(ctx context.Context, req PushRequest) {
	delay := 250 * time.Millisecond
	for attempt := 0; attempt < 3; attempt++ {
		if _, err := s.Push(ctx, req); err == nil {
			return
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		delay *= 2
	}
}

func (s *Service) pushTarget(req PushRequest) (string, string) {
	remote := strings.TrimSpace(req.RemoteName)
	if remote == "" {
		remote = strings.TrimSpace(s.cfg.Git.RemoteName)
	}
	if remote == "" {
		remote = "origin"
	}
	branch := strings.TrimSpace(req.Branch)
	if branch == "" {
		branch = strings.TrimSpace(s.cfg.Git.Branch)
	}
	if branch == "" {
		branch = "main"
	}
	return remote, branch
}

func parseDebounceInterval(raw string) time.Duration {
	interval, err := time.ParseDuration(strings.TrimSpace(raw))
	if err != nil || interval <= 0 {
		return 5 * time.Minute
	}
	return interval
}
