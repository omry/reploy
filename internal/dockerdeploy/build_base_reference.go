package dockerdeploy

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/omry/reploy/internal/providers"
)

const temporaryBuildReferencePrefix = "reploy/build/"

// prepareTemporaryBuildBaseReference gives Dockerfile FROM a locally
// addressable name for an exact image ID. The name is private to one
// deployment workspace and exists only for the enclosing build operation.
func prepareTemporaryBuildBaseReference(
	ctx context.Context,
	storeRoot string,
	workspace string,
	image providers.RealizedImageV1,
	run dockerOutputRunner,
) (string, func(context.Context) error, error) {
	if ctx == nil {
		return "", nil, fmt.Errorf("prepare temporary build base reference requires a context")
	}
	if err := image.Validate(); err != nil {
		return "", nil, fmt.Errorf("prepare temporary build base reference: %w", err)
	}
	if run == nil {
		return "", nil, fmt.Errorf("prepare temporary build base reference requires a Docker runner")
	}
	reference, err := temporaryBuildBaseReference(storeRoot, workspace)
	if err != nil {
		return "", nil, err
	}
	output, err := run(ctx, "image", "ls", "--quiet", "--no-trunc", reference)
	if err != nil {
		return "", nil, fmt.Errorf("check temporary build base reference: %w", err)
	}
	if strings.TrimSpace(output) != "" {
		return "", nil, fmt.Errorf("temporary build base reference unexpectedly already exists")
	}
	if _, err := run(ctx, "image", "tag", string(image.ConfigDigest), reference); err != nil {
		return "", nil, fmt.Errorf("create temporary build base reference: %w", err)
	}
	cleanup := func(cleanupContext context.Context) error {
		return removeTemporaryBuildBaseReference(cleanupContext, reference, string(image.ConfigDigest), run)
	}
	output, inspectErr := run(ctx, "image", "inspect", "--format", "{{.Id}}", reference)
	if inspectErr == nil && strings.TrimSpace(output) == string(image.ConfigDigest) {
		return reference, cleanup, nil
	}
	cleanupErr := cleanup(context.WithoutCancel(ctx))
	if inspectErr != nil {
		if cleanupErr != nil {
			return "", nil, fmt.Errorf("verify temporary build base reference: %v; cleanup failed: %w", inspectErr, cleanupErr)
		}
		return "", nil, fmt.Errorf("verify temporary build base reference: %w", inspectErr)
	}
	mismatch := fmt.Errorf("Docker config ID is %q, want %q", strings.TrimSpace(output), image.ConfigDigest)
	if cleanupErr != nil {
		return "", nil, fmt.Errorf("verify temporary build base reference: %v; cleanup failed: %w", mismatch, cleanupErr)
	}
	return "", nil, fmt.Errorf("verify temporary build base reference: %w", mismatch)
}

func temporaryBuildBaseReference(storeRoot string, workspace string) (string, error) {
	return temporaryBuildReference(storeRoot, workspace, "")
}

func temporaryBuildOutputReference(storeRoot string, workspace string) (string, error) {
	return temporaryBuildReference(storeRoot, workspace, "output")
}

func temporaryBuildReference(storeRoot string, workspace string, role string) (string, error) {
	if storeRoot == "" || !filepath.IsAbs(storeRoot) || filepath.Clean(storeRoot) != storeRoot {
		return "", fmt.Errorf("temporary build base reference store root must be absolute and clean")
	}
	if workspace == "" || !filepath.IsAbs(workspace) || filepath.Clean(workspace) != workspace {
		return "", fmt.Errorf("temporary build base reference workspace must be absolute and clean")
	}
	relative, err := filepath.Rel(filepath.Join(storeRoot, "tmp"), workspace)
	if err != nil || relative == "." || filepath.Dir(relative) != "." || strings.HasPrefix(relative, "..") {
		return "", fmt.Errorf("temporary build base reference workspace must belong to the deployment provider store")
	}
	repository, err := temporaryBuildRepository(storeRoot)
	if err != nil {
		return "", fmt.Errorf("temporary build base reference deployment identity: %w", err)
	}
	suffix := dockerNameSlug(filepath.Base(workspace), "build")
	if role != "" {
		suffix += "-" + dockerNameSlug(role, "output")
	}
	return repository + ":" + suffix, nil
}

func temporaryBuildRepository(storeRoot string) (string, error) {
	directoryHash, err := pathIdentityHash(storeRoot)
	if err != nil {
		return "", err
	}
	return temporaryBuildReferencePrefix + directoryHash, nil
}

func validateTemporaryBuildReference(reference string) error {
	if reference == "" || strings.TrimSpace(reference) != reference ||
		!strings.HasPrefix(reference, temporaryBuildReferencePrefix) {
		return fmt.Errorf("temporary build reference is not Reploy-owned")
	}
	name, tag, found := strings.Cut(reference, ":")
	if !found || name == temporaryBuildReferencePrefix || tag == "" ||
		strings.Contains(tag, ":") {
		return fmt.Errorf("temporary build reference is malformed")
	}
	deployment := strings.TrimPrefix(name, temporaryBuildReferencePrefix)
	if len(deployment) != identityHashLength || strings.Contains(deployment, "/") {
		return fmt.Errorf("temporary build reference has an invalid deployment identity")
	}
	for _, character := range deployment {
		if !strings.ContainsRune("0123456789abcdef", character) {
			return fmt.Errorf("temporary build reference has an invalid deployment identity")
		}
	}
	if len(tag) > 128 {
		return fmt.Errorf("temporary build reference tag is too long")
	}
	for index, character := range tag {
		valid := character >= 'a' && character <= 'z' ||
			character >= '0' && character <= '9' ||
			index > 0 && strings.ContainsRune("_.-", character)
		if !valid {
			return fmt.Errorf("temporary build reference has an invalid tag")
		}
	}
	return nil
}

func prepareTemporaryBuildOutputReference(
	ctx context.Context,
	storeRoot string,
	workspace string,
	run dockerOutputRunner,
) (string, error) {
	if ctx == nil {
		return "", fmt.Errorf("prepare temporary build output reference requires a context")
	}
	if run == nil {
		return "", fmt.Errorf("prepare temporary build output reference requires a Docker runner")
	}
	reference, err := temporaryBuildOutputReference(storeRoot, workspace)
	if err != nil {
		return "", err
	}
	output, err := run(ctx, "image", "ls", "--quiet", "--no-trunc", reference)
	if err != nil {
		return "", fmt.Errorf("check temporary build output reference: %w", err)
	}
	if strings.TrimSpace(output) != "" {
		return "", fmt.Errorf("temporary build output reference unexpectedly already exists")
	}
	return reference, nil
}

func removeTemporaryBuildBaseReference(
	ctx context.Context,
	reference string,
	expectedConfigID string,
	run dockerOutputRunner,
) error {
	return removeTemporaryBuildReference(ctx, reference, expectedConfigID, run)
}

func removeTemporaryBuildReference(
	ctx context.Context,
	reference string,
	expectedConfigID string,
	run dockerOutputRunner,
) error {
	if ctx == nil {
		return fmt.Errorf("remove temporary build reference requires a context")
	}
	if err := validateTemporaryBuildReference(reference); err != nil {
		return fmt.Errorf("refuse to remove temporary build reference: %w", err)
	}
	if run == nil {
		return fmt.Errorf("remove temporary build reference requires a Docker runner")
	}
	output, err := run(ctx, "image", "ls", "--quiet", "--no-trunc", reference)
	if err != nil {
		return fmt.Errorf("inspect temporary build reference for removal: %w", err)
	}
	ids := strings.Fields(output)
	if len(ids) == 0 {
		return nil
	}
	if len(ids) != 1 || expectedConfigID != "" && ids[0] != expectedConfigID {
		return fmt.Errorf("temporary build reference no longer names its expected config ID")
	}
	if _, err := run(ctx, "image", "rm", reference); err != nil {
		return fmt.Errorf("remove temporary build reference: %w", err)
	}
	return nil
}

func cleanupTemporaryBuildBaseReferenceAfterBuild(
	ctx context.Context,
	cleanup func(context.Context) error,
	candidate BuiltImageCandidate,
	run dockerOutputRunner,
) error {
	cleanupErr := cleanup(ctx)
	if cleanupErr == nil {
		return nil
	}
	if candidate.ImageID == "" {
		return cleanupErr
	}
	// The workspace is the recovery record for both the base and output
	// references. A base-reference cleanup failure must retain that record even
	// when the output reference can be removed immediately.
	candidate.Workspace = ""
	if removeErr := removeBuiltImageCandidate(ctx, candidate, run); removeErr != nil {
		return fmt.Errorf("%v; remove unaccepted built image: %w", cleanupErr, removeErr)
	}
	return cleanupErr
}
