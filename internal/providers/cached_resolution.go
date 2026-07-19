package providers

import (
	"context"
	"fmt"
)

type CachedResolutionValidator func(context.Context, ResolveResult) error
type FreshNodeResolver func(context.Context) (ResolveResult, error)

type ValidatedResolution struct {
	Result    ResolveResult
	Refreshed bool
}

// ResolveCachedOrFresh accepts a cached result only after the consumer
// validates it against its current upstream prefix. A rejected cached result
// gets one fresh resolution and no further retry.
func ResolveCachedOrFresh(
	ctx context.Context,
	cached *ResolveResult,
	validate CachedResolutionValidator,
	resolveFresh FreshNodeResolver,
) (ValidatedResolution, error) {
	if ctx == nil {
		return ValidatedResolution{}, fmt.Errorf("cached provider resolution requires a context")
	}
	if validate == nil {
		return ValidatedResolution{}, fmt.Errorf("cached provider resolution requires consumer validation")
	}
	if resolveFresh == nil {
		return ValidatedResolution{}, fmt.Errorf("cached provider resolution requires a fresh resolver")
	}
	if err := ctx.Err(); err != nil {
		return ValidatedResolution{}, err
	}
	var cachedMismatch error
	if cached != nil {
		if err := validate(ctx, *cached); err == nil {
			return ValidatedResolution{Result: *cached}, nil
		} else {
			cachedMismatch = err
		}
		if err := ctx.Err(); err != nil {
			return ValidatedResolution{}, err
		}
	}
	fresh, err := resolveFresh(ctx)
	if err != nil {
		if cachedMismatch != nil {
			return ValidatedResolution{}, fmt.Errorf("cached provider prerequisites changed (%v); fresh resolution failed: %w", cachedMismatch, err)
		}
		return ValidatedResolution{}, fmt.Errorf("fresh provider resolution failed: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return ValidatedResolution{}, err
	}
	if err := validate(ctx, fresh); err != nil {
		if cachedMismatch != nil {
			return ValidatedResolution{}, fmt.Errorf("cached provider prerequisites changed (%v); fresh resolution still does not match: %w", cachedMismatch, err)
		}
		return ValidatedResolution{}, fmt.Errorf("fresh provider resolution does not match current prerequisites: %w", err)
	}
	return ValidatedResolution{Result: fresh, Refreshed: cached != nil}, nil
}
