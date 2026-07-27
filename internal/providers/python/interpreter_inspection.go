package python

import (
	"fmt"
	"path"
	"strconv"
	"strings"
)

const interpreterInspectionProgram = `import sys; print(".".join(map(str, sys.version_info[:3])))`

// InterpreterInspectionArgv returns the complete fixed invocation used by a
// Python consumer to identify the selected absolute interpreter. The caller
// executes this argv inside its existing resolver or materializer container.
func InterpreterInspectionArgv(executable string) ([]string, error) {
	if executable == "" || !path.IsAbs(executable) || path.Clean(executable) != executable || strings.Contains(executable, `\`) {
		return nil, fmt.Errorf("Python interpreter executable %q must be a normalized absolute Linux path", executable)
	}
	return []string{executable, "-I", "-S", "-c", interpreterInspectionProgram}, nil
}

// ParseInterpreterInspectionOutput accepts only the three canonical numeric
// components emitted by interpreterInspectionProgram. Diagnostics and other
// output cannot be mistaken for a Python version.
func ParseInterpreterInspectionOutput(output []byte) (string, error) {
	value := string(output)
	if strings.HasSuffix(value, "\n") {
		value = strings.TrimSuffix(value, "\n")
	}
	parts := strings.Split(value, ".")
	if len(parts) != 3 {
		return "", fmt.Errorf("Python interpreter inspection returned %q, want a three-part release version", value)
	}
	for _, part := range parts {
		parsed, err := strconv.Atoi(part)
		if err != nil || parsed < 0 || strconv.Itoa(parsed) != part {
			return "", fmt.Errorf("Python interpreter inspection returned %q, want a canonical three-part release version", value)
		}
	}
	return value, nil
}
