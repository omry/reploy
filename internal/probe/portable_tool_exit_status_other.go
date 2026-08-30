//go:build !linux

package probe

import (
	"fmt"
	"io"
)

func runPortableToolObservedExecV1([]string, io.Reader, io.Writer, io.Writer) error {
	return fmt.Errorf("portable-tool observed exec is supported only in Linux containers")
}

func runPortableToolApplicationExecV1([]string) error {
	return fmt.Errorf("portable-tool application exec is supported only in Linux containers")
}

func readPortableToolExitStatusV1(io.Writer) error {
	return fmt.Errorf("portable-tool exit status is supported only in Linux containers")
}
