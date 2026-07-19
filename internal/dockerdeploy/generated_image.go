package dockerdeploy

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/omry/reploy/internal/providers"
)

const generatedImageDockerfileSyntax = "docker/dockerfile:1.7"

type GeneratedImagePlan struct {
	BaseImage       string
	BaseIdentity    string
	Tag             string
	BundleDir       string
	Materialization providers.Materialization
	Labels          map[string]string
}

var runGeneratedImageCommand = runCommand

func BuildGeneratedImage(plan GeneratedImagePlan, options RunOptions) error {
	dockerfile, err := GeneratedImageDockerfile(plan)
	if err != nil {
		return err
	}
	file, err := os.CreateTemp("", "reploy-generated-*.Dockerfile")
	if err != nil {
		return fmt.Errorf("create generated Dockerfile: %w", err)
	}
	name := file.Name()
	defer os.Remove(name)
	if _, err := file.Write(dockerfile); err != nil {
		file.Close()
		return fmt.Errorf("write generated Dockerfile: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close generated Dockerfile: %w", err)
	}
	contextDir, cleanup, err := prepareGeneratedBuildContext(plan.BundleDir, plan.Materialization.Artifacts)
	if err != nil {
		return err
	}
	defer cleanup()
	buildPlan := plan
	buildPlan.BundleDir = contextDir
	command, err := GeneratedImageBuildCommand(buildPlan, name)
	if err != nil {
		return err
	}
	if err := runGeneratedImageCommand(command, options); err != nil {
		return fmt.Errorf("build generated image %s: %w", plan.Tag, err)
	}
	return nil
}

func prepareGeneratedBuildContext(bundleDir string, artifacts []providers.Artifact) (string, func(), error) {
	if len(artifacts) == 0 {
		return bundleDir, func() {}, nil
	}
	contextDir, err := os.MkdirTemp("", "reploy-build-context-*")
	if err != nil {
		return "", func() {}, fmt.Errorf("create generated image context: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(contextDir) }
	for _, artifact := range artifacts {
		source := filepath.Join(bundleDir, filepath.FromSlash(artifact.Path))
		info, err := os.Lstat(source)
		if err != nil {
			cleanup()
			return "", func() {}, fmt.Errorf("read generated image artifact %s: %w", artifact.Path, err)
		}
		if !info.Mode().IsRegular() {
			cleanup()
			return "", func() {}, fmt.Errorf("generated image artifact must be a regular file: %s", artifact.Path)
		}
		target := filepath.Join(contextDir, filepath.FromSlash(artifact.Path))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			cleanup()
			return "", func() {}, err
		}
		input, err := os.Open(source)
		if err != nil {
			cleanup()
			return "", func() {}, err
		}
		output, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err != nil {
			input.Close()
			cleanup()
			return "", func() {}, err
		}
		hash := sha256.New()
		_, copyErr := io.Copy(io.MultiWriter(output, hash), input)
		closeInputErr := input.Close()
		closeOutputErr := output.Close()
		if copyErr != nil || closeInputErr != nil || closeOutputErr != nil {
			cleanup()
			return "", func() {}, fmt.Errorf("copy generated image artifact %s", artifact.Path)
		}
		if got := fmt.Sprintf("%x", hash.Sum(nil)); got != artifact.SHA256 {
			cleanup()
			return "", func() {}, fmt.Errorf("generated image artifact checksum changed for %s: got %s, want %s", artifact.Path, got, artifact.SHA256)
		}
	}
	return contextDir, cleanup, nil
}

// GeneratedImageDockerfile renders provider-owned argv only. Blueprint and
// user strings never become shell source.
func GeneratedImageDockerfile(plan GeneratedImagePlan) ([]byte, error) {
	if strings.TrimSpace(plan.BaseImage) == "" {
		return nil, fmt.Errorf("generated image base image is required")
	}
	materialization := plan.Materialization
	if materialization.Provider == "" || materialization.Version == "" || materialization.BundleMount == "" || len(materialization.Steps) == 0 {
		return nil, fmt.Errorf("generated image materialization is incomplete")
	}
	if !strings.HasPrefix(materialization.BundleMount, "/") {
		return nil, fmt.Errorf("generated image bundle mount must be absolute")
	}
	var output bytes.Buffer
	fmt.Fprintf(&output, "# syntax=%s\n", generatedImageDockerfileSyntax)
	output.WriteString("ARG REPLOY_BASE_IMAGE\n")
	output.WriteString("FROM ${REPLOY_BASE_IMAGE}\n")
	for _, step := range materialization.Steps {
		if len(step.Argv) == 0 || step.Argv[0] == "" {
			return nil, fmt.Errorf("generated image materialization step has empty argv")
		}
		argv, err := json.Marshal(step.Argv)
		if err != nil {
			return nil, err
		}
		mount := "--mount=type=bind,target=" + materialization.BundleMount + ",readonly"
		fmt.Fprintf(&output, "RUN %s %s\n", mount, argv)
	}
	return output.Bytes(), nil
}

func GeneratedImageBuildCommand(plan GeneratedImagePlan, dockerfilePath string) (CommandSpec, error) {
	if strings.TrimSpace(plan.Tag) == "" {
		return CommandSpec{}, fmt.Errorf("generated image tag is required")
	}
	if strings.TrimSpace(plan.BaseIdentity) == "" {
		return CommandSpec{}, fmt.Errorf("generated image immutable base identity is required")
	}
	bundleDir, err := filepath.Abs(plan.BundleDir)
	if err != nil {
		return CommandSpec{}, err
	}
	if dockerfilePath == "" {
		return CommandSpec{}, fmt.Errorf("generated Dockerfile path is required")
	}
	args := []string{
		"build", "--file", dockerfilePath,
		"--tag", plan.Tag,
		"--build-arg", "REPLOY_BASE_IMAGE=" + plan.BaseIdentity,
	}
	labelNames := make([]string, 0, len(plan.Labels))
	for name := range plan.Labels {
		labelNames = append(labelNames, name)
	}
	sort.Strings(labelNames)
	for _, name := range labelNames {
		args = append(args, "--label", name+"="+plan.Labels[name])
	}
	args = append(args, bundleDir)
	return CommandSpec{Name: "docker", Args: args, Env: []string{"DOCKER_BUILDKIT=1"}}, nil
}
