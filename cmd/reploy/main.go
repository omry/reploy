package main

import (
	"os"

	"github.com/omry/reploy/internal/cli"
	"github.com/omry/reploy/internal/dockerdeploy"
)

func main() {
	if code, handled := dockerdeploy.RunControlledSessionWatchdogChild(os.Args[1:], os.Stderr); handled {
		os.Exit(code)
	}
	os.Exit(cli.Main(os.Args[1:], os.Stdout, os.Stderr))
}
