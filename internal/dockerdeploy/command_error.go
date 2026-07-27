package dockerdeploy

import (
	"fmt"
	"strings"
)

func commandErrorWithOutput(label string, output []byte, err error) error {
	trimmedOutput := strings.TrimSpace(string(output))
	if trimmedOutput == "" {
		return fmt.Errorf("%s: %w", label, err)
	}
	return fmt.Errorf("%s: %w\n%s", label, err, trimmedOutput)
}
