package gitops

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

type Client struct {
	root string
}

type WorktreeStatus struct {
	Branch         string
	ExpectedBranch string
	BranchOK       bool
	Detached       bool
	Dirty          bool
	Head           string
}

type CommitOptions struct {
	Message     string
	Body        string
	Paths       []string
	AuthorName  string
	AuthorEmail string
}

type CommitResult struct {
	Hash      string
	ShortHash string
	Changed   bool
}

type LogEntry struct {
	Hash        string
	ShortHash   string
	Timestamp   string
	Author      string
	Subject     string
	Files       []string
	FileSummary string
}

type LogPage struct {
	Entries    []LogEntry
	NextCursor string
}

type HistoryEntry struct {
	Hash      string
	ShortHash string
	Timestamp string
	Author    string
	Subject   string
	Diff      string
}

type HistoryPage struct {
	Entries    []HistoryEntry
	NextCursor string
}

func New(root string) Client {
	return Client{root: root}
}

func Status(root string, branch string) (WorktreeStatus, error) {
	return New(root).Status(branch)
}

func Commit(root string, opts CommitOptions) (CommitResult, error) {
	return New(root).Commit(opts)
}

func Log(root string, cursor string, limit int) (LogPage, error) {
	return New(root).Log(cursor, limit)
}

func History(root string, path string, cursor string, limit int) (HistoryPage, error) {
	return New(root).History(path, cursor, limit)
}

func Push(root string, remote string, branch string) error {
	return New(root).Push(remote, branch)
}

func (c Client) Status(branch string) (WorktreeStatus, error) {
	status := WorktreeStatus{ExpectedBranch: branch}
	currentBranch, err := c.currentBranch()
	if err != nil {
		var detached detachedHeadError
		if !asDetached(err, &detached) {
			return WorktreeStatus{}, err
		}
		status.Detached = true
	} else {
		status.Branch = currentBranch
	}
	dirty, err := c.isDirty()
	if err != nil {
		return WorktreeStatus{}, err
	}
	status.Dirty = dirty
	head, err := c.head()
	if err == nil {
		status.Head = head
	}
	status.BranchOK = !status.Detached && (branch == "" || status.Branch == branch)
	return status, nil
}

func (c Client) Commit(opts CommitOptions) (CommitResult, error) {
	if strings.TrimSpace(opts.Message) == "" {
		return CommitResult{}, fmt.Errorf("commit message is required")
	}
	paths := opts.Paths
	scoped := len(paths) > 0
	if len(paths) > 0 {
		paths = filterRuntimePaths(paths)
		if len(paths) == 0 {
			hash, _ := c.head()
			return CommitResult{
				Hash:      hash,
				ShortHash: shortHash(hash),
				Changed:   false,
			}, nil
		}
	}
	if err := c.add(paths); err != nil {
		return CommitResult{}, err
	}
	diff, err := c.diffCached(paths)
	if err != nil {
		return CommitResult{}, err
	}
	if strings.TrimSpace(diff) == "" {
		hash, _ := c.head()
		return CommitResult{
			Hash:      hash,
			ShortHash: shortHash(hash),
			Changed:   false,
		}, nil
	}

	args := []string{"commit", "-m", opts.Message}
	if strings.TrimSpace(opts.Body) != "" {
		args = append(args, "-m", opts.Body)
	}
	if scoped {
		args = append(args, "--")
		args = append(args, scopedPathspec(paths)...)
	}
	env := authorEnv(opts)
	if _, err := c.run(context.Background(), env, args...); err != nil {
		return CommitResult{}, err
	}

	hash, err := c.head()
	if err != nil {
		return CommitResult{}, err
	}
	return CommitResult{
		Hash:      hash,
		ShortHash: shortHash(hash),
		Changed:   true,
	}, nil
}

func (c Client) Log(cursor string, limit int) (LogPage, error) {
	offset, err := parseCursor(cursor)
	if err != nil {
		return LogPage{}, err
	}
	limit = normalizeLimit(limit)

	out, err := c.run(context.Background(), nil,
		"log",
		"--format=%x1e%H%x1f%h%x1f%cI%x1f%an <%ae>%x1f%s",
		"--name-only",
		"--no-renames",
		"-n", strconv.Itoa(limit+1),
		"--skip", strconv.Itoa(offset),
	)
	if err != nil {
		return LogPage{}, err
	}

	records := parseRecords(out)
	page := LogPage{Entries: make([]LogEntry, 0, min(limit, len(records)))}
	for i, record := range records {
		if i == limit {
			page.NextCursor = strconv.Itoa(offset + limit)
			break
		}
		lines := strings.Split(record, "\n")
		meta := strings.Split(lines[0], "\x1f")
		files := collectFiles(lines[1:])
		entry := LogEntry{
			Hash:        metaValue(meta, 0),
			ShortHash:   metaValue(meta, 1),
			Timestamp:   metaValue(meta, 2),
			Author:      metaValue(meta, 3),
			Subject:     metaValue(meta, 4),
			Files:       files,
			FileSummary: summarizeFiles(files),
		}
		page.Entries = append(page.Entries, entry)
	}
	return page, nil
}

func (c Client) History(path string, cursor string, limit int) (HistoryPage, error) {
	offset, err := parseCursor(cursor)
	if err != nil {
		return HistoryPage{}, err
	}
	limit = normalizeLimit(limit)

	hashesOut, err := c.run(context.Background(), nil,
		"log",
		"--format=%H",
		"-n", strconv.Itoa(limit+1),
		"--skip", strconv.Itoa(offset),
		"--", path,
	)
	if err != nil {
		return HistoryPage{}, err
	}
	hashes := filterNonEmpty(strings.Split(strings.TrimSpace(hashesOut), "\n"))
	page := HistoryPage{Entries: make([]HistoryEntry, 0, min(limit, len(hashes)))}
	for i, hash := range hashes {
		if i == limit {
			page.NextCursor = strconv.Itoa(offset + limit)
			break
		}
		entry, err := c.showPathCommit(hash, path)
		if err != nil {
			return HistoryPage{}, err
		}
		page.Entries = append(page.Entries, entry)
	}
	return page, nil
}

func (c Client) Push(remote string, branch string) error {
	_, err := c.run(context.Background(), nil, "push", remote, branch)
	return err
}

func (c Client) currentBranch() (string, error) {
	cmd := exec.CommandContext(context.Background(), "git", "symbolic-ref", "--quiet", "--short", "HEAD")
	cmd.Dir = c.root
	out, err := cmd.CombinedOutput()
	if err != nil {
		var exitErr *exec.ExitError
		if exit, ok := err.(*exec.ExitError); ok {
			exitErr = exit
		}
		if exitErr != nil && exitErr.ExitCode() == 1 {
			return "", detachedHeadError{err: fmt.Errorf("git symbolic-ref --quiet --short HEAD: detached HEAD")}
		}
		return "", fmt.Errorf("git symbolic-ref --quiet --short HEAD: %s", strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

func (c Client) head() (string, error) {
	out, err := c.run(context.Background(), nil, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

func (c Client) isDirty() (bool, error) {
	out, err := c.run(context.Background(), nil, "status", "--porcelain")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) != "", nil
}

func (c Client) add(paths []string) error {
	if len(paths) == 0 {
		_, err := c.run(context.Background(), nil, "add", "--all")
		return err
	}

	paths = filterRuntimePaths(paths)
	if len(paths) == 0 {
		return nil
	}

	args := []string{"add", "--all", "--force", "--"}
	args = append(args, scopedPathspec(paths)...)
	_, err := c.run(context.Background(), nil, args...)
	return err
}

func filterRuntimePaths(paths []string) []string {
	kept := make([]string, 0, len(paths))
	for _, path := range paths {
		if isRuntimePath(path) {
			continue
		}
		kept = append(kept, path)
	}
	return kept
}

func isRuntimePath(path string) bool {
	path = strings.TrimPrefix(strings.ReplaceAll(path, "\\", "/"), "./")
	return path == ".kvt" || strings.HasPrefix(path, ".kvt/")
}

func scopedPathspec(paths []string) []string {
	pathspec := append([]string{}, paths...)
	return append(pathspec, ":(exclude).kvt", ":(exclude).kvt/**")
}

func (c Client) diffCached(paths []string) (string, error) {
	args := []string{"diff", "--cached", "--name-only"}
	if len(paths) > 0 {
		args = append(args, "--")
		args = append(args, scopedPathspec(paths)...)
	}
	return c.run(context.Background(), nil, args...)
}

func (c Client) showPathCommit(hash string, path string) (HistoryEntry, error) {
	out, err := c.run(context.Background(), nil,
		"show",
		"--format=%H%x1f%h%x1f%cI%x1f%an <%ae>%x1f%s",
		"--unified=3",
		hash,
		"--", path,
	)
	if err != nil {
		return HistoryEntry{}, err
	}
	lines := strings.Split(out, "\n")
	meta := strings.Split(lines[0], "\x1f")
	diff := strings.TrimLeft(strings.Join(lines[1:], "\n"), "\n")
	return HistoryEntry{
		Hash:      metaValue(meta, 0),
		ShortHash: metaValue(meta, 1),
		Timestamp: metaValue(meta, 2),
		Author:    metaValue(meta, 3),
		Subject:   metaValue(meta, 4),
		Diff:      diff,
	}, nil
}

func (c Client) run(ctx context.Context, extraEnv []string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = c.root
	cmd.Env = append(os.Environ(), extraEnv...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = strings.TrimSpace(stdout.String())
		}
		if message == "" {
			message = err.Error()
		}
		return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), message)
	}
	return stdout.String(), nil
}

func authorEnv(opts CommitOptions) []string {
	env := []string{}
	if opts.AuthorName != "" {
		env = append(env, "GIT_AUTHOR_NAME="+opts.AuthorName, "GIT_COMMITTER_NAME="+opts.AuthorName)
	}
	if opts.AuthorEmail != "" {
		env = append(env, "GIT_AUTHOR_EMAIL="+opts.AuthorEmail, "GIT_COMMITTER_EMAIL="+opts.AuthorEmail)
	}
	return env
}

func parseCursor(cursor string) (int, error) {
	if cursor == "" {
		return 0, nil
	}
	offset, err := strconv.Atoi(cursor)
	if err != nil || offset < 0 {
		return 0, fmt.Errorf("invalid cursor %q", cursor)
	}
	return offset, nil
}

func normalizeLimit(limit int) int {
	if limit <= 0 {
		return 20
	}
	return limit
}

func parseRecords(out string) []string {
	parts := strings.Split(out, "\x1e")
	records := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		records = append(records, part)
	}
	return records
}

func collectFiles(lines []string) []string {
	files := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		files = append(files, line)
	}
	return files
}

func summarizeFiles(files []string) string {
	switch len(files) {
	case 0:
		return ""
	case 1:
		return "1 file: " + files[0]
	case 2:
		return "2 files: " + files[0] + ", " + files[1]
	default:
		return fmt.Sprintf("%d files: %s, ... and %d more", len(files), files[0], len(files)-1)
	}
}

func shortHash(hash string) string {
	if len(hash) <= 7 {
		return hash
	}
	return hash[:7]
}

func filterNonEmpty(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		result = append(result, value)
	}
	return result
}

func metaValue(values []string, index int) string {
	if index >= len(values) {
		return ""
	}
	return values[index]
}

func min(a int, b int) int {
	if a < b {
		return a
	}
	return b
}

type detachedHeadError struct {
	err error
}

func (e detachedHeadError) Error() string {
	return e.err.Error()
}

func asDetached(err error, target *detachedHeadError) bool {
	value, ok := err.(detachedHeadError)
	if !ok {
		return false
	}
	*target = value
	return true
}
