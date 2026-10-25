package dockerdeploy

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"path/filepath"
	"reflect"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providerstore"
)

// Image admission is invocation-local and uses the same peer-checked client as
// create. Ordinary portable builds and their builder selection stay unchanged.
func (p *preparedMappedPodmanCreateV1) verifyImage(ctx context.Context, client *podmanHealthcheckClient) (string, error) {
	if p == nil || ctx == nil || client == nil || client.endpoint != p.endpoint || !localDockerEndpointV1(p.endpoint) {
		return "", fmt.Errorf("mapped image verification requires the selected local endpoint")
	}
	ctx, cancel := context.WithTimeout(ctx, effectiveDockerPreflightTimeout(0))
	defer cancel()
	if err := p.ops.Operation.RequireHeld(); err != nil {
		return "", err
	}
	dir := filepath.Dir(filepath.Dir(p.ops.Operation.Path()))
	store, err := providerstore.NewStore(dir)
	if err != nil {
		return "", err
	}
	document, err := blueprint.DecodeResolvedDocumentV1(p.state.Blueprint)
	if err != nil || p.state.Current == nil {
		return "", fmt.Errorf("mapped image requires a recorded current generation")
	}
	verify := func(ctx context.Context, image providers.RealizedImageV1, reference, _, _ string) error {
		return client.verifyMappedImageReference(ctx, reference, image, p.state.Current.Platform)
	}
	current, found, err := validateCurrentBuild(ctx, p.ops.Operation, store, document.Environment.ID, dir, verify)
	if err != nil {
		return "", fmt.Errorf("mapped current image: %w", err)
	}
	if !found || !reflect.DeepEqual(current.State, p.state) {
		return "", fmt.Errorf("mapped image generation or selected owner changed")
	}
	// Portable companions retain their existing owned-reference requirement.
	pairs, err := ProjectEnvironmentOwnedReferencesV1(current.Generation, current.Lock, document.Environment.ID, dir)
	if err != nil {
		return "", err
	}
	for _, pair := range pairs[1:] {
		if err := verify(ctx, pair.Image, pair.Reference, "", ""); err != nil {
			return "", err
		}
	}
	return string(current.Lock.FinalImage.ConfigDigest), nil
}

func (client *podmanHealthcheckClient) verifyMappedImageReference(ctx context.Context, reference string, image providers.RealizedImageV1, platform blueprint.Platform) error {
	// Bracket the immutable-ID observation with owned-tag observations. A tag
	// change during admission rejects; later changes cannot redirect create.
	for _, selected := range []string{reference, string(image.ConfigDigest), reference} {
		data, err := client.request(ctx, http.MethodGet, "/v1.44/images/"+url.PathEscape(selected)+"/json", nil)
		if err != nil {
			return fmt.Errorf("inspect mapped owned image: %w", err)
		}
		// The compatible API returns one object; reuse the CLI parser's single
		// record checks without a second image format or reference authority.
		inspection, err := parseDockerImageInspectionDetails(string(image.ConfigDigest), platform, append(append([]byte{'['}, data...), ']'))
		if err != nil {
			// Image config can contain application environment values.
			return fmt.Errorf("mapped image inspection does not match its locked config and platform")
		}
		rootFS, err := deploy.RootFSSubject(inspection.Descriptor.RootFSDiffIDs)
		if err != nil || rootFS != image.RootFSSubject {
			return fmt.Errorf("mapped image root filesystem differs from its build lock")
		}
	}
	return nil
}
