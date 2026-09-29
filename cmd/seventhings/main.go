// Command seventhings is the CLI for the seventhings customer API.
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/SeventhingsCompany/customer-api-cli/internal/cmd"
	"golang.org/x/term"
)

// Set via -ldflags by goreleaser.
var (
	version = "dev"
	commit  = ""
	date    = ""
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := cmd.Execute(ctx, os.Args[1:], cmd.IO{
		In:        os.Stdin,
		Out:       os.Stdout,
		Err:       os.Stderr,
		StdinTTY:  term.IsTerminal(int(os.Stdin.Fd())),
		StdoutTTY: term.IsTerminal(int(os.Stdout.Fd())),
	}, os.Getenv, cmd.BuildInfo{Version: version, Commit: commit, Date: date})
	stop()
	os.Exit(code)
}
