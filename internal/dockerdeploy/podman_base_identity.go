package dockerdeploy

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"runtime"
	"strconv"
	"strings"

	"github.com/distribution/reference"
	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/deploy"
)

// Podman 6.1.4's Docker-compatible pull omits Docker's manifest-digest message.
// The native 6.1.4 pull contract returns full IDs from the operation itself.
// Use that ID through the same selected local engine, without another registry
// connection or a guess from RepoDigests. This is a base-pull adapter, not a
// native runtime backend. The 6.1.4 route is also qualified on 6.2.0-dev.
const podmanBasePullPath = "/v6.1.4/libpod/images/pull"
const podmanBasePullResponseLimit = 64 << 10

func resolveNativePodmanBase(ctx context.Context, author string, platform blueprint.Platform) (deploy.ImageDescriptor, deploy.BaseConfig, bool, error) {
	builder := currentGeneratedImageBuilder(ctx)
	if builder == nil {
		var err error
		builder, err = identifyGeneratedImageBuilder(ctx, RunOptions{})
		if err != nil {
			return deploy.ImageDescriptor{}, deploy.BaseConfig{}, false, err
		}
	}
	if builder.mode != generatedPodmanAPI {
		return deploy.ImageDescriptor{}, deploy.BaseConfig{}, false, nil
	}
	if runtime.GOOS != "linux" || !supportsNativePodmanBasePull(builder.serverVersion) {
		return deploy.ImageDescriptor{}, deploy.BaseConfig{}, true, fmt.Errorf("native Podman base pull requires a supported local Linux Podman API (6.1.4 or newer)")
	}
	transport := &http.Transport{DialContext: func(c context.Context, _, _ string) (net.Conn, error) {
		return dialLocalDockerEndpointV1(c, builder.endpoint)
	}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	run := func(c context.Context, args ...string) (string, error) {
		return executeDockerOutputAtEndpoint(c, builder.endpoint, args...)
	}
	descriptor, config, err := resolvePodmanBaseFromPull(ctx, author, platform, client, run)
	return descriptor, config, true, err
}

func resolvePodmanBaseFromPull(ctx context.Context, author string, platform blueprint.Platform, client *http.Client, inspect dockerOutputRunner) (deploy.ImageDescriptor, deploy.BaseConfig, error) {
	named, err := reference.ParseNormalizedNamed(author)
	if err != nil {
		return deploy.ImageDescriptor{}, deploy.BaseConfig{}, fmt.Errorf("normalize Podman base reference: %w", err)
	}
	named = reference.TagNameOnly(named)
	auth, err := podmanBaseRegistryAuth(named)
	if err != nil {
		return deploy.ImageDescriptor{}, deploy.BaseConfig{}, err
	}
	query := url.Values{"reference": {named.String()}, "quiet": {"true"}, "policy": {"always"}, "OS": {platform.OS}, "Arch": {platform.Architecture}, "retry": {"2"}, "retrydelay": {"500ms"}}
	if platform.Variant != "" {
		query.Set("Variant", platform.Variant)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://docker"+podmanBasePullPath+"?"+query.Encode(), nil)
	if err != nil {
		return deploy.ImageDescriptor{}, deploy.BaseConfig{}, err
	}
	request.Header.Set("X-Registry-Auth", base64.URLEncoding.EncodeToString(auth))
	progress, _ := ctx.Value(dockerPullProgressContextKey{}).(io.Writer)
	writeProviderBuildProgress(progress, "Pulling base image with Podman")
	response, err := client.Do(request)
	if err != nil {
		return deploy.ImageDescriptor{}, deploy.BaseConfig{}, fmt.Errorf("native Podman base pull: %w", err)
	}
	defer response.Body.Close()
	// Never include engine error bodies: registry or helper failures can contain
	// credentials. HTTP failure and invalid success evidence fail closed.
	if response.StatusCode != http.StatusOK {
		return deploy.ImageDescriptor{}, deploy.BaseConfig{}, fmt.Errorf("native Podman base pull returned HTTP %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, podmanBasePullResponseLimit+1))
	if err != nil {
		return deploy.ImageDescriptor{}, deploy.BaseConfig{}, fmt.Errorf("read native Podman base pull: %w", err)
	}
	if len(data) > podmanBasePullResponseLimit {
		return deploy.ImageDescriptor{}, deploy.BaseConfig{}, fmt.Errorf("native Podman base pull response exceeds 64 KiB")
	}
	var result struct {
		ID     string   `json:"id"`
		Images []string `json:"images"`
		Error  string   `json:"error"`
		Status string   `json:"status"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return deploy.ImageDescriptor{}, deploy.BaseConfig{}, fmt.Errorf("invalid native Podman base pull response")
	}
	id := canonical.Digest("sha256:" + result.ID)
	if result.Error != "" || result.Status != "" && result.Status != "success" || len(result.Images) != 1 || result.Images[0] != result.ID || id.Validate() != nil {
		return deploy.ImageDescriptor{}, deploy.BaseConfig{}, fmt.Errorf("native Podman base pull did not return one matching full image ID")
	}
	output, err := inspect(ctx, "image", "inspect", string(id))
	if err != nil {
		return deploy.ImageDescriptor{}, deploy.BaseConfig{}, fmt.Errorf("inspect native-pulled Podman base: %w", err)
	}
	descriptor, config, err := parseResolvedDockerBase(string(id), platform, []byte(output), "")
	if err != nil {
		return deploy.ImageDescriptor{}, deploy.BaseConfig{}, err
	}
	if pinned, ok := dockerPinnedManifestDigest(author); ok {
		// The pull used the author's immutable subject. Retain the existing pinned
		// validator and cache representation, including index pins.
		descriptor, config, err = parseResolvedDockerBase(author, platform, []byte(output), pinned)
		if err != nil {
			return deploy.ImageDescriptor{}, deploy.BaseConfig{}, err
		}
	}
	descriptor.AuthorReference = author
	if err := descriptor.Validate(); err != nil {
		return deploy.ImageDescriptor{}, deploy.BaseConfig{}, err
	}
	return descriptor, config, nil
}

// Keep the compatibility floor explicit. Pre-release suffixes occur on the
// qualified development engine, but missing or malformed versions fail closed.
func supportsNativePodmanBasePull(value string) bool {
	core, _, _ := strings.Cut(strings.TrimPrefix(value, "v"), "-")
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return false
	}
	var version [3]int
	for i, part := range parts {
		n, err := strconv.Atoi(part)
		if err != nil || n < 0 {
			return false
		}
		version[i] = n
	}
	return version[0] > 6 || version[0] == 6 && (version[1] > 1 || version[1] == 1 && version[2] >= 4)
}
