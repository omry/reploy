package dockerdeploy

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
)

func TestAPTNodePreparerRetriesOneCachedMismatchInSameSession(t *testing.T) {
	descriptor := testProbeImageDescriptor(t, "linux/amd64")
	upstream, err := realizedImageFromDescriptor(descriptor)
	if err != nil {
		t.Fatal(err)
	}
	previous := openAPTNodePreparationSession
	t.Cleanup(func() { openAPTNodePreparationSession = previous })
	session := &APTResolverSession{closed: true}
	openCalls := 0
	openAPTNodePreparationSession = func(context.Context, deploy.ImageDescriptor, PreparedProbeWorkspace, PreparedAPTResolverWorkspace, RunOptions) (*APTResolverSession, error) {
		openCalls++
		return session, nil
	}
	cached := providers.ResolveResult{}
	fresh := providers.ResolveResult{}
	consumer := providers.GraphConsumerValidation{}
	validateCalls, freshCalls := 0, 0
	preparer := APTNodePreparer{
		Descriptor: descriptor,
		ValidateCached: func(_ context.Context, got *APTResolverSession, _ providers.ResolveNodeRequest, _ providers.ResolveResult) (providers.GraphConsumerValidation, error) {
			validateCalls++
			if got != session {
				t.Fatal("cached validation received a different session")
			}
			return providers.GraphConsumerValidation{}, errors.New("locked tool changed")
		},
		ResolveFresh: func(_ context.Context, got *APTResolverSession, _ providers.ResolveNodeRequest) (providers.ResolveResult, providers.GraphConsumerValidation, error) {
			freshCalls++
			if got != session {
				t.Fatal("fresh resolution received a different session")
			}
			return fresh, consumer, nil
		},
	}
	result, err := preparer.Prepare(context.Background(), providers.GraphNodePrepareRequest{
		Resolve: providers.ResolveNodeRequest{Platform: descriptor.Platform, Upstream: upstream}, CachedResolution: &cached,
	})
	if err != nil {
		t.Fatal(err)
	}
	if openCalls != 1 || validateCalls != 1 || freshCalls != 1 || !result.Refreshed {
		t.Fatalf("open=%d validate=%d fresh=%d result=%#v", openCalls, validateCalls, freshCalls, result)
	}
}

func TestAPTNodePreparerPersistentMismatchFailsWithoutAnotherRetry(t *testing.T) {
	descriptor := testProbeImageDescriptor(t, "linux/amd64")
	upstream, err := realizedImageFromDescriptor(descriptor)
	if err != nil {
		t.Fatal(err)
	}
	previous := openAPTNodePreparationSession
	t.Cleanup(func() { openAPTNodePreparationSession = previous })
	openAPTNodePreparationSession = func(context.Context, deploy.ImageDescriptor, PreparedProbeWorkspace, PreparedAPTResolverWorkspace, RunOptions) (*APTResolverSession, error) {
		return &APTResolverSession{closed: true}, nil
	}
	cached := providers.ResolveResult{}
	freshCalls := 0
	preparer := APTNodePreparer{
		Descriptor: descriptor,
		ValidateCached: func(context.Context, *APTResolverSession, providers.ResolveNodeRequest, providers.ResolveResult) (providers.GraphConsumerValidation, error) {
			return providers.GraphConsumerValidation{}, errors.New("cached mismatch")
		},
		ResolveFresh: func(context.Context, *APTResolverSession, providers.ResolveNodeRequest) (providers.ResolveResult, providers.GraphConsumerValidation, error) {
			freshCalls++
			return providers.ResolveResult{}, providers.GraphConsumerValidation{}, errors.New("fresh mismatch")
		},
	}
	_, err = preparer.Prepare(context.Background(), providers.GraphNodePrepareRequest{
		Resolve: providers.ResolveNodeRequest{Platform: descriptor.Platform, Upstream: upstream}, CachedResolution: &cached,
	})
	if err == nil || !strings.Contains(err.Error(), "cached mismatch") || !strings.Contains(err.Error(), "fresh mismatch") || freshCalls != 1 {
		t.Fatalf("error=%v fresh=%d", err, freshCalls)
	}
}
