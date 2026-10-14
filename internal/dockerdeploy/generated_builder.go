package dockerdeploy

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"runtime"
	"strings"
	"sync"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/providerstore"
)

type generatedBuilderMode uint8

const (
	generatedBuildKit generatedBuilderMode = iota
	generatedPodmanAPI
)

type generatedImageBuilder struct {
	mode                                           generatedBuilderMode
	endpoint, engine, serverVersion, clientVersion string
}
type generatedBuilderSessionKey struct{}
type generatedBuilderSession struct {
	mu              sync.Mutex
	qualificationMu sync.Mutex
	builder         *generatedImageBuilder
	err             error
	attempted       bool
	qualified       map[string]bool
	healthMu        sync.Mutex
	healthchecks    map[string]*dockerHealthcheck
	healthEngine    *generatedImageBuilder
}

// One invocation owns one selection and its feature evidence. No global cache.
func withGeneratedBuilderSession(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if _, ok := ctx.Value(generatedBuilderSessionKey{}).(*generatedBuilderSession); ok {
		return ctx
	}
	return context.WithValue(ctx, generatedBuilderSessionKey{}, &generatedBuilderSession{qualified: map[string]bool{}})
}

var probeGeneratedImageBuilder = identifyGeneratedImageBuilder

func selectGeneratedImageBuilder(ctx context.Context, options RunOptions) (*generatedImageBuilder, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	session := ctx.Value(generatedBuilderSessionKey{}).(*generatedBuilderSession)
	session.mu.Lock()
	defer session.mu.Unlock()
	if !session.attempted {
		session.attempted = true
		session.builder, session.err = probeGeneratedImageBuilder(ctx, options)
		if session.err == nil {
			progress := options.Progress
			if progress == nil {
				progress, _ = ctx.Value(dockerPullProgressContextKey{}).(io.Writer)
			}
			writeProviderBuildProgress(progress, "Using %s for image builds", session.builder.engine)
		}
	}
	return session.builder, session.err
}
func currentGeneratedImageBuilder(ctx context.Context) *generatedImageBuilder {
	if ctx == nil {
		return nil
	}
	session, _ := ctx.Value(generatedBuilderSessionKey{}).(*generatedBuilderSession)
	if session == nil {
		return nil
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	return session.builder
}
func identifyGeneratedImageBuilder(ctx context.Context, options RunOptions) (*generatedImageBuilder, error) {
	ctx, cancel := context.WithTimeout(ctx, effectiveDockerPreflightTimeout(options.DockerPreflightTimeout))
	defer cancel()
	target, err := verifiedLocalDockerTargetV1(ctx, CommandSpec{Name: "docker", Env: []string{"DOCKER_BUILDKIT=1"}}, options.DockerPreflightTimeout)
	if err != nil {
		return nil, err
	}
	output, err := executeDockerOutputAtEndpoint(ctx, target.Endpoint, "version", "--format", "{{json .}}")
	if err != nil {
		return nil, fmt.Errorf("identify generated-image builder: %w", err)
	}
	builder, err := parseGeneratedImageBuilder(output)
	if err != nil {
		return nil, err
	}
	if builder.mode == generatedPodmanAPI && runtime.GOOS != "linux" {
		return nil, fmt.Errorf("generated Podman builds require a local Linux host")
	}
	builder.endpoint = target.Endpoint
	return builder, nil
}
func parseGeneratedImageBuilder(output string) (*generatedImageBuilder, error) {
	var response struct {
		Client struct{ Version string }
		Server struct {
			Version, Os string
			Platform    struct{ Name string }
			Components  []struct{ Name, Version string }
		}
	}
	if err := json.Unmarshal([]byte(output), &response); err != nil {
		return nil, fmt.Errorf("parse generated-image engine identity: %w", err)
	}
	if response.Server.Os != "linux" || response.Server.Version == "" || response.Client.Version == "" {
		return nil, fmt.Errorf("generated images require an identified Linux engine and Docker client")
	}
	engine, podman := "", ""
	for _, component := range response.Server.Components {
		switch component.Name {
		case "Engine":
			if engine != "" || component.Version == "" {
				return nil, fmt.Errorf("ambiguous Docker engine identity")
			}
			engine = component.Version
		case "Podman Engine":
			if podman != "" || component.Version == "" {
				return nil, fmt.Errorf("ambiguous Podman engine identity")
			}
			podman = component.Version
		}
	}
	if engine != "" && engine != response.Server.Version || podman != "" && podman != response.Server.Version {
		return nil, fmt.Errorf("conflicting generated-image engine versions")
	}
	builder := &generatedImageBuilder{serverVersion: response.Server.Version, clientVersion: response.Client.Version}
	if podman != "" {
		if strings.HasPrefix(response.Server.Platform.Name, "Docker") {
			return nil, fmt.Errorf("conflicting generated-image engine identity")
		}
		builder.mode, builder.engine = generatedPodmanAPI, "Podman"
	} else if engine != "" {
		if !minimumDockerVersion(engine, 24, 0) {
			return nil, fmt.Errorf("generated images require Docker Engine 24.0 or newer; daemon reports %s", engine)
		}
		builder.engine = "Docker"
	} else {
		return nil, fmt.Errorf("unknown generated-image engine; cannot select a compatible builder")
	}
	return builder, nil
}
func generatedBuilderModeOf(builder *generatedImageBuilder) generatedBuilderMode {
	if builder == nil {
		return generatedBuildKit
	}
	return builder.mode
}
func qualifyGeneratedImageBuilder(ctx context.Context, store providerstore.Store, base string, platform blueprint.Platform, options RunOptions, builder *generatedImageBuilder) error {
	if builder.mode != generatedPodmanAPI {
		return nil
	}
	session := ctx.Value(generatedBuilderSessionKey{}).(*generatedBuilderSession)
	// Qualification executes commands which read the selection from this context.
	// Keep that selection readable while serializing concurrent qualifications.
	session.qualificationMu.Lock()
	defer session.qualificationMu.Unlock()
	if session.qualified[platform.Canonical] {
		return nil
	}
	if err := probeGeneratedBuilderFeatures(ctx, store, base, platform, options, builder); err != nil {
		return fmt.Errorf("Podman generated-image build features are unsupported: %w", err)
	}
	session.qualified[platform.Canonical] = true
	return nil
}

// Carry the original selection into inspection and acceptance, including callers
// of standalone layer helpers. Reject a different selection in an active run.
func bindMaterializationBuilderContext(ctx context.Context, builder *generatedImageBuilder) (context.Context, error) {
	if selected := currentGeneratedImageBuilder(ctx); selected != nil {
		if builder == nil || *selected != *builder {
			return nil, fmt.Errorf("materialization builder does not match the invocation")
		}
		return ctx, nil
	}
	if builder == nil {
		return ctx, nil
	}
	if !localDockerEndpointV1(builder.endpoint) {
		return nil, fmt.Errorf("materialization builder endpoint is not local")
	}
	return context.WithValue(ctx, generatedBuilderSessionKey{}, &generatedBuilderSession{builder: builder, attempted: true, qualified: map[string]bool{}}), nil
}

func bindGenericMaterializationBuilderContext(ctx context.Context, builder *generatedImageBuilder) (context.Context, error) {
	if builder == nil {
		return ctx, nil
	}
	return bindMaterializationBuilderContext(ctx, builder)
}
