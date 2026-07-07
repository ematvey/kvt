package service

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/ematvey/kvt/internal/config"
	"github.com/ematvey/kvt/internal/frontmatter"
	"github.com/ematvey/kvt/internal/gitops"
	"github.com/ematvey/kvt/internal/pathutil"
	"github.com/ematvey/kvt/internal/vault"
	"gopkg.in/yaml.v3"
)

const rootOKFVersion = "0.1"

type Deps struct{}

type Service struct {
	root string
	cfg  config.Config
	git  gitops.Client
}

type InitRequest struct {
	VaultPath string
	Defaults  bool
}

type InitResult struct {
	Branch  string
	Created bool
}

func New(root string, cfg config.Config, deps Deps) (*Service, error) {
	if strings.TrimSpace(root) == "" {
		return nil, fmt.Errorf("vault root is required")
	}
	return &Service{
		root: root,
		cfg:  cfg,
		git:  gitops.New(root),
	}, nil
}

func Init(ctx context.Context, req InitRequest) (result InitResult, err error) {
	_ = ctx
	root := strings.TrimSpace(req.VaultPath)
	if root == "" {
		return InitResult{}, fmt.Errorf("vault path is required")
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return InitResult{}, err
	}
	lock, err := AcquireVaultLock(root)
	if err != nil {
		return InitResult{}, err
	}
	defer func() {
		releaseErr := lock.Release()
		if err == nil && releaseErr != nil {
			err = releaseErr
		}
	}()

	hasGit, err := hasGitRepo(root)
	if err != nil {
		return InitResult{}, err
	}

	branch := "main"
	if hasGit {
		status, err := gitops.Status(root, "")
		if err != nil {
			return InitResult{}, err
		}
		if status.Detached {
			return InitResult{}, fmt.Errorf("cannot initialize from detached HEAD")
		}
		branch = status.Branch
	} else {
		empty, err := isEmptyDir(root)
		if err != nil {
			return InitResult{}, err
		}
		if !empty {
			return InitResult{}, fmt.Errorf("vault path must be empty or an existing git repository")
		}
		if err := initRepo(root, branch); err != nil {
			return InitResult{}, err
		}
	}

	changedPaths, err := ensureVaultFiles(root, branch)
	if err != nil {
		return InitResult{}, err
	}
	changed := len(changedPaths) > 0
	if changed {
		commitResult, err := gitops.Commit(root, gitops.CommitOptions{
			Message:     "Initialize KVT vault",
			Paths:       changedPaths,
			AuthorName:  config.Default().Git.AuthorName,
			AuthorEmail: config.Default().Git.AuthorEmail,
		})
		if err != nil {
			return InitResult{}, err
		}
		if !commitResult.Changed {
			changed = false
		}
	}
	return InitResult{
		Branch:  branch,
		Created: changed,
	}, nil
}

func initRepo(root string, branch string) error {
	cmd := exec.Command("git", "init", "-b", branch)
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git init: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func ensureVaultFiles(root string, branch string) ([]string, error) {
	changedPaths := []string{}
	fileChanged, err := ensureGitIgnore(root)
	if err != nil {
		return nil, err
	}
	if fileChanged {
		changedPaths = append(changedPaths, ".gitignore")
	}

	if _, err = ensureDefaultConfig(root, branch); err != nil {
		return nil, err
	}

	fileChanged, err = ensureStarterOntology(root)
	if err != nil {
		return nil, err
	}
	if fileChanged {
		changedPaths = append(changedPaths, "_ontology.yaml")
	}

	indexPaths, err := ensureIndexes(root)
	if err != nil {
		return nil, err
	}
	changedPaths = appendUniquePaths(changedPaths, indexPaths...)

	return changedPaths, nil
}

func ensureGitIgnore(root string) (bool, error) {
	path := filepath.Join(root, ".gitignore")
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return false, err
	}
	if strings.Contains(string(data), ".kvt/") {
		return false, nil
	}
	content := string(data)
	if content != "" && !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	content += ".kvt/\n"
	return true, os.WriteFile(path, []byte(content), 0o644)
}

func ensureDefaultConfig(root string, branch string) (bool, error) {
	path := filepath.Join(root, ".kvt", "config.yaml")
	if _, err := os.Stat(path); err == nil {
		return false, nil
	} else if !os.IsNotExist(err) {
		return false, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	cfg := config.Default()
	cfg.Git.Branch = branch
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return false, err
	}
	return true, os.WriteFile(path, data, 0o644)
}

func ensureStarterOntology(root string) (bool, error) {
	path := filepath.Join(root, "_ontology.yaml")
	if _, err := os.Stat(path); err == nil {
		return false, nil
	} else if !os.IsNotExist(err) {
		return false, err
	}
	data := []byte("types: {}\nrules: []\nunknown_types: warn\n")
	return true, os.WriteFile(path, data, 0o644)
}

func ensureIndexes(root string) ([]string, error) {
	paths, err := existingMarkdownPaths(root)
	if err != nil {
		return nil, err
	}
	if len(paths) == 0 {
		if _, err := os.Stat(filepath.Join(root, "index.md")); err == nil {
			return nil, nil
		} else if !os.IsNotExist(err) {
			return nil, err
		}
		content, err := frontmatter.Render(frontmatter.Document{
			Fields: map[string]any{
				"okf_version": rootOKFVersion,
				"type":        "Index",
			},
			Body: []byte("# Index\n"),
		})
		if err != nil {
			return nil, err
		}
		return []string{"index.md"}, os.WriteFile(filepath.Join(root, "index.md"), content, 0o644)
	}

	changedPaths := []string{}
	for _, docPath := range paths {
		writtenPaths, err := regenerateIndexes(root, docPath)
		if err != nil {
			return nil, err
		}
		changedPaths = appendUniquePaths(changedPaths, writtenPaths...)
	}
	return changedPaths, nil
}

func regenerateIndexes(root string, docPath pathutil.Path) ([]string, error) {
	return vault.RegenerateIndexes(root, docPath, 50, rootOKFVersion)
}

func existingMarkdownPaths(root string) ([]pathutil.Path, error) {
	paths := []pathutil.Path{}
	err := filepath.WalkDir(root, func(filePath string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" || d.Name() == ".kvt" {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(d.Name()) != ".md" || d.Name() == "index.md" {
			return nil
		}
		rel, err := filepath.Rel(root, filePath)
		if err != nil {
			return err
		}
		normalized, err := pathutil.Normalize(filepath.ToSlash(rel))
		if err != nil {
			return nil
		}
		paths = append(paths, normalized)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return paths, nil
}

func hasGitRepo(root string) (bool, error) {
	_, err := os.Stat(filepath.Join(root, ".git"))
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

func isEmptyDir(root string) (bool, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return false, err
	}
	for _, entry := range entries {
		if entry.Name() == ".kvt" {
			continue
		}
		return false, nil
	}
	return true, nil
}

func appendUniquePaths(paths []string, more ...string) []string {
	seen := make(map[string]struct{}, len(paths)+len(more))
	for _, path := range paths {
		seen[path] = struct{}{}
	}
	for _, path := range more {
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		paths = append(paths, path)
	}
	return paths
}
