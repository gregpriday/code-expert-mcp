// Command codeexpert is the read-only repository planning, help, and code-review
// MCP server and CLI.
package main

import (
	"context"
	"os"

	"github.com/gregpriday/codeexpert/internal/cli"
)

func main() {
	os.Exit(cli.Run(context.Background(), os.Args[1:]))
}
