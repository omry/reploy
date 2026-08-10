package probe

import (
	"errors"
	"fmt"
	"math/big"
	"os"
	"path"
	"strconv"
	"strings"
)

// Sandbox setup is deliberately pinned to one OS thread because Linux
// credentials and capability sets are thread-scoped. /proc/self/status
// describes the thread-group leader, which may be a different Go runtime
// thread; verify the exact thread that will exec the application instead.
const applicationKernelStatusPath = "/proc/thread-self/status"

var requiredApplicationKernelStatusV1 = []struct {
	name string
	want string
	hex  bool
}{
	{name: "CapInh", want: "0", hex: true},
	{name: "CapBnd", want: "0", hex: true},
	{name: "CapEff", want: "0", hex: true},
	{name: "CapPrm", want: "0", hex: true},
	{name: "CapAmb", want: "0", hex: true},
	{name: "NoNewPrivs", want: "1"},
	{name: "Seccomp", want: "2"},
}

func readApplicationKernelStatus() ([]byte, error) {
	return readApplicationKernelStatusWithV1(os.ReadFile, applicationKernelStatusFallbackPathV1)
}

func readApplicationKernelStatusWithV1(
	readFile func(string) ([]byte, error),
	fallbackPath func() (string, error),
) ([]byte, error) {
	content, err := readFile(applicationKernelStatusPath)
	if err == nil {
		return content, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read %s: %w", applicationKernelStatusPath, err)
	}

	compatibilityPath, pathErr := fallbackPath()
	if pathErr != nil {
		return nil, fmt.Errorf("read %s and determine compatibility path: %w", applicationKernelStatusPath, pathErr)
	}
	content, err = readFile(compatibilityPath)
	if err != nil {
		return nil, fmt.Errorf("read %s after %s was unavailable: %w", compatibilityPath, applicationKernelStatusPath, err)
	}
	return content, nil
}

func verifyAndExecApplication(
	argv []string,
	readStatus func() ([]byte, error),
	exec func([]string) error,
) error {
	if len(argv) == 0 || argv[0] == "" || !path.IsAbs(argv[0]) || path.Clean(argv[0]) != argv[0] {
		return fmt.Errorf("application command must begin with a normalized absolute path")
	}
	if readStatus == nil || exec == nil {
		return fmt.Errorf("startup verifier is incomplete")
	}
	content, err := readStatus()
	if err != nil {
		return err
	}
	if err := verifyApplicationKernelStatus(content); err != nil {
		return err
	}
	if err := exec(append([]string(nil), argv...)); err != nil {
		return fmt.Errorf("execute application %q: %w", argv[0], err)
	}
	return nil
}

func verifyApplicationKernelStatus(content []byte) error {
	values := make(map[string]string, len(requiredApplicationKernelStatusV1))
	required := make(map[string]bool, len(requiredApplicationKernelStatusV1))
	for _, field := range requiredApplicationKernelStatusV1 {
		required[field.name] = true
	}
	for _, line := range strings.Split(string(content), "\n") {
		name, raw, found := strings.Cut(line, ":")
		if !found || !required[name] {
			continue
		}
		if _, exists := values[name]; exists {
			return fmt.Errorf("%s appears more than once in %s", name, applicationKernelStatusPath)
		}
		fields := strings.Fields(raw)
		if len(fields) != 1 {
			return fmt.Errorf("%s is malformed in %s", name, applicationKernelStatusPath)
		}
		values[name] = fields[0]
	}
	for _, field := range requiredApplicationKernelStatusV1 {
		value, found := values[field.name]
		if !found {
			return fmt.Errorf("%s is missing from %s", field.name, applicationKernelStatusPath)
		}
		if field.hex {
			parsed := new(big.Int)
			if _, ok := parsed.SetString(value, 16); !ok {
				return fmt.Errorf("%s value %q is not hexadecimal", field.name, value)
			}
			if parsed.Sign() != 0 {
				return fmt.Errorf("%s is %s, want an empty capability set", field.name, value)
			}
			continue
		}
		parsed, err := strconv.ParseUint(value, 10, 8)
		if err != nil {
			return fmt.Errorf("%s value %q is not a decimal integer", field.name, value)
		}
		want, _ := strconv.ParseUint(field.want, 10, 8)
		if parsed != want {
			return fmt.Errorf("%s is %d, want %d", field.name, parsed, want)
		}
	}
	return nil
}
