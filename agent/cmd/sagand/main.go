// Command sagand is the SaganSync agent: a daemon on the VPS and the client
// that deploy keys run through SSH.
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/borgim/sagansync/agent/internal/gateway"
)

var version = "dev" // set with -ldflags "-X main.version=v0.1.0"

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: sagand daemon | sagand gateway | sagand <command> [flags]")
		return 2
	}
	switch args[0] {
	case "daemon":
		return runDaemon(args[1:], stderr)
	case "gateway":
		cmd, err := gateway.Parse(os.Getenv("SSH_ORIGINAL_COMMAND"))
		if err != nil {
			fmt.Fprintf(stderr, "sagand: %v\n", err)
			return 126
		}
		return runClient(cmd, stdin, stdout)
	}
	return runClient(args, stdin, stdout)
}
