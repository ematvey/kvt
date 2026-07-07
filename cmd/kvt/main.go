package main

import (
	"fmt"
	"io"
	"os"

	"github.com/ematvey/kvt/internal/version"
)

func main() {
	os.Exit(run(os.Args, os.Stdout, os.Stderr))
}

func run(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) >= 2 && args[1] == "version" {
		fmt.Fprintln(stdout, version.Version)
		return 0
	}
	fmt.Fprintln(stderr, "usage: kvt <init|serve|reindex|validate|push|version>")
	return 2
}
