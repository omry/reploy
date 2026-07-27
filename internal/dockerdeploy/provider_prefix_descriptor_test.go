package dockerdeploy

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/providers"
)

func TestResolveProviderPrefixDescriptorReusesExactBaseWithoutInspection(t *testing.T) {
	base := testProbeImageDescriptor(t, "linux/amd64")
	image, err := realizedImageFromDescriptor(base)
	if err != nil {
		t.Fatal(err)
	}
	previous := inspectProviderPrefixImage
	t.Cleanup(func() { inspectProviderPrefixImage = previous })
	inspectProviderPrefixImage = func(context.Context, BuiltImageCandidate, blueprint.Platform) (InspectedImageCandidate, error) {
		t.Fatal("exact base prefix was reinspected")
		return InspectedImageCandidate{}, nil
	}
	got, err := ResolveProviderPrefixDescriptor(context.Background(), base, image, base.Platform)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, base) {
		t.Fatalf("descriptor = %#v", got)
	}
}

func TestResolveProviderPrefixDescriptorInspectsLocalChildByConfigID(t *testing.T) {
	base := testProbeImageDescriptor(t, "linux/amd64")
	childDescriptor := base
	childDescriptor.AuthorReference = string(canonical.Digest("sha256:" + strings.Repeat("a", 64)))
	childDescriptor.ImmutableReference = childDescriptor.AuthorReference
	childDescriptor.ManifestDigest = ""
	childDescriptor.ConfigDigest = canonical.Digest(childDescriptor.AuthorReference)
	childDescriptor.RootFSDiffIDs = append(append([]canonical.Digest{}, base.RootFSDiffIDs...), canonical.Digest("sha256:"+strings.Repeat("b", 64)))
	child, err := realizedImageFromDescriptor(childDescriptor)
	if err != nil {
		t.Fatal(err)
	}
	previous := inspectProviderPrefixImage
	t.Cleanup(func() { inspectProviderPrefixImage = previous })
	inspectCalls := 0
	inspectProviderPrefixImage = func(_ context.Context, candidate BuiltImageCandidate, platform blueprint.Platform) (InspectedImageCandidate, error) {
		inspectCalls++
		if candidate.ImageID != child.ConfigDigest || platform != base.Platform {
			return InspectedImageCandidate{}, errors.New("wrong prefix inspection input")
		}
		return InspectedImageCandidate{Descriptor: childDescriptor, Image: child}, nil
	}
	got, err := ResolveProviderPrefixDescriptor(context.Background(), base, child, base.Platform)
	if err != nil {
		t.Fatal(err)
	}
	if inspectCalls != 1 || !reflect.DeepEqual(got, childDescriptor) {
		t.Fatalf("calls = %d, descriptor = %#v", inspectCalls, got)
	}
}

func TestResolveProviderPrefixDescriptorRejectsInspectionDrift(t *testing.T) {
	base := testProbeImageDescriptor(t, "linux/amd64")
	prefix := providers.RealizedImageV1{
		Digest:        canonical.Digest("sha256:" + strings.Repeat("c", 64)),
		ConfigDigest:  canonical.Digest("sha256:" + strings.Repeat("c", 64)),
		RootFSSubject: canonical.Digest("sha256:" + strings.Repeat("d", 64)),
	}
	previous := inspectProviderPrefixImage
	t.Cleanup(func() { inspectProviderPrefixImage = previous })
	inspectProviderPrefixImage = func(context.Context, BuiltImageCandidate, blueprint.Platform) (InspectedImageCandidate, error) {
		baseImage, err := realizedImageFromDescriptor(base)
		if err != nil {
			return InspectedImageCandidate{}, err
		}
		return InspectedImageCandidate{Descriptor: base, Image: baseImage}, nil
	}
	if _, err := ResolveProviderPrefixDescriptor(context.Background(), base, prefix, base.Platform); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("error = %v", err)
	}
}
