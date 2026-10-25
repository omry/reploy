package dockerdeploy

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"slices"
	"strconv"

	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/identitymapping"
)

// Invocation-local preparation only. Later launch/lifecycle owners must supply
// their own admission; this seam neither starts payloads nor accepts mappings.
type preparedMappedPodmanCreateV1 struct {
	ops       deploy.IdentityMappingOperationsV1
	state     deploy.StateV1
	active    identitymapping.Geometry
	operation string
	spec      CommandSpec
	endpoint  string
	version   string
	namespace string
}

func prepareMappedPodmanCreateV1(ctx context.Context, ops deploy.IdentityMappingOperationsV1, expected deploy.StateV1, active identitymapping.Geometry, operationID string, spec CommandSpec) (*preparedMappedPodmanCreateV1, error) {
	state, err := ops.ObserveEffects(ctx, expected, active, operationID)
	if err != nil {
		return nil, err
	}
	active, err = identitymapping.ValidateGeometry(active)
	if err != nil {
		return nil, err
	}
	target, err := verifiedLocalDockerTargetV1(ctx, spec, defaultDockerPreflightTimeout)
	if err != nil {
		return nil, err
	}
	spec = pinDockerEndpointV1(spec, target.Endpoint)
	spec.Args, spec.Env = slices.Clone(spec.Args), slices.Clone(spec.Env)
	p := &preparedMappedPodmanCreateV1{ops: ops, state: state, active: active, operation: operationID, spec: spec, endpoint: target.Endpoint}
	client, namespace, err := p.observe(ctx)
	if err != nil {
		return nil, err
	}
	defer client.http.CloseIdleConnections()
	body, _, err := podmanApplicationCreateRequest(spec, namespace)
	if err != nil {
		return nil, err
	}
	// Storage preparation belongs to F; this internal seam cannot grant access
	// to external host binds or implicitly create persistent named volumes.
	for _, mount := range body.HostConfig.Mounts {
		if mount.Type != "tmpfs" {
			return nil, fmt.Errorf("mapped create requires separately admitted storage before host or persistent mounts")
		}
	}
	image, err := p.verifyImage(ctx, client)
	if err != nil {
		return nil, err
	}
	if body.Image != state.Current.Reference && body.Image != image {
		return nil, fmt.Errorf("mapped create image is not the selected generation or its immutable config ID")
	}
	p.version, p.namespace = client.version, namespace
	return p, nil
}

func (p *preparedMappedPodmanCreateV1) observe(ctx context.Context) (*podmanHealthcheckClient, string, error) {
	if p == nil || ctx == nil {
		return nil, "", fmt.Errorf("mapped create requires its invocation context and preparation")
	}
	if _, err := p.ops.ObserveEffects(ctx, p.state, p.active, p.operation); err != nil {
		return nil, "", err
	}
	m := p.state.IdentityMapping
	if os.Getuid() <= 0 || os.Getuid() != os.Geteuid() || os.Getgid() != os.Getegid() ||
		m.OperatorUID != strconv.Itoa(os.Geteuid()) || m.OperatorGID != strconv.Itoa(os.Getegid()) {
		return nil, "", fmt.Errorf("mapped create requires the selected owner and invoking non-root account")
	}
	// Bound only read-only identification and metadata. The returned client
	// must retain the later create/cleanup operation's own context deadline.
	ctx, cancel := context.WithTimeout(ctx, effectiveDockerPreflightTimeout(0))
	defer cancel()
	client, err := identifyPodmanClientWithDialV1(ctx, p.endpoint, dialMappedPodmanV1, true)
	if err != nil || client == nil {
		return nil, "", fmt.Errorf("mapped create requires a positively identified local Linux Podman engine: %v", err)
	}
	data, err := client.request(ctx, http.MethodGet, "/v"+client.version+"/libpod/info", nil)
	if err == nil {
		var namespace string
		namespace, err = compilePodmanMappingV1(data, p.active, uint32(os.Geteuid()), uint32(os.Getegid()))
		if err == nil {
			return client, namespace, nil
		}
	}
	client.http.CloseIdleConnections()
	return nil, "", err
}

func (p *preparedMappedPodmanCreateV1) create(ctx context.Context, raw commandRunner) (string, error) {
	if raw == nil {
		return "", fmt.Errorf("mapped create requires its existing owned cleanup runner")
	}
	client, namespace, err := p.observe(ctx)
	if err != nil {
		return "", err
	}
	defer client.http.CloseIdleConnections()
	if client.version != p.version || namespace != p.namespace {
		return "", fmt.Errorf("mapped Podman engine identity or parent namespace changed after preparation")
	}
	body, name, err := podmanApplicationCreateRequest(p.spec, namespace)
	if err != nil {
		return "", err
	}
	image, err := p.verifyImage(ctx, client)
	if err != nil {
		return "", err
	}
	// The generated input may use its owned generation tag. Only the freshly
	// verified immutable config ID crosses the effectful create boundary.
	body.Image = image
	// Metadata reads can take time. Refresh host and locked-state authority
	// after them, immediately before the potentially effectful request.
	if _, err := p.ops.ObserveEffects(ctx, p.state, p.active, p.operation); err != nil {
		return "", err
	}
	return client.createApplication(ctx, p.spec, body, name, body.Image, raw)
}
