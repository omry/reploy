package dockerdeploy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/omry/reploy/internal/canonical"
)

// Temporary compatibility for Podman scheduling a timer for HEALTHCHECK NONE.
// Keep image metadata intact. Update only a container created by this operation,
// before the caller can initialize/start it. Never apply this to an old timer:
// clearing its interval also disables cleanup in the affected Podman releases.
type podmanHealthcheckClient struct {
	endpoint, version string
	http              *http.Client
	privateErrors     bool
}

var probePodmanHealthcheckClient = identifyPodmanHealthcheckClient

type podmanHealthcheckInspection struct {
	ID    string `json:"Id"`
	State struct {
		Status  string
		Running bool
		Pid     int
	}
	Config struct {
		Healthcheck        *dockerHealthcheck
		StartupHealthCheck *dockerHealthcheck
		OpenStdin          bool
		Labels             map[string]string
	}
}

func (client *podmanHealthcheckClient) request(ctx context.Context, method, path string, body []byte) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, method, "http://docker"+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := client.http.Do(request)
	if err != nil {
		return nil, fmt.Errorf("Podman healthcheck request: %w", err)
	}
	defer response.Body.Close()
	output, err := io.ReadAll(io.LimitReader(response.Body, imageHealthConfigLimit+1))
	if err != nil || int64(len(output)) > imageHealthConfigLimit {
		return nil, fmt.Errorf("read bounded Podman healthcheck response: %v", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		if client.privateErrors {
			return nil, fmt.Errorf("Podman request returned HTTP %d", response.StatusCode)
		}
		return nil, fmt.Errorf("Podman healthcheck request returned %s: %s", response.Status, trimmedCommandOutput(string(output)))
	}
	return output, nil
}

func identifyPodmanHealthcheckClient(ctx context.Context, endpoint string) (*podmanHealthcheckClient, error) {
	return identifyPodmanClientWithDialV1(ctx, endpoint, dialLocalDockerEndpointV1, false)
}

func identifyPodmanClientWithDialV1(ctx context.Context, endpoint string, dial func(context.Context, string) (net.Conn, error), privateErrors bool) (*podmanHealthcheckClient, error) {
	transport := &http.Transport{DialContext: func(c context.Context, _, _ string) (net.Conn, error) {
		return dial(c, endpoint)
	}}
	client := &podmanHealthcheckClient{endpoint: endpoint, privateErrors: privateErrors, http: &http.Client{
		Transport:     transport,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
	if privateErrors {
		client.http.Timeout = effectiveDockerPreflightTimeout(0)
	}
	output, err := client.request(ctx, http.MethodGet, "/version", nil)
	if err != nil {
		transport.CloseIdleConnections()
		return nil, err
	}
	var identity struct {
		Version, Os string
		Platform    struct{ Name string }
		Components  []struct{ Name, Version string }
	}
	if err := json.Unmarshal(output, &identity); err != nil {
		transport.CloseIdleConnections()
		return nil, fmt.Errorf("identify Podman healthcheck compatibility: %w", err)
	}
	count, docker := 0, false
	for _, component := range identity.Components {
		if component.Name == "Engine" {
			docker = true
		}
		if component.Name == "Podman Engine" {
			count++
			client.version = component.Version
		}
	}
	if count == 0 {
		transport.CloseIdleConnections()
		return nil, nil
	}
	if count != 1 || docker || client.version != identity.Version || identity.Os != "linux" ||
		runtime.GOOS != "linux" || strings.HasPrefix(identity.Platform.Name, "Docker") {
		transport.CloseIdleConnections()
		return nil, fmt.Errorf("ambiguous or unsupported Podman healthcheck engine identity")
	}
	client.version, _, _ = strings.Cut(client.version, "-")
	parts := strings.Split(client.version, ".")
	if len(parts) != 3 {
		transport.CloseIdleConnections()
		return nil, fmt.Errorf("invalid Podman healthcheck API version")
	}
	for _, part := range parts {
		if _, err := strconv.ParseUint(part, 10, 32); err != nil {
			transport.CloseIdleConnections()
			return nil, fmt.Errorf("invalid Podman healthcheck API version")
		}
	}
	return client, nil
}

func validPodmanHealthcheckContainerID(id string) bool {
	return len(id) == 64 && canonical.Digest("sha256:"+id).Validate() == nil
}

func (client *podmanHealthcheckClient) inspect(ctx context.Context, id string) (podmanHealthcheckInspection, error) {
	var inspection podmanHealthcheckInspection
	if !validPodmanHealthcheckContainerID(id) {
		return inspection, fmt.Errorf("Podman healthcheck requires an exact created container ID")
	}
	output, err := client.request(ctx, http.MethodGet, "/v"+client.version+"/libpod/containers/"+id+"/json", nil)
	if err != nil {
		return inspection, err
	}
	if err := json.Unmarshal(output, &inspection); err != nil || inspection.ID != id {
		return inspection, fmt.Errorf("Podman healthcheck container inspection identity mismatch: %v", err)
	}
	return inspection, nil
}

func podmanNoneHealthcheck(health *dockerHealthcheck) bool {
	return health != nil && len(health.Test) == 1 && strings.EqualFold(health.Test[0], "NONE")
}

func (client *podmanHealthcheckClient) prepare(ctx context.Context, id string) (podmanHealthcheckInspection, error) {
	before, err := client.inspect(ctx, id)
	if err != nil || !podmanNoneHealthcheck(before.Config.Healthcheck) {
		return before, err
	}
	if before.State.Running || before.State.Pid != 0 || before.State.Status != "created" ||
		before.Config.StartupHealthCheck != nil && len(before.Config.StartupHealthCheck.Test) != 0 {
		return before, fmt.Errorf("refuse to disable Podman healthcheck after container initialization/start")
	}
	output, err := client.request(ctx, http.MethodPost, "/v"+client.version+"/libpod/containers/"+id+"/update", []byte(`{"no_healthcheck":true}`))
	if err != nil {
		return before, err
	}
	if strings.TrimSpace(string(output)) != id {
		return before, fmt.Errorf("Podman healthcheck update did not confirm the created container ID")
	}
	after, err := client.inspect(ctx, id)
	if err != nil {
		return before, err
	}
	if !podmanNoneHealthcheck(after.Config.Healthcheck) || after.Config.Healthcheck.Interval != 0 ||
		after.State.Running || after.State.Pid != 0 || after.State.Status != "created" {
		return before, fmt.Errorf("Podman did not disable the created container's healthcheck timer")
	}
	return after, nil
}

func removeCreatedPodmanContainer(ctx context.Context, spec CommandSpec, id string, run commandRunner) error {
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 20*time.Second)
	defer cancel()
	spec.Args = []string{"container", "rm", "--force", id}
	err := run(spec, RunOptions{Context: cleanup})
	if err != nil && strings.Contains(strings.ToLower(err.Error()), "no such container") {
		return nil
	}
	return err
}
