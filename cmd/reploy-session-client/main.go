package main

import (
	"os"

	"github.com/omry/reploy/internal/sessionclientcmd"
)

func main() {
	os.Exit(sessionclientcmd.Main(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
