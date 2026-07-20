package dockerdeploy

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providerstore"
)

func TestPrepareProviderBasePlansResolvesAndRealizesExactRequest(t *testing.T) {
	request := providerBaseResolvedRequest(t)
	descriptor := providerBaseDescriptor(t, true)
	config := deploy.BaseConfig{
		Schema: deploy.BaseConfigSchemaV1, Environment: []deploy.ConfigEnvironmentVariable{},
		Entrypoint: []string{}, Command: []string{}, OnBuild: []string{}, Volumes: []string{},
	}
	image := providers.RealizedImageV1{
		Digest: descriptor.ManifestDigest, ConfigDigest: descriptor.ConfigDigest, RootFSSubject: rendererDigest("9"),
	}
	catalog := []providers.RealizedOutput{}
	previousResolve := resolveProviderBaseImage
	previousRealize := realizePreparedProviderBase
	t.Cleanup(func() {
		resolveProviderBaseImage = previousResolve
		realizePreparedProviderBase = previousRealize
	})
	resolveCalls := 0
	resolveProviderBaseImage = func(_ context.Context, authorReference string, platform blueprint.Platform) (deploy.ImageDescriptor, deploy.BaseConfig, error) {
		resolveCalls++
		if authorReference != "debian:bookworm-slim" || platform != request.Platform {
			t.Fatalf("resolve base input = %q, %#v", authorReference, platform)
		}
		return descriptor, config, nil
	}
	realizeCalls := 0
	realizePreparedProviderBase = func(_ context.Context, _ providerstore.Store, plan providers.ProviderPlanV1, gotDescriptor deploy.ImageDescriptor) (providers.RealizedImageV1, []providers.RealizedOutput, error) {
		realizeCalls++
		if !reflect.DeepEqual(gotDescriptor, descriptor) || len(plan.Nodes) != 1 || plan.Nodes[0].ID != "base" {
			t.Fatalf("realize base input = %#v, %#v", plan, gotDescriptor)
		}
		return image, catalog, nil
	}
	prepared, err := PrepareProviderBase(context.Background(), providerstore.Store{}, request)
	if err != nil {
		t.Fatal(err)
	}
	if resolveCalls != 1 || realizeCalls != 1 || !reflect.DeepEqual(prepared.Descriptor, descriptor) || !reflect.DeepEqual(prepared.Config, config) || prepared.Image != image || prepared.Catalog == nil {
		t.Fatalf("calls/prepared = %d/%d/%#v", resolveCalls, realizeCalls, prepared)
	}
}

func TestSelectProviderBaseDefersOutputRealization(t *testing.T) {
	request := providerBaseResolvedRequest(t)
	descriptor := providerBaseDescriptor(t, true)
	config := deploy.BaseConfig{
		Schema: deploy.BaseConfigSchemaV1, Environment: []deploy.ConfigEnvironmentVariable{},
		Entrypoint: []string{}, Command: []string{}, OnBuild: []string{}, Volumes: []string{},
	}
	image := providers.RealizedImageV1{
		Digest: descriptor.ManifestDigest, ConfigDigest: descriptor.ConfigDigest, RootFSSubject: rendererDigest("9"),
	}
	previousResolve := resolveProviderBaseImage
	previousRealize := realizePreparedProviderBase
	t.Cleanup(func() {
		resolveProviderBaseImage = previousResolve
		realizePreparedProviderBase = previousRealize
	})
	resolveProviderBaseImage = func(context.Context, string, blueprint.Platform) (deploy.ImageDescriptor, deploy.BaseConfig, error) {
		return descriptor, config, nil
	}
	realizeCalls := 0
	realizePreparedProviderBase = func(context.Context, providerstore.Store, providers.ProviderPlanV1, deploy.ImageDescriptor) (providers.RealizedImageV1, []providers.RealizedOutput, error) {
		realizeCalls++
		return image, []providers.RealizedOutput{}, nil
	}

	selected, err := SelectProviderBase(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if realizeCalls != 0 || !reflect.DeepEqual(selected.Descriptor, descriptor) || !reflect.DeepEqual(selected.Config, config) {
		t.Fatalf("selection realized outputs or changed base: calls=%d selected=%#v", realizeCalls, selected)
	}
	prepared, err := RealizeSelectedProviderBase(context.Background(), providerstore.Store{}, selected)
	if err != nil {
		t.Fatal(err)
	}
	if realizeCalls != 1 || prepared.Image != image || prepared.Catalog == nil {
		t.Fatalf("realization calls/result = %d/%#v", realizeCalls, prepared)
	}
}

func TestPrepareProviderBaseRejectsInvalidRequestBeforeDocker(t *testing.T) {
	request := providerBaseResolvedRequest(t)
	request.Components[0].Request.Value["image"] = ""
	previous := resolveProviderBaseImage
	t.Cleanup(func() { resolveProviderBaseImage = previous })
	resolveProviderBaseImage = func(context.Context, string, blueprint.Platform) (deploy.ImageDescriptor, deploy.BaseConfig, error) {
		t.Fatal("invalid request reached Docker")
		return deploy.ImageDescriptor{}, deploy.BaseConfig{}, nil
	}
	if _, err := PrepareProviderBase(context.Background(), providerstore.Store{}, request); err == nil {
		t.Fatal("invalid request was accepted")
	}
}

func TestPrepareProviderBaseStopsAfterResolveOrRealizeFailure(t *testing.T) {
	request := providerBaseResolvedRequest(t)
	previousResolve := resolveProviderBaseImage
	previousRealize := realizePreparedProviderBase
	t.Cleanup(func() {
		resolveProviderBaseImage = previousResolve
		realizePreparedProviderBase = previousRealize
	})
	t.Run("resolve", func(t *testing.T) {
		resolveProviderBaseImage = func(context.Context, string, blueprint.Platform) (deploy.ImageDescriptor, deploy.BaseConfig, error) {
			return deploy.ImageDescriptor{}, deploy.BaseConfig{}, errors.New("injected resolve failure")
		}
		if _, err := PrepareProviderBase(context.Background(), providerstore.Store{}, request); err == nil || !strings.Contains(err.Error(), "resolve failure") {
			t.Fatalf("error = %v", err)
		}
	})
	t.Run("realize", func(t *testing.T) {
		descriptor := providerBaseDescriptor(t, true)
		resolveProviderBaseImage = func(context.Context, string, blueprint.Platform) (deploy.ImageDescriptor, deploy.BaseConfig, error) {
			return descriptor, deploy.BaseConfig{
				Schema: deploy.BaseConfigSchemaV1, Environment: []deploy.ConfigEnvironmentVariable{},
				Entrypoint: []string{}, Command: []string{}, OnBuild: []string{}, Volumes: []string{},
			}, nil
		}
		realizePreparedProviderBase = func(context.Context, providerstore.Store, providers.ProviderPlanV1, deploy.ImageDescriptor) (providers.RealizedImageV1, []providers.RealizedOutput, error) {
			return providers.RealizedImageV1{}, nil, errors.New("injected realize failure")
		}
		if _, err := PrepareProviderBase(context.Background(), providerstore.Store{}, request); err == nil || !strings.Contains(err.Error(), "realize failure") {
			t.Fatalf("error = %v", err)
		}
	})
}

func providerBaseResolvedRequest(t *testing.T) providers.ResolvedRequestV1 {
	t.Helper()
	platform, err := blueprint.ParsePlatform("linux/amd64")
	if err != nil {
		t.Fatal(err)
	}
	base, err := providers.CanonicalBaseProviderRequestV1(providers.BaseProviderRequestV1{
		Image: "debian:bookworm-slim", Exports: map[string]blueprint.BaseExecutableExport{},
	})
	if err != nil {
		t.Fatal(err)
	}
	return providers.ResolvedRequestV1{
		Schema: providers.ResolvedRequestSchemaV1, OverlayDigest: rendererDigest("b"),
		Platform:   platform,
		Components: []providers.ResolvedComponentRequestV1{{Component: "base", Provider: blueprint.ComponentTypeBase, Request: base}},
		Sources:    []providers.ResolvedSourceInput{},
	}
}

func TestResolvedRequestBaseReferenceReadsValidatedBase(t *testing.T) {
	request := providerBaseResolvedRequest(t)
	reference, err := resolvedRequestBaseReference(request)
	if err != nil || reference != "debian:bookworm-slim" {
		t.Fatalf("reference/error = %q/%v", reference, err)
	}
	request.Components = []providers.ResolvedComponentRequestV1{}
	if _, err := resolvedRequestBaseReference(request); err == nil {
		t.Fatal("missing base was accepted")
	}
}
