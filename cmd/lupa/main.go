// Command lupa is a verified code reviewer that runs locally with your coding agent.
package main

import (
	"fmt"
	"os"

	"github.com/pavp/lupa/internal/cli"
)

func main() {
	dir, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "lupa: %s\n", err)
		os.Exit(2)
	}
	os.Exit(cli.Run(os.Args[1:], cli.Env{Dir: dir, Stdout: os.Stdout, Stderr: os.Stderr}))
}
