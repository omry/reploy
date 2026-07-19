package providers

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestResolveCachedOrFreshUsesValidCacheWithoutResolution(t *testing.T) {
	cached := ResolveResult{Bundle: ResolvedBundle{Identity: testDigest("1")}}
	freshCalls := 0
	result, err := ResolveCachedOrFresh(context.Background(), &cached, func(context.Context, ResolveResult) error {
		return nil
	}, func(context.Context) (ResolveResult, error) {
		freshCalls++
		return ResolveResult{}, nil
	})
	if err != nil || freshCalls != 0 || result.Refreshed || result.Result.Bundle.Identity != cached.Bundle.Identity {
		t.Fatalf("result = %#v, fresh calls = %d, error = %v", result, freshCalls, err)
	}
}

func TestResolveCachedOrFreshReresolvesCachedMismatchExactlyOnce(t *testing.T) {
	cached := ResolveResult{Bundle: ResolvedBundle{Identity: testDigest("2")}}
	fresh := ResolveResult{Bundle: ResolvedBundle{Identity: testDigest("3")}}
	freshCalls := 0
	validateCalls := 0
	result, err := ResolveCachedOrFresh(context.Background(), &cached, func(_ context.Context, result ResolveResult) error {
		validateCalls++
		if result.Bundle.Identity == cached.Bundle.Identity {
			return errors.New("upstream rootfs changed")
		}
		return nil
	}, func(context.Context) (ResolveResult, error) {
		freshCalls++
		return fresh, nil
	})
	if err != nil || freshCalls != 1 || validateCalls != 2 || !result.Refreshed || result.Result.Bundle.Identity != fresh.Bundle.Identity {
		t.Fatalf("result = %#v, fresh calls = %d, validation calls = %d, error = %v", result, freshCalls, validateCalls, err)
	}
}

func TestResolveCachedOrFreshRejectsPersistentMismatchWithoutRetry(t *testing.T) {
	cached := ResolveResult{Bundle: ResolvedBundle{Identity: testDigest("4")}}
	freshCalls := 0
	validateCalls := 0
	result, err := ResolveCachedOrFresh(context.Background(), &cached, func(context.Context, ResolveResult) error {
		validateCalls++
		return errors.New("prerequisite mismatch")
	}, func(context.Context) (ResolveResult, error) {
		freshCalls++
		return ResolveResult{Bundle: ResolvedBundle{Identity: testDigest("5")}}, nil
	})
	if err == nil || !strings.Contains(err.Error(), "fresh resolution still does not match") || freshCalls != 1 || validateCalls != 2 || result.Refreshed || result.Result.Bundle.Identity != "" {
		t.Fatalf("result = %#v, fresh calls = %d, validation calls = %d, error = %v", result, freshCalls, validateCalls, err)
	}
}

func TestResolveCachedOrFreshDoesNotRetryFreshOnlyMismatch(t *testing.T) {
	freshCalls := 0
	_, err := ResolveCachedOrFresh(context.Background(), nil, func(context.Context, ResolveResult) error {
		return errors.New("mismatch")
	}, func(context.Context) (ResolveResult, error) {
		freshCalls++
		return ResolveResult{}, nil
	})
	if err == nil || !strings.Contains(err.Error(), "fresh provider resolution does not match") || freshCalls != 1 {
		t.Fatalf("fresh calls = %d, error = %v", freshCalls, err)
	}
}
