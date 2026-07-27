package dockerdeploy

import (
	"bufio"
	"bytes"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	PrivateWorkloadEnvironmentFileName = ".env"
	privateWorkloadEnvironmentMaxBytes = 1024 * 1024
)

type privateWorkloadEnvironmentV1 struct {
	Present bool
	Payload []byte
	Raw     []byte
}

func loadPrivateWorkloadEnvironmentV1(deploymentDir string) (privateWorkloadEnvironmentV1, error) {
	raw, found, err := readPrivateWorkloadEnvironmentFileV1(deploymentDir)
	if err != nil {
		return privateWorkloadEnvironmentV1{}, err
	}
	if !found {
		return privateWorkloadEnvironmentV1{}, nil
	}
	assignments, err := parsePrivateWorkloadEnvironmentV1(raw)
	if err != nil {
		return privateWorkloadEnvironmentV1{}, fmt.Errorf("validate %s: %w", PrivateWorkloadEnvironmentFileName, err)
	}
	var payload bytes.Buffer
	for _, assignment := range assignments {
		payload.WriteString(assignment)
		payload.WriteByte('\n')
	}
	payload.WriteByte('\n')
	return privateWorkloadEnvironmentV1{
		Present: true,
		Payload: payload.Bytes(),
		Raw:     append([]byte(nil), raw...),
	}, nil
}

func parsePrivateWorkloadEnvironmentV1(content []byte) ([]string, error) {
	if len(content) > privateWorkloadEnvironmentMaxBytes {
		return nil, fmt.Errorf("file exceeds the %d-byte limit", privateWorkloadEnvironmentMaxBytes)
	}
	if !utf8.Valid(content) {
		return nil, fmt.Errorf("file must be UTF-8")
	}
	values := map[string]string{}
	scanner := bufio.NewScanner(bytes.NewReader(content))
	scanner.Buffer(make([]byte, 4096), privateWorkloadEnvironmentMaxBytes+1)
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		line := strings.TrimSuffix(scanner.Text(), "\r")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		nameText, valueText, found := strings.Cut(trimmed, "=")
		if !found {
			return nil, fmt.Errorf("line %d must be NAME=value", lineNumber)
		}
		name := strings.TrimSpace(nameText)
		if !validPrivateWorkloadEnvironmentNameV1(name) {
			return nil, fmt.Errorf("line %d has an invalid variable name", lineNumber)
		}
		if _, exists := values[name]; exists {
			return nil, fmt.Errorf("line %d repeats variable name", lineNumber)
		}
		value, err := parsePrivateWorkloadEnvironmentValueV1(strings.TrimSpace(valueText))
		if err != nil {
			return nil, fmt.Errorf("line %d has an invalid value: %w", lineNumber, err)
		}
		if strings.ContainsAny(value, "\x00\r\n") {
			return nil, fmt.Errorf("line %d value contains an unsupported control character", lineNumber)
		}
		values[name] = value
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read file: %w", err)
	}
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	assignments := make([]string, 0, len(names))
	for _, name := range names {
		assignments = append(assignments, name+"="+values[name])
	}
	return assignments, nil
}

func parsePrivateWorkloadEnvironmentValueV1(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	switch value[0] {
	case '\'':
		if len(value) < 2 || value[len(value)-1] != '\'' {
			return "", fmt.Errorf("single-quoted value is not terminated")
		}
		return value[1 : len(value)-1], nil
	case '"':
		decoded, err := strconv.Unquote(value)
		if err != nil {
			return "", fmt.Errorf("double-quoted value is invalid")
		}
		return decoded, nil
	default:
		return value, nil
	}
}

func validPrivateWorkloadEnvironmentNameV1(name string) bool {
	if name == "" || name[0] != '_' && (name[0] < 'A' || name[0] > 'Z') && (name[0] < 'a' || name[0] > 'z') {
		return false
	}
	for index := 1; index < len(name); index++ {
		char := name[index]
		if char == '_' || char >= 'A' && char <= 'Z' || char >= 'a' && char <= 'z' || char >= '0' && char <= '9' {
			continue
		}
		return false
	}
	return true
}

func validatePrivateWorkloadEnvironmentIsolationV1(deploymentDir string, plan DockerExecutionPlan) error {
	if strings.TrimSpace(plan.Restart) != "" && strings.TrimSpace(plan.Restart) != "no" {
		return fmt.Errorf(
			"%s cannot be used with Docker restart policy %q; Docker cannot privately reinject workload environment values, so use Reploy-managed restart instead",
			PrivateWorkloadEnvironmentFileName,
			plan.Restart,
		)
	}
	environmentPath := filepath.Join(deploymentDir, PrivateWorkloadEnvironmentFileName)
	realEnvironmentDir, err := filepath.EvalSymlinks(filepath.Dir(environmentPath))
	if err != nil {
		return fmt.Errorf("resolve %s parent directory: %w", PrivateWorkloadEnvironmentFileName, err)
	}
	realEnvironmentPath := filepath.Join(realEnvironmentDir, filepath.Base(environmentPath))
	for _, mount := range plan.Mounts {
		if mount.Mode != "bind" && mount.Mode != "managed-bind" {
			continue
		}
		source, err := filepath.Abs(mount.Source)
		if err != nil {
			return fmt.Errorf("resolve runtime mount %q while protecting %s: %w", mount.Name, PrivateWorkloadEnvironmentFileName, err)
		}
		if info, err := filepath.EvalSymlinks(source); err == nil {
			source = info
		} else {
			return fmt.Errorf("resolve runtime mount %q while protecting %s: %w", mount.Name, PrivateWorkloadEnvironmentFileName, err)
		}
		if pathContainsOrEqualsV1(source, realEnvironmentPath) {
			return fmt.Errorf(
				"runtime mount %q exposes %s through host source %s; private workload environment files and their ancestors must not be mounted",
				mount.Name,
				PrivateWorkloadEnvironmentFileName,
				mount.Source,
			)
		}
	}
	return nil
}

func validatePrivateWorkloadEnvironmentIsolationV1ForPlan(deploymentDir string, plan DockerExecutionPlan) error {
	if strings.TrimSpace(plan.Restart) != "" && strings.TrimSpace(plan.Restart) != "no" {
		return fmt.Errorf(
			"%s cannot be installed with Docker restart policy %q; Docker cannot privately reinject workload environment values",
			PrivateWorkloadEnvironmentFileName,
			plan.Restart,
		)
	}
	environmentPath := filepath.Join(deploymentDir, PrivateWorkloadEnvironmentFileName)
	for _, mount := range plan.Mounts {
		if mount.Mode != "bind" && mount.Mode != "managed-bind" {
			continue
		}
		source, err := filepath.Abs(mount.Source)
		if err != nil {
			return fmt.Errorf("resolve installed runtime mount %q while protecting %s: %w", mount.Name, PrivateWorkloadEnvironmentFileName, err)
		}
		if pathContainsOrEqualsV1(source, environmentPath) {
			return fmt.Errorf(
				"installed runtime mount %q exposes %s through host source %s; private workload environment files and their ancestors must not be mounted",
				mount.Name,
				PrivateWorkloadEnvironmentFileName,
				mount.Source,
			)
		}
	}
	return nil
}

func pathContainsOrEqualsV1(parent string, child string) bool {
	relative, err := filepath.Rel(filepath.Clean(parent), filepath.Clean(child))
	return err == nil && (relative == "." || relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative))
}
