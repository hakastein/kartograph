// Command kartograph builds a graph of a git worktree's code and answers questions about it.
package main

import (
	"os"

	"github.com/hakastein/kartograph/internal/cli"
)

func main() {
	dir, _ := os.Getwd()
	os.Exit(cli.Run(os.Args[1:], os.Environ(), dir, os.Stdout, os.Stderr))
}
