package dockerdeploy

import (
	"context"
	"fmt"
	"sort"

	"github.com/omry/reploy/internal/providers"
	aptprovider "github.com/omry/reploy/internal/providers/apt"
)

// reproduceAPTOutputEvidence performs only the provider-owned checks needed
// after the combined filesystem probe. Every command runs in the already-held
// validation session and is parameterized by paths or packages from accepted
// evidence.
func reproduceAPTOutputEvidence(
	ctx context.Context,
	session *ImageValidationSession,
	nativeArchitecture string,
	fresh []providers.ExecutableEvidence,
	locked []providers.ExecutableEvidence,
) ([]providers.ExecutableEvidence, error) {
	if ctx == nil {
		return nil, fmt.Errorf("reproduce APT output evidence requires a context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if session == nil || session.closed {
		return nil, fmt.Errorf("image validation session is not open")
	}
	if fresh == nil || locked == nil || len(fresh) != len(locked) {
		return nil, fmt.Errorf("APT fresh and locked output evidence must use matching arrays")
	}
	if len(fresh) == 0 {
		return []providers.ExecutableEvidence{}, nil
	}

	paths := map[string]bool{}
	alternativeGroups := map[string]string{}
	for _, evidence := range fresh {
		if err := providers.ValidateFinalExecutableEvidence(evidence); err != nil {
			return nil, err
		}
		if evidence.Terminal.Owner != nil {
			return nil, fmt.Errorf("fresh APT output evidence contains a prebound terminal owner")
		}
		for index, link := range evidence.LinkChain {
			if link.Kind != "ordinary" || link.Owner != nil || link.ProviderDetail != nil {
				return nil, fmt.Errorf("fresh APT output evidence contains prebound link metadata")
			}
			if group, managed := aptprovider.AlternativeGroupForPathV1(link.Path); managed {
				alternativeGroups[link.Path] = group
				if index > 0 {
					delete(paths, evidence.LinkChain[index-1].Path)
				}
			} else {
				paths[link.Path] = true
			}
		}
		paths[evidence.Terminal.Path] = true
	}
	orderedPaths := sortedStringKeys(paths)
	rawOwners, err := session.QueryDPKGOwners(ctx, orderedPaths)
	if err != nil {
		return nil, err
	}
	ownerByPath, err := aptprovider.ParseDPKGSearchOutputV1(rawOwners, orderedPaths, nativeArchitecture)
	if err != nil {
		return nil, err
	}

	lockedTuples, err := aptprovider.LockedOutputOwnerTuplesV1(nativeArchitecture, locked)
	if err != nil {
		return nil, err
	}
	packageNames := make([]string, len(lockedTuples))
	for index, tuple := range lockedTuples {
		packageNames[index] = tuple.Name
	}
	rawState, err := session.QueryDPKGPackageState(ctx, packageNames)
	if err != nil {
		return nil, err
	}
	installed, err := aptprovider.ParseInstalledPackageStateV1(rawState, lockedTuples, nativeArchitecture)
	if err != nil {
		return nil, err
	}

	alternativePaths := sortedStringKeys(alternativeGroups)
	alternatives := make(map[string]aptprovider.AlternativeSelectionV1, len(alternativePaths))
	for _, alternativePath := range alternativePaths {
		group := alternativeGroups[alternativePath]
		raw, err := session.QueryAlternative(ctx, group)
		if err != nil {
			return nil, err
		}
		selection, err := aptprovider.ParseAlternativeQueryV1(raw, group)
		if err != nil {
			return nil, err
		}
		alternatives[alternativePath] = selection
	}
	return aptprovider.ReproduceOutputOwnershipV1(
		nativeArchitecture, fresh, locked, ownerByPath, installed, alternatives,
	)
}

func sortedStringKeys[V any](values map[string]V) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
