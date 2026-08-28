// Command argx is a terminal UI for Argo CD.
package main

import (
	"fmt"
	"os"

	"github.com/keyolk/argx/internal/cli"
)

// version is set at build time via -ldflags.
var version = "dev"

func main() {
	if err := cli.Execute(version); err != nil {
		fmt.Fprintln(os.Stderr, "argx: "+err.Error())
		os.Exit(1)
	}
}
