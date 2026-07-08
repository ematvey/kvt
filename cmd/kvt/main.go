package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"

	"github.com/ematvey/kvt/internal/config"
	"github.com/ematvey/kvt/internal/gitops"
	"github.com/ematvey/kvt/internal/httpapi"
	kvtmcp "github.com/ematvey/kvt/internal/mcp"
	"github.com/ematvey/kvt/internal/ontology"
	"github.com/ematvey/kvt/internal/service"
	"github.com/ematvey/kvt/internal/version"
)

func main() {
	os.Exit(run(os.Args, os.Stdout, os.Stderr))
}

func run(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) < 2 {
		fmt.Fprintln(stderr, "usage: kvt <init|serve|reindex|validate|push|version>")
		return 2
	}
	switch args[1] {
	case "version":
		fmt.Fprintln(stdout, version.Version)
		return 0
	case "init":
		fs := flag.NewFlagSet("init", flag.ContinueOnError)
		fs.SetOutput(stderr)
		vaultPath := fs.String("vault", os.Getenv("KVT_VAULT"), "vault path")
		defaults := fs.Bool("defaults", false, "write default config without prompts")
		if err := fs.Parse(args[2:]); err != nil {
			return 2
		}
		if *vaultPath == "" {
			fmt.Fprintln(stderr, "init requires --vault or KVT_VAULT")
			return 2
		}
		if _, err := service.Init(context.Background(), service.InitRequest{
			VaultPath: *vaultPath,
			Defaults:  *defaults,
		}); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		return 0
	case "validate":
		fs := flag.NewFlagSet("validate", flag.ContinueOnError)
		fs.SetOutput(stderr)
		vaultPath := fs.String("vault", os.Getenv("KVT_VAULT"), "vault path")
		configPath := fs.String("config", "", "config path")
		if err := fs.Parse(args[2:]); err != nil {
			return 2
		}
		if *vaultPath == "" {
			fmt.Fprintln(stderr, "validate requires --vault or KVT_VAULT")
			return 2
		}
		cfg, err := config.Load(*vaultPath, *configPath)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		svc, err := service.New(*vaultPath, cfg, service.Deps{})
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		resp, err := svc.Validate(context.Background(), service.ValidateRequest{})
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		printIssues(stdout, "warning", resp.Warnings)
		if len(resp.Errors) > 0 {
			printIssues(stderr, "error", resp.Errors)
			return 1
		}
		return 0
	case "reindex":
		fs := flag.NewFlagSet("reindex", flag.ContinueOnError)
		fs.SetOutput(stderr)
		vaultPath := fs.String("vault", os.Getenv("KVT_VAULT"), "vault path")
		configPath := fs.String("config", "", "config path")
		if err := fs.Parse(args[2:]); err != nil {
			return 2
		}
		if *vaultPath == "" {
			fmt.Fprintln(stderr, "reindex requires --vault or KVT_VAULT")
			return 2
		}
		if err := requireInitializedVault(*vaultPath); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		cfg, err := config.Load(*vaultPath, *configPath)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		lock, err := service.AcquireVaultLock(*vaultPath)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		defer lock.Release()
		svc, err := service.New(*vaultPath, cfg, service.Deps{})
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		result, err := svc.Rebuild(context.Background())
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		fmt.Fprintf(stdout, "reindexed %d documents\n", len(result.AppliedDocuments))
		return 0
	case "push":
		fs := flag.NewFlagSet("push", flag.ContinueOnError)
		fs.SetOutput(stderr)
		vaultPath := fs.String("vault", os.Getenv("KVT_VAULT"), "vault path")
		configPath := fs.String("config", "", "config path")
		remoteName := fs.String("remote", "", "remote name")
		branch := fs.String("branch", "", "branch name")
		if err := fs.Parse(args[2:]); err != nil {
			return 2
		}
		if *vaultPath == "" {
			fmt.Fprintln(stderr, "push requires --vault or KVT_VAULT")
			return 2
		}
		if err := requireInitializedVault(*vaultPath); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		cfg, err := config.Load(*vaultPath, *configPath)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		lock, err := service.AcquireVaultLock(*vaultPath)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		defer lock.Release()
		svc, err := service.New(*vaultPath, cfg, service.Deps{})
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		resp, err := svc.Push(context.Background(), service.PushRequest{
			RemoteName: *remoteName,
			Branch:     *branch,
		})
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		fmt.Fprintf(stdout, "pushed %d commits to %s/%s\n", resp.PushedCommits, resp.RemoteName, resp.Branch)
		return 0
	case "serve":
		fs := flag.NewFlagSet("serve", flag.ContinueOnError)
		fs.SetOutput(stderr)
		vaultPath := fs.String("vault", os.Getenv("KVT_VAULT"), "vault path")
		configPath := fs.String("config", "", "config path")
		addr := fs.String("addr", "", "HTTP listen address")
		if err := fs.Parse(args[2:]); err != nil {
			return 2
		}
		if *vaultPath == "" {
			fmt.Fprintln(stderr, "serve requires --vault or KVT_VAULT")
			return 2
		}
		if err := requireInitializedVault(*vaultPath); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		cfg, err := config.Load(*vaultPath, *configPath)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		lock, err := service.AcquireVaultLock(*vaultPath)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		defer lock.Release()
		status, err := gitops.Status(*vaultPath, cfg.Git.Branch)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		if !status.BranchOK {
			fmt.Fprintf(stderr, "vault is on branch %q, expected %q\n", status.Branch, status.ExpectedBranch)
			return 1
		}
		svc, err := service.New(*vaultPath, cfg, service.Deps{})
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		defer svc.Close()
		handler, err := buildServeHandler(svc, cfg)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		if err := startConfiguredMCP(context.Background(), svc, cfg, stderr); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		listenAddr := *addr
		if listenAddr == "" {
			listenAddr = fmt.Sprintf(":%d", cfg.Server.HTTPPort)
		}
		fmt.Fprintf(stderr, "serving %s on %s\n", *vaultPath, listenAddr)
		if err := http.ListenAndServe(listenAddr, handler); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		return 0
	}
	fmt.Fprintln(stderr, "usage: kvt <init|serve|reindex|validate|push|version>")
	return 2
}

func requireInitializedVault(root string) error {
	if _, err := os.Stat(filepath.Join(root, ".git")); err != nil {
		return fmt.Errorf("vault is not initialized; run kvt init --vault %s", root)
	}
	if _, err := os.Stat(filepath.Join(root, ".kvt", "config.yaml")); err != nil {
		return fmt.Errorf("vault is not initialized; run kvt init --vault %s", root)
	}
	return nil
}

func startConfiguredMCP(ctx context.Context, svc *service.Service, cfg config.Config, stderr io.Writer) error {
	switch cfg.Server.MCPTransport {
	case "", "stdio":
		mcpServer, err := kvtmcp.NewServer(svc, cfg)
		if err != nil {
			return err
		}
		go func() {
			if err := mcpServer.RunStdio(ctx); err != nil {
				fmt.Fprintln(stderr, err)
			}
		}()
		return nil
	case "streamable-http":
		return nil
	default:
		return fmt.Errorf("unsupported mcp_transport %q", cfg.Server.MCPTransport)
	}
}

func buildServeHandler(svc *service.Service, cfg config.Config) (http.Handler, error) {
	rest := httpapi.NewServer(svc, cfg)
	switch cfg.Server.MCPTransport {
	case "", "stdio":
		return rest, nil
	case "streamable-http":
		mcpServer, err := kvtmcp.NewServer(svc, cfg)
		if err != nil {
			return nil, err
		}
		mux := http.NewServeMux()
		mux.Handle("/", rest)
		mux.Handle("/mcp", httpapi.WithAuth(kvtmcp.StreamableHTTPHandler(mcpServer), cfg))
		return mux, nil
	default:
		return nil, fmt.Errorf("unsupported mcp_transport %q", cfg.Server.MCPTransport)
	}
}

func printIssues(w io.Writer, label string, issues []ontology.Issue) {
	for _, issue := range issues {
		location := issue.Field
		if issue.Path.String() != "" {
			location = issue.Path.String()
			if issue.Field != "" {
				location += " [" + issue.Field + "]"
			}
		}
		fmt.Fprintf(w, "%s: %s: %s\n", label, location, issue.Message)
	}
}
