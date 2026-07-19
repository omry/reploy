package main

import (
	"os"

	"github.com/omry/reploy/internal/probe"
)

func main() {
	os.Exit(probe.Main(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
