package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/omry/reploy/internal/probearchive"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stderr))
}

func run(args []string, stderr io.Writer) int {
	flags := flag.NewFlagSet("reploy-probe-pack", flag.ContinueOnError)
	flags.SetOutput(stderr)
	executable := flags.String("executable", "", "Reploy executable to pack")
	linuxAMD64 := flags.String("linux-amd64", "", "linux/amd64 reploy-probe")
	linuxARMv7 := flags.String("linux-arm-v7", "", "linux/arm/v7 reploy-probe")
	linuxARM64 := flags.String("linux-arm64", "", "linux/arm64 reploy-probe")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 || *executable == "" || *linuxAMD64 == "" || *linuxARMv7 == "" || *linuxARM64 == "" {
		_, _ = fmt.Fprintln(stderr, "reploy-probe-pack requires --executable, --linux-amd64, --linux-arm-v7, and --linux-arm64")
		return 2
	}
	inputs := []probearchive.HelperInput{
		{Platform: "linux/amd64", Path: *linuxAMD64},
		{Platform: "linux/arm/v7", Path: *linuxARMv7},
		{Platform: "linux/arm64", Path: *linuxARM64},
	}
	if err := probearchive.Append(*executable, inputs); err != nil {
		_, _ = fmt.Fprintf(stderr, "reploy-probe-pack: %v\n", err)
		return 1
	}
	return 0
}
