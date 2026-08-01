package dockerdeploy

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/omry/reploy/internal/deploy"
)

const (
	PrivateWorkloadEnvironmentFileName  = ".env"
	privateWorkloadEnvironmentMaxBytes  = 1024 * 1024
	privateRuntimeMetadataDirectoryName = ".reploy"
)

type privateRuntimeMaskKindV1 string

const (
	privateRuntimeMaskDirectoryV1 privateRuntimeMaskKindV1 = "directory"
	privateRuntimeMaskFileV1      privateRuntimeMaskKindV1 = "file"
)

type privateRuntimeMaskV1 struct {
	Kind   privateRuntimeMaskKindV1
	Target string
}

type privateWorkloadEnvironmentV1 struct {
	Exists  bool
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
		Exists:  true,
		Present: len(assignments) != 0,
		Payload: payload.Bytes(),
		Raw:     append([]byte{}, raw...),
	}, nil
}

// preparePrivateWorkloadEnvironmentV1 makes the private environment path a
// stable regular-file mountpoint before container creation, then validates and
// loads it through the defensive platform-specific reader. The empty
// placeholder is not considered a configured private environment.
func preparePrivateWorkloadEnvironmentV1(deploymentDir string) (privateWorkloadEnvironmentV1, error) {
	target := filepath.Join(deploymentDir, PrivateWorkloadEnvironmentFileName)
	if _, err := publishPrivateWorkloadEnvironmentFileV1(target, nil, false); err != nil {
		return privateWorkloadEnvironmentV1{}, fmt.Errorf("create empty %s mountpoint: %w", PrivateWorkloadEnvironmentFileName, err)
	}
	environment, err := loadPrivateWorkloadEnvironmentV1(deploymentDir)
	if err != nil {
		return privateWorkloadEnvironmentV1{}, err
	}
	return environment, nil
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
	plan.DeploymentDir = deploymentDir
	if _, err := privateRuntimeMasksV1(plan); err != nil {
		return fmt.Errorf("plan private runtime masks: %w", err)
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
	plan.DeploymentDir = deploymentDir
	if _, err := privateRuntimeMasksV1(plan); err != nil {
		return fmt.Errorf("plan installed private runtime masks: %w", err)
	}
	return nil
}

func validatePrivateRuntimeMaskSnapshotV1(plan DockerExecutionPlan, expected []privateRuntimeMaskV1) error {
	masks, err := privateRuntimeMasksV1(plan)
	if err != nil {
		return fmt.Errorf("resolve current private runtime masks: %w", err)
	}
	if len(masks) != len(expected) {
		return fmt.Errorf("runtime bind sources changed after runtime inputs were rendered; rerun the operation")
	}
	for index := range masks {
		if masks[index] != expected[index] {
			return fmt.Errorf("runtime bind sources changed after runtime inputs were rendered; rerun the operation")
		}
	}
	return nil
}

func privateRuntimeMasksV1(plan DockerExecutionPlan) ([]privateRuntimeMaskV1, error) {
	if strings.TrimSpace(plan.DeploymentDir) == "" {
		return nil, fmt.Errorf("private runtime masks require a deployment directory")
	}
	root, err := canonicalPathAllowMissingV1(plan.DeploymentDir)
	if err != nil {
		return nil, fmt.Errorf("resolve deployment root: %w", err)
	}
	protected := []struct {
		path string
		kind privateRuntimeMaskKindV1
	}{
		{path: filepath.Join(root, privateRuntimeMetadataDirectoryName), kind: privateRuntimeMaskDirectoryV1},
		{path: filepath.Join(root, PrivateWorkloadEnvironmentFileName), kind: privateRuntimeMaskFileV1},
	}
	byTarget := map[string]privateRuntimeMaskKindV1{}
	for _, mount := range plan.Mounts {
		if mount.Mode != "bind" && mount.Mode != "managed-bind" {
			continue
		}
		source, err := canonicalPathAllowMissingV1(mount.Source)
		if err != nil {
			return nil, fmt.Errorf("resolve runtime mount %q source: %w", mount.Name, err)
		}
		sourceKind, err := runtimeMountSourceKindV1(mount)
		if err != nil {
			return nil, fmt.Errorf("resolve runtime mount %q kind: %w", mount.Name, err)
		}
		for _, item := range protected {
			target, kind, exposed := privateRuntimeMaskTargetV1(source, sourceKind, mount.Target, item.path, item.kind)
			if !exposed {
				continue
			}
			if previous, found := byTarget[target]; found && previous != kind {
				return nil, fmt.Errorf("runtime mask target %q has conflicting file and directory types", target)
			}
			byTarget[target] = kind
		}
	}
	targets := make([]string, 0, len(byTarget))
	for target := range byTarget {
		targets = append(targets, target)
	}
	sort.Strings(targets)
	masks := make([]privateRuntimeMaskV1, 0, len(targets))
	for _, target := range targets {
		masks = append(masks, privateRuntimeMaskV1{Kind: byTarget[target], Target: target})
	}
	return masks, nil
}

func privateRuntimeMaskTargetV1(
	source string,
	sourceKind string,
	containerTarget string,
	protectedPath string,
	protectedKind privateRuntimeMaskKindV1,
) (target string, kind privateRuntimeMaskKindV1, exposed bool) {
	if pathContainsOrEqualsV1(source, protectedPath) {
		relative, _ := filepath.Rel(source, protectedPath)
		if relative == "." {
			return path.Clean(containerTarget), protectedKind, true
		}
		return path.Join(containerTarget, filepath.ToSlash(relative)), protectedKind, true
	}
	if !pathContainsOrEqualsV1(protectedPath, source) {
		return "", "", false
	}
	kind = privateRuntimeMaskDirectoryV1
	if sourceKind == deploy.RuntimeMountSourceFile {
		kind = privateRuntimeMaskFileV1
	}
	return path.Clean(containerTarget), kind, true
}

func canonicalPathAllowMissingV1(value string) (string, error) {
	absolute, err := filepath.Abs(value)
	if err != nil {
		return "", err
	}
	current := filepath.Clean(absolute)
	missing := []string{}
	for {
		_, err := os.Lstat(current)
		if err == nil {
			resolved, err := filepath.EvalSymlinks(current)
			if err != nil {
				return "", err
			}
			for index := len(missing) - 1; index >= 0; index-- {
				resolved = filepath.Join(resolved, missing[index])
			}
			return filepath.Clean(resolved), nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", err
		}
		missing = append(missing, filepath.Base(current))
		current = parent
	}
}

func pathContainsOrEqualsV1(parent string, child string) bool {
	relative, err := filepath.Rel(filepath.Clean(parent), filepath.Clean(child))
	return err == nil && (relative == "." || relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative))
}
