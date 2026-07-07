package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"

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
	}
	fmt.Fprintln(stderr, "usage: kvt <init|serve|reindex|validate|push|version>")
	return 2
}
