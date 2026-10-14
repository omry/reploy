package dockerdeploy

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/providerstore"
)

// Use a verified engine-local base, the shared build command, and fresh inputs.
// Never start the proof container: copy its RUN result from its filesystem.
type generatedBuilderProbeBackend struct {
	docker dockerOutputRunner
	build  commandRunner
}

func probeGeneratedBuilderFeatures(ctx context.Context, store providerstore.Store, base string, platform blueprint.Platform, options RunOptions, builder *generatedImageBuilder) error {
	return probeGeneratedBuilderFeaturesWithBackend(ctx, store, base, platform, options, builder, generatedBuilderProbeBackend{
		docker: func(c context.Context, args ...string) (string, error) {
			return executeDockerOutputAtEndpoint(c, builder.endpoint, args...)
		},
		build: runDockerBuildWithFrontendRetry,
	})
}

func probeGeneratedBuilderFeaturesWithBackend(ctx context.Context, store providerstore.Store, base string, platform blueprint.Platform, options RunOptions, builder *generatedImageBuilder, backend generatedBuilderProbeBackend) (resultErr error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	workspace, err := store.NewWorkspace("builder-probe-*")
	if err != nil {
		return err
	}
	run := backend.docker
	reference, err := prepareTemporaryBuildOutputReference(ctx, store.Root(), workspace, run)
	if err != nil {
		_ = os.RemoveAll(workspace)
		return err
	}
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		_ = os.RemoveAll(workspace)
		return err
	}
	proof := hex.EncodeToString(random[:])
	name := "reploy-builder-probe-" + proof
	var imageID string
	defer func() {
		cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), 20*time.Second)
		defer stop()
		probeErr := resultErr
		var cleanupErr error
		// Recover a successful create even if cancellation hid its response.
		container, inspectErr := run(cleanup, "container", "inspect", "--format", "{{.Id}}\t{{index .Config.Labels \"io.reploy.builder-probe\"}}", name)
		if inspectErr == nil {
			fields := strings.Fields(container)
			if len(fields) != 2 || fields[1] != proof || canonical.Digest("sha256:"+fields[0]).Validate() != nil {
				cleanupErr = errors.Join(cleanupErr, fmt.Errorf("refuse cleanup of unowned builder probe container %q", name))
			} else if _, err := run(cleanup, "container", "rm", "--force", fields[0]); err != nil {
				cleanupErr = errors.Join(cleanupErr, err)
			}
		} else if !dockerImageInspectReportsMissing(inspectErr) && !strings.Contains(strings.ToLower(inspectErr.Error()), "no such container") {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("inspect builder probe cleanup %q: %w", name, inspectErr))
		}
		cleanupErr = errors.Join(cleanupErr, removeTemporaryBuildReference(cleanup, reference, imageID, run))
		if cleanupErr != nil {
			resultErr = errors.Join(probeErr, cleanupErr, fmt.Errorf("builder probe evidence retained at %s", workspace))
			return
		}
		if workspaceErr := os.RemoveAll(workspace); workspaceErr != nil {
			resultErr = errors.Join(probeErr, fmt.Errorf("remove builder probe workspace: %w", workspaceErr), fmt.Errorf("builder probe evidence retained at %s", workspace))
			return
		}
		resultErr = probeErr
	}()
	if err := os.WriteFile(filepath.Join(workspace, "proof"), []byte(proof), 0o600); err != nil {
		return err
	}
	file := "# syntax=" + MaterializationDockerfileSyntax + "\nARG REPLOY_BASE_IMAGE\nFROM ${REPLOY_BASE_IMAGE}\nUSER 0:0\n" +
		"RUN --network=none --mount=type=bind,source=proof,target=/.reploy-build/qualification,readonly /bin/sh -eu -c '" +
		"test \"$(ls /sys/class/net)\" = lo; test \"$(cat /.reploy-build/qualification)\" = " + proof +
		"; if printf changed > /.reploy-build/qualification; then exit 1; fi; printf " + proof + " > /reploy-builder-proof'\n"
	filePath, iidPath := filepath.Join(workspace, "Dockerfile"), filepath.Join(workspace, "result.iid")
	if err := os.WriteFile(filePath, []byte(file), 0o600); err != nil {
		return err
	}
	spec, err := MaterializationBuildCommand(MaterializationBuildPlan{
		BaseReference: base, OutputReference: reference, Platform: platform,
		DockerfilePath: filePath, ContextDir: workspace, IIDFile: iidPath, NoCache: true, builder: builder,
	})
	if err != nil {
		return err
	}
	options.Context = ctx
	if err := backend.build(spec, options); err != nil {
		return err
	}
	content, err := os.ReadFile(iidPath)
	if err != nil {
		return fmt.Errorf("builder probe result is not loaded: %w", err)
	}
	imageID = strings.TrimSpace(string(content))
	if err := canonical.Digest(imageID).Validate(); err != nil {
		imageID = ""
		return fmt.Errorf("builder probe image identity: %w", err)
	}
	observed, err := run(ctx, "image", "inspect", "--format", "{{.Id}}", reference)
	if err != nil || strings.TrimSpace(observed) != imageID {
		return fmt.Errorf("builder probe local image identity mismatch: %q: %v", observed, err)
	}
	if _, err := run(ctx, "container", "create", "--name", name, "--label", "io.reploy.builder-probe="+proof, "--entrypoint", "/bin/true", imageID); err != nil {
		return err
	}
	output := filepath.Join(workspace, "observed-proof")
	if _, err := run(ctx, "container", "cp", name+":/reploy-builder-proof", output); err != nil {
		return err
	}
	content, err = os.ReadFile(output)
	if err != nil || string(content) != proof {
		return fmt.Errorf("builder did not execute offline/read-only RUN proof: %v", err)
	}
	return nil
}
