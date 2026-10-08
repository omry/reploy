package dockerdeploy

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/probe"
	"github.com/omry/reploy/internal/providers"
	aptprovider "github.com/omry/reploy/internal/providers/apt"
	"github.com/omry/reploy/internal/providerstore"
)

// CollectApplicationPortableNativeExportEvidenceV1 binds the native handoff
// to the complete validated application lock and its exact final image before
// repeating current package and file observations. A provider prefix image or
// independently supplied catalog cannot authorize the handoff.
func CollectApplicationPortableNativeExportEvidenceV1(
	ctx context.Context, store providerstore.Store, image InspectedImageCandidate,
	lock deploy.BuildLockV1, scope string, consumerPaths map[string]string,
) ([]providers.ExecutableEvidence, []providers.ExecutableEvidence, error) {
	if _, err := PortableToolApplicationValidationInputFromBuildLockV1(image, lock, scope); err != nil {
		return nil, nil, err
	}
	return CollectPortableNativeExportEvidenceV1(ctx, store, image, *lock.PortableTools, scope, lock.Catalog, consumerPaths)
}

// CollectPortableNativeExportEvidenceV1 repeats exact locked APT output
// ownership and package-state checks on one held final-image session. Consumer
// paths receive ordinary file/executable observations, without asserting dpkg
// ownership of a filesystem alias. Both results use existing evidence records.
func CollectPortableNativeExportEvidenceV1(
	ctx context.Context, store providerstore.Store, image InspectedImageCandidate,
	lock providers.PortableToolLockV1, scope string, outputs []providers.RealizedOutput,
	consumerPaths map[string]string,
) (exports, consumers []providers.ExecutableEvidence, resultErr error) {
	if ctx == nil {
		return nil, nil, fmt.Errorf("native export evidence requires a context")
	}
	if err := providers.ValidatePortableToolLockV1(lock); err != nil {
		return nil, nil, err
	}
	if err := ValidateInspectedImageCandidateIdentity(image); err != nil {
		return nil, nil, err
	}
	if outputs == nil || consumerPaths == nil {
		return nil, nil, fmt.Errorf("native export evidence inputs must use collections")
	}
	component := "source-builder"
	if owner, ok := strings.CutPrefix(scope, "application:"); ok {
		component = "application/" + owner + "/os"
	} else if !strings.HasPrefix(scope, "source-builder:") {
		return nil, nil, fmt.Errorf("native export evidence requires an owning resolution scope")
	}
	names := []string{}
	locked := []providers.ExecutableEvidence{}
	for _, entry := range lock.Plan.PortableToolPlan.Tools {
		if entry.Scope != scope || len(entry.Responsibilities.NativePackageSets) == 0 || len(entry.Responsibilities.Payloads) != 0 || len(entry.Responsibilities.BindingContracts) != 0 {
			continue
		}
		projected := providers.PortableToolPlanV1{Schema: providers.PortableToolPlanSchemaV1, Tools: []providers.PortableToolPlanEntryV1{entry}}
		var components []providers.ResolvedComponentRequestV1
		var err error
		if component == "source-builder" {
			components, err = aptprovider.ProjectSourceBuilderPortableToolAPTRootsV1(projected)
		} else {
			components, err = aptprovider.ProjectPortableToolAPTRootsV1(projected, []providers.ResolvedComponentRequestV1{})
		}
		if err != nil || len(components) != 1 {
			return nil, nil, fmt.Errorf("native export selection has no exact APT projection: %v", err)
		}
		roots, err := aptprovider.ResolveRootOperandsV1(components[0].Request)
		if err != nil || len(roots) != 1 {
			return nil, nil, fmt.Errorf("native export selection has no unambiguous root: %v", err)
		}
		packageName, version, _ := strings.Cut(roots[0], "=")
		for _, export := range entry.Exports {
			matches := []providers.RealizedOutput{}
			for _, output := range outputs {
				if output.SupplierComponent == component && output.Name == export.Name {
					matches = append(matches, output)
				}
			}
			if len(matches) != 1 || matches[0].SupplierNode != "apt" || matches[0].Evidence.InvocationPath != export.Path {
				return nil, nil, fmt.Errorf("native export %q lacks its unique selected APT output", export.Name)
			}
			owned := matches[0].Evidence
			if owned.Output != (providers.QualifiedOutput{Component: component, Name: export.Name}) || matches[0].Candidate.InvocationPath != export.Path {
				return nil, nil, fmt.Errorf("native export %q differs from its supplier-qualified candidate", export.Name)
			}
			tuples, err := aptprovider.LockedOutputOwnerTuplesV1(image.Descriptor.Platform.Architecture, []providers.ExecutableEvidence{owned})
			if err != nil || len(tuples) != 1 || tuples[0].Name != packageName || tuples[0].Version != version {
				return nil, nil, fmt.Errorf("native export %q ownership differs from selected package: %v", export.Name, err)
			}
			// Every selecting tool must agree with the same locked owner,
			// including tools that share a supplier-qualified export.
			if slices.Contains(names, export.Name) {
				continue
			}
			names = append(names, export.Name)
			locked = append(locked, owned)
		}
	}
	if len(names) == 0 || len(consumerPaths) != len(names) {
		return nil, nil, fmt.Errorf("native export consumer paths must match the complete selected export set")
	}
	order := make([]int, len(names))
	for i := range order {
		order[i] = i
	}
	sort.Slice(order, func(i, j int) bool { return names[order[i]] < names[order[j]] })
	request := probe.RequestV1{Schema: probe.RequestSchemaV1, Inspections: []probe.ExecutableInspectionV1{}}
	ordered := []providers.ExecutableEvidence{}
	for i, original := range order {
		name := names[original]
		consumer, exists := consumerPaths[name]
		if !exists {
			return nil, nil, fmt.Errorf("native export %q has no consumer path", name)
		}
		ordered = append(ordered, locked[original])
		request.Inspections = append(request.Inspections,
			probe.ExecutableInspectionV1{ID: fmt.Sprintf("consumer_%03d", i), InvocationPath: consumer},
			probe.ExecutableInspectionV1{ID: fmt.Sprintf("export_%03d", i), InvocationPath: locked[original].InvocationPath})
	}
	sort.Slice(request.Inspections, func(i, j int) bool { return request.Inspections[i].ID < request.Inspections[j].ID })
	if err := probe.ValidateRequestV1(request); err != nil {
		return nil, nil, err
	}
	workspace, cleanup, err := prepareImageProbeWorkspace(ctx, store, image.Descriptor.Platform)
	if err != nil {
		return nil, nil, err
	}
	defer func() {
		if !providerHelperCleanupFailed(resultErr) {
			if err := cleanup(); err != nil {
				exports, consumers = nil, nil
				resultErr = errors.Join(resultErr, err)
			}
		}
	}()
	session, err := OpenImageValidationSession(ctx, image.Descriptor, workspace)
	if err != nil {
		return nil, nil, err
	}
	defer func() {
		if err := session.Close(context.WithoutCancel(ctx)); err != nil {
			exports, consumers = nil, nil
			resultErr = errors.Join(resultErr, err)
		}
	}()
	response, err := session.Probe(ctx, request)
	if err != nil {
		return nil, nil, err
	}
	observations := map[string]probe.ExecutableObservationV1{}
	for _, observation := range response.Observations {
		observations[observation.ID] = observation
	}
	fresh := []providers.ExecutableEvidence{}
	for i, owned := range ordered {
		binding := ProbeExecutableBinding{Output: owned.Output, Facts: owned.Facts}
		current, err := ExecutableEvidenceFromProbe(observations[fmt.Sprintf("export_%03d", i)], binding)
		if err != nil {
			return nil, nil, err
		}
		consumer, err := ExecutableEvidenceFromProbe(observations[fmt.Sprintf("consumer_%03d", i)], binding)
		if err != nil {
			return nil, nil, err
		}
		if current.Terminal.Path != consumer.Terminal.Path {
			return nil, nil, fmt.Errorf("native export and consumer resolve to different terminal paths: export %q, consumer %q", current.Terminal.Path, consumer.Terminal.Path)
		}
		if current.Terminal.SHA256 != owned.Terminal.SHA256 || current.Terminal.Size != owned.Terminal.Size ||
			current.Terminal.SHA256 != consumer.Terminal.SHA256 || current.Terminal.Size != consumer.Terminal.Size {
			return nil, nil, fmt.Errorf("native export and consumer regular file differ from locked bytes")
		}
		fresh = append(fresh, current)
		consumers = append(consumers, consumer)
	}
	exports, err = reproduceAPTOutputEvidence(ctx, session, image.Descriptor.Platform.Architecture, fresh, ordered)
	if err != nil {
		return nil, nil, err
	}
	return exports, consumers, nil
}
