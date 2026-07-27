package dockerdeploy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path"
	"strings"
	"unicode/utf8"

	"github.com/omry/reploy/internal/providers"
)

const MaterializationDockerfileSyntax = "docker/dockerfile:1.24.0@sha256:87999aa3d42bdc6bea60565083ee17e86d1f3339802f543c0d03998580f9cb89"

type MaterializationMountSource struct {
	ID          string
	ContextPath string
}

func MaterializationDockerfile(transaction providers.MaterializationTransaction, sources []MaterializationMountSource) ([]byte, error) {
	argv, err := RenderMaterializationArgv(transaction)
	if err != nil {
		return nil, err
	}
	if len(transaction.BuildIdentity.SupplementaryGIDs) != 0 {
		return nil, fmt.Errorf("Dockerfile materialization does not support supplementary build groups")
	}
	sourceByID, err := validateMaterializationMountSources(transaction.Mounts, sources)
	if err != nil {
		return nil, err
	}
	encodedArgv, err := json.Marshal(argv)
	if err != nil {
		return nil, fmt.Errorf("encode materialization argv: %w", err)
	}

	var output bytes.Buffer
	fmt.Fprintf(&output, "# syntax=%s\n", MaterializationDockerfileSyntax)
	output.WriteString("ARG REPLOY_BASE_IMAGE=scratch\n")
	output.WriteString("FROM ${REPLOY_BASE_IMAGE}\n")
	fmt.Fprintf(&output, "USER %s:%s\n", transaction.BuildIdentity.UID, transaction.BuildIdentity.GID)
	workingDirectory, err := quoteDockerfileWord(transaction.WorkingDirectory)
	if err != nil {
		return nil, fmt.Errorf("render materialization working directory: %w", err)
	}
	fmt.Fprintf(&output, "WORKDIR %s\n", workingDirectory)
	output.WriteString("RUN --network=none")
	for _, mount := range transaction.Mounts {
		if err := validateMountOptionPath("target", mount.Destination, true); err != nil {
			return nil, fmt.Errorf("render build mount %q: %w", mount.ID, err)
		}
		switch mount.SourceKind {
		case providers.BuildMountSourceArtifact, providers.BuildMountSourceScript:
			source := sourceByID[mount.ID]
			fmt.Fprintf(&output, " --mount=type=bind,source=%s,target=%s,readonly", source, mount.Destination)
		case providers.BuildMountSourcePrivateOutput:
			return nil, fmt.Errorf("render build mount %q: private-output mounts belong to the disposable resolver runner", mount.ID)
		default:
			return nil, fmt.Errorf("render build mount %q: unsupported source kind %q", mount.ID, mount.SourceKind)
		}
	}
	fmt.Fprintf(&output, " %s\n", encodedArgv)

	config := transaction.FinalImageConfig
	for _, variable := range config.Environment {
		value, err := quoteDockerfileWord(variable.Value)
		if err != nil {
			return nil, fmt.Errorf("render final environment variable %q: %w", variable.Name, err)
		}
		fmt.Fprintf(&output, "ENV %s=%s\n", variable.Name, value)
	}
	user, err := quoteDockerfileWord(config.User)
	if err != nil {
		return nil, fmt.Errorf("render final image user: %w", err)
	}
	fmt.Fprintf(&output, "USER %s\n", user)
	finalWorkingDirectory, err := quoteDockerfileWord(config.WorkingDir)
	if err != nil {
		return nil, fmt.Errorf("render final working directory: %w", err)
	}
	fmt.Fprintf(&output, "WORKDIR %s\n", finalWorkingDirectory)
	entrypoint, err := json.Marshal(config.Entrypoint)
	if err != nil {
		return nil, fmt.Errorf("encode final entrypoint: %w", err)
	}
	command, err := json.Marshal(config.Command)
	if err != nil {
		return nil, fmt.Errorf("encode final command: %w", err)
	}
	fmt.Fprintf(&output, "ENTRYPOINT %s\n", entrypoint)
	fmt.Fprintf(&output, "CMD %s\n", command)
	output.WriteString("HEALTHCHECK NONE\n")
	if err := validateDockerfileAtom("stop signal", config.StopSignal); err != nil {
		return nil, err
	}
	fmt.Fprintf(&output, "STOPSIGNAL %s\n", config.StopSignal)
	for _, label := range config.Labels {
		name, err := quoteDockerfileWord(label.Name)
		if err != nil {
			return nil, fmt.Errorf("render image label name: %w", err)
		}
		value, err := quoteDockerfileWord(label.Value)
		if err != nil {
			return nil, fmt.Errorf("render image label %q: %w", label.Name, err)
		}
		fmt.Fprintf(&output, "LABEL %s=%s\n", name, value)
	}
	return output.Bytes(), nil
}

func validateMaterializationMountSources(mounts []providers.BuildMount, sources []MaterializationMountSource) (map[string]string, error) {
	expected := 0
	for _, mount := range mounts {
		if mount.SourceKind != providers.BuildMountSourcePrivateOutput {
			expected++
		}
	}
	if sources == nil || len(sources) != expected {
		return nil, fmt.Errorf("materialization mount sources do not match read-only transaction mounts")
	}
	result := make(map[string]string, len(sources))
	for index, source := range sources {
		if index > 0 && sources[index-1].ID >= source.ID {
			return nil, fmt.Errorf("materialization mount sources must be unique and sorted by ID")
		}
		if source.ContextPath == "" || path.IsAbs(source.ContextPath) || path.Clean(source.ContextPath) != source.ContextPath || source.ContextPath == "." || strings.Contains(source.ContextPath, `\`) {
			return nil, fmt.Errorf("materialization mount source %q must use a normalized relative context path", source.ID)
		}
		if err := validateMountOptionPath("source", source.ContextPath, false); err != nil {
			return nil, fmt.Errorf("materialization mount source %q: %w", source.ID, err)
		}
		result[source.ID] = source.ContextPath
	}
	for _, mount := range mounts {
		if mount.SourceKind == providers.BuildMountSourcePrivateOutput {
			if _, exists := result[mount.ID]; exists {
				return nil, fmt.Errorf("private-output mount %q must not have a context source", mount.ID)
			}
			continue
		}
		if _, exists := result[mount.ID]; !exists {
			return nil, fmt.Errorf("read-only mount %q has no context source", mount.ID)
		}
	}
	return result, nil
}

func validateMountOptionPath(field string, value string, absolute bool) error {
	if !utf8.ValidString(value) || strings.ContainsAny(value, ",= \t\r\n") {
		return fmt.Errorf("mount %s %q contains unsupported option characters", field, value)
	}
	if absolute != path.IsAbs(value) {
		return fmt.Errorf("mount %s %q has the wrong path form", field, value)
	}
	return nil
}

func quoteDockerfileWord(value string) (string, error) {
	if !utf8.ValidString(value) || strings.ContainsAny(value, "\x00\r\n") {
		return "", fmt.Errorf("value contains unsupported Dockerfile characters")
	}
	var output strings.Builder
	output.WriteByte('"')
	for _, char := range value {
		switch char {
		case '\\', '"', '$':
			output.WriteByte('\\')
		}
		output.WriteRune(char)
	}
	output.WriteByte('"')
	return output.String(), nil
}

func validateDockerfileAtom(field string, value string) error {
	if value == "" || !utf8.ValidString(value) {
		return fmt.Errorf("Dockerfile %s is required", field)
	}
	for _, char := range value {
		if char <= 0x20 || char == 0x7f || char == '\\' || char == '"' || char == '\'' || char == '$' || char == '#' {
			return fmt.Errorf("Dockerfile %s %q contains unsupported characters", field, value)
		}
	}
	return nil
}
