package dockerdeploy

import (
	"context"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/probe"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providerstore"
)

func TestRealizeProviderBaseUsesManifestIdentityAndOneExportBatch(t *testing.T) {
	descriptor := providerBaseDescriptor(t, true)
	plan := providerBasePlan(t, map[string]string{
		"pip": "/usr/bin/pip3", "python": "/usr/bin/python3",
	})
	previous := collectProviderBaseExecutableEvidence
	t.Cleanup(func() { collectProviderBaseExecutableEvidence = previous })
	calls := 0
	collectProviderBaseExecutableEvidence = func(
		_ context.Context, _ providerstore.Store, gotDescriptor deploy.ImageDescriptor, checks []FullImageExecutableProbe,
	) ([]providers.ExecutableEvidence, error) {
		calls++
		if gotDescriptor.ImmutableReference != descriptor.ImmutableReference || len(checks) != 2 {
			t.Fatalf("base probe input = %#v, %#v", gotDescriptor, checks)
		}
		result := make([]providers.ExecutableEvidence, 0, len(checks))
		for _, check := range checks {
			observation := providerBaseObservation(check.ID, check.InvocationPath)
			evidence, err := ExecutableEvidenceFromProbe(observation, check.Binding)
			if err != nil {
				t.Fatal(err)
			}
			result = append(result, evidence)
		}
		return result, nil
	}
	image, catalog, err := RealizeProviderBase(context.Background(), providerstore.Store{}, plan, descriptor)
	if err != nil {
		t.Fatal(err)
	}
	wantSubject, err := deploy.RootFSSubject(descriptor.RootFSDiffIDs)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || image.Digest != descriptor.ManifestDigest || image.ConfigDigest != descriptor.ConfigDigest || image.RootFSSubject != wantSubject {
		t.Fatalf("calls/image = %d/%#v", calls, image)
	}
	if len(catalog) != 2 || catalog[0].Name != "pip" || catalog[1].Name != "python" {
		t.Fatalf("catalog = %#v", catalog)
	}
	for _, output := range catalog {
		if output.Evidence.Output != (providers.QualifiedOutput{Component: "base", Name: output.Name}) || output.Evidence.Facts.Schema != providers.BaseExportSchemaV1 {
			t.Fatalf("base output evidence = %#v", output.Evidence)
		}
	}
}

func TestRealizeProviderBaseUsesConfigIdentityForLocalImageAndSkipsEmptyProbe(t *testing.T) {
	descriptor := providerBaseDescriptor(t, false)
	plan := providerBasePlan(t, map[string]string{})
	previous := collectProviderBaseExecutableEvidence
	t.Cleanup(func() { collectProviderBaseExecutableEvidence = previous })
	collectProviderBaseExecutableEvidence = func(_ context.Context, _ providerstore.Store, _ deploy.ImageDescriptor, checks []FullImageExecutableProbe) ([]providers.ExecutableEvidence, error) {
		if len(checks) != 0 {
			t.Fatalf("checks = %#v", checks)
		}
		return []providers.ExecutableEvidence{}, nil
	}
	image, catalog, err := RealizeProviderBase(context.Background(), providerstore.Store{}, plan, descriptor)
	if err != nil {
		t.Fatal(err)
	}
	if image.Digest != descriptor.ConfigDigest || catalog == nil || len(catalog) != 0 {
		t.Fatalf("image/catalog = %#v/%#v", image, catalog)
	}
}

func TestRealizeProviderBaseRejectsMissingOrExtraBatchedEvidence(t *testing.T) {
	descriptor := providerBaseDescriptor(t, true)
	plan := providerBasePlan(t, map[string]string{"python": "/usr/bin/python3"})
	previous := collectProviderBaseExecutableEvidence
	t.Cleanup(func() { collectProviderBaseExecutableEvidence = previous })
	tests := []struct {
		name string
		run  func([]FullImageExecutableProbe) []providers.ExecutableEvidence
		want string
	}{
		{name: "missing", run: func([]FullImageExecutableProbe) []providers.ExecutableEvidence {
			return []providers.ExecutableEvidence{}
		}, want: "missing"},
		{name: "extra", run: func(checks []FullImageExecutableProbe) []providers.ExecutableEvidence {
			declared, err := ExecutableEvidenceFromProbe(
				providerBaseObservation(checks[0].ID, checks[0].InvocationPath), checks[0].Binding,
			)
			if err != nil {
				t.Fatal(err)
			}
			extra, err := ExecutableEvidenceFromProbe(providerBaseObservation("other", "/usr/bin/other"), ProbeExecutableBinding{
				Output: providers.QualifiedOutput{Component: "base", Name: "other"},
				Facts:  checks[0].Binding.Facts,
			})
			if err != nil {
				t.Fatal(err)
			}
			return []providers.ExecutableEvidence{declared, extra}
		}, want: "undeclared"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			collectProviderBaseExecutableEvidence = func(_ context.Context, _ providerstore.Store, _ deploy.ImageDescriptor, checks []FullImageExecutableProbe) ([]providers.ExecutableEvidence, error) {
				return test.run(checks), nil
			}
			if _, _, err := RealizeProviderBase(context.Background(), providerstore.Store{}, plan, descriptor); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func providerBaseDescriptor(t *testing.T, registry bool) deploy.ImageDescriptor {
	t.Helper()
	platform, err := blueprint.ParsePlatform("linux/amd64")
	if err != nil {
		t.Fatal(err)
	}
	config := rendererDigest("2")
	descriptor := deploy.ImageDescriptor{
		Schema: deploy.ImageDescriptorSchemaV1, Platform: platform,
		AuthorReference: "debian:bookworm-slim", ImmutableReference: "debian@" + string(rendererDigest("1")),
		ManifestDigest: rendererDigest("1"), ConfigDigest: config,
		RootFSDiffIDs: []canonical.Digest{rendererDigest("3"), rendererDigest("4")},
	}
	if !registry {
		descriptor.AuthorReference = string(config)
		descriptor.ImmutableReference = string(config)
		descriptor.ManifestDigest = ""
	}
	if err := descriptor.Validate(); err != nil {
		t.Fatal(err)
	}
	return descriptor
}

func providerBasePlan(t *testing.T, exports map[string]string) providers.ProviderPlanV1 {
	t.Helper()
	request, err := providers.CanonicalBaseProviderRequestV1(providers.BaseProviderRequestV1{
		Image: "debian:bookworm-slim", Exports: func() map[string]blueprint.BaseExecutableExport {
			result := make(map[string]blueprint.BaseExecutableExport, len(exports))
			for name, path := range exports {
				result[name] = blueprint.BaseExecutableExport{Executable: path}
			}
			return result
		}(),
	})
	if err != nil {
		t.Fatal(err)
	}
	base, err := providers.BaseNodeSpec(request)
	if err != nil {
		t.Fatal(err)
	}
	return providers.ProviderPlanV1{Schema: providers.ProviderPlanSchemaV1, Nodes: []providers.NodeSpec{base}, Edges: []providers.ProviderEdgeV1{}}
}

func providerBaseObservation(id string, path string) probe.ExecutableObservationV1 {
	observation := testExecutableObservation()
	observation.ID = id
	observation.InvocationPath = path
	observation.Links[0].Path = path
	return observation
}
