package python

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path"
	"reflect"
	"sort"
	"strconv"

	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/portabletool"
	providerapi "github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providerstore"
)

// PortableToolVerifiedWheelModeV1 selects whether the exact acquisition
// request is allowed to use the acquisition transport or must be satisfied
// from the deployment-local provider store.
type PortableToolVerifiedWheelModeV1 string

const (
	PortableToolVerifiedWheelFreshV1        PortableToolVerifiedWheelModeV1 = "fresh"
	PortableToolVerifiedWheelLockedReplayV1 PortableToolVerifiedWheelModeV1 = "locked-replay"
)

// PortableToolPythonVerifiedWheelRequestV1 is the complete input for one
// selected portable binding wheel. The selected plan entry and projection are
// repeated per request intentionally: verification can therefore authenticate
// every output without consulting mutable global state.
//
// SourceRecord is required for fresh acquisition and must identify the
// authorizing source. During locked replay the existing lock supplies it.
// ManifestRecord is required in both modes to authenticate that source
// against the selected release before network access or replay.
// LockedAcquisition must be supplied for locked replay and is the existing
// provider lock entry, rather than a new replay schema.
type PortableToolPythonVerifiedWheelRequestV1 struct {
	SelectedPlanEntry providerapi.PortableToolPlanEntryV1
	Component         PortableToolPythonComponentV1
	Binding           PortableToolPythonBindingV1
	ContractRecord    providerapi.PortableToolSelectedRecordV1
	ArtifactRecord    providerapi.PortableToolSelectedRecordV1
	SourceRecord      providerapi.PortableToolSelectedRecordV1
	ManifestRecord    providerapi.PortableToolSelectedRecordV1
	Interpreter       providerapi.ExecutableEvidence
	Acquisition       providerstore.AcquisitionRequest
	Mode              PortableToolVerifiedWheelModeV1
	LockedAcquisition *providerapi.PortableToolArtifactAcquisitionLockV1
}

// PortableToolVerifiedWheelInputV1 is a detached, authenticated wheel input
// suitable for resolver staging and lock assembly. Its observations came from
// the exact descriptor-bound store file and are never inferred by executing
// wheel or bundled component content.
type PortableToolVerifiedWheelInputV1 struct {
	Scope                 string
	Tool                  string
	SelectedClosureDigest canonical.Digest
	Contract              providerapi.PortableToolRecordReferenceV1
	Artifact              providerapi.PortableToolRecordReferenceV1
	Descriptor            providerstore.ArtifactDescriptor
	Inspection            WheelInspectionV1
	EligibleFilenameTags  []string
	ConsoleScript         WheelConsoleScriptV1
	Source                providerapi.PortableToolSelectedRecordV1
	Provenance            providerstore.AcquisitionProvenance
}

// ProducePortableToolPythonVerifiedWheelsV1 acquires (or reopens during
// locked replay), inspects, and verifies selected portable Python wheels.
// Every request is checked for structural and acquisition conflicts before
// the first acquisition begins. Results are sorted by application scope,
// distribution, and exact artifact reference. The complete selected
// projection is required so a partial request set cannot silently omit a
// binding.
func ProducePortableToolPythonVerifiedWheelsV1(
	ctx context.Context,
	store providerstore.Store,
	projection PortableToolPythonProjectionV1,
	requests []PortableToolPythonVerifiedWheelRequestV1,
) ([]PortableToolVerifiedWheelInputV1, error) {
	if ctx == nil {
		return nil, fmt.Errorf("portable Python verified-wheel context is required")
	}
	if requests == nil {
		return nil, fmt.Errorf("portable Python verified-wheel requests must use an explicit array")
	}
	if _, err := CanonicalPortableToolPythonProjectionBytesV1(projection); err != nil {
		return nil, fmt.Errorf("portable Python verified-wheel projection: %w", err)
	}
	selected := make(map[string]PortableToolPythonComponentV1, len(projection.Components))
	expectedCount := 0
	for _, component := range projection.Components {
		selected[component.Component] = component
		expectedCount += len(component.Bindings)
	}
	for index := range requests {
		if index > 0 && requests[index].Mode != requests[0].Mode {
			return nil, fmt.Errorf("portable Python verified-wheel requests must use one acquisition mode")
		}
		component, found := selected[requests[index].Component.Component]
		if !found || !reflect.DeepEqual(component, requests[index].Component) {
			return nil, fmt.Errorf("portable Python verified-wheel request %d does not match the selected projection component", index)
		}
		if err := preflightPortableToolPythonVerifiedWheelRequestV1(requests[index]); err != nil {
			return nil, fmt.Errorf("portable Python verified-wheel request %d: %w", index, err)
		}
	}
	seenOutputs := make(map[string]struct{}, len(requests))
	seenBindings := make(map[string]struct{}, len(requests))
	artifactDeclarations := make(map[string]PortableToolPythonVerifiedWheelRequestV1, len(requests))
	requestKeys := make([]string, len(requests))
	type acquisitionDeclaration struct {
		index   int
		request PortableToolPythonVerifiedWheelRequestV1
	}
	declarations := make(map[string]acquisitionDeclaration, len(requests))
	for index := range requests {
		request := requests[index]
		outputKey := request.Binding.Scope + "\x00" + request.SelectedPlanEntry.Provenance.Tool + "\x00" + portableToolVerifiedWheelArtifactKeyV1(request)
		if _, exists := seenOutputs[outputKey]; exists {
			return nil, fmt.Errorf("portable Python verified-wheel request %d duplicates exact artifact acquisition", index)
		}
		seenOutputs[outputKey] = struct{}{}
		bindingKey := request.Binding.Component + "\x00" + request.Binding.Distribution
		if _, exists := seenBindings[bindingKey]; exists {
			return nil, fmt.Errorf("portable Python verified-wheel request %d duplicates selected binding", index)
		}
		seenBindings[bindingKey] = struct{}{}
		artifactKey := portableToolVerifiedWheelArtifactKeyV1(request)
		if previous, exists := artifactDeclarations[artifactKey]; exists {
			if previous.Acquisition.Artifact != request.Acquisition.Artifact {
				return nil, fmt.Errorf("portable Python verified-wheel request %d shared artifact acquisition descriptors differ", index)
			}
			if previous.Acquisition.Policy != request.Acquisition.Policy || previous.Acquisition.OperationID != request.Acquisition.OperationID {
				return nil, fmt.Errorf("portable Python verified-wheel request %d shared artifact acquisition policy or operation ID differs", index)
			}
		} else {
			artifactDeclarations[artifactKey] = request
		}
		declarationKey, err := portableToolVerifiedWheelAcquisitionKeyV1(request)
		if err != nil {
			return nil, fmt.Errorf("portable Python verified-wheel request %d acquisition identity: %w", index, err)
		}
		requestKeys[index] = declarationKey
		if previous, exists := declarations[declarationKey]; exists {
			if err := comparePortableToolVerifiedWheelAcquisitionDeclarationsV1(previous.request, request); err != nil {
				return nil, fmt.Errorf("portable Python verified-wheel request %d conflicts with request %d for shared artifact %q: %w", index, previous.index, request.Binding.Artifact.ID, err)
			}
			continue
		}
		declarations[declarationKey] = acquisitionDeclaration{index: index, request: request}
	}
	if len(requests) != expectedCount {
		return nil, fmt.Errorf("portable Python verified-wheel requests cover %d of %d selected bindings", len(requests), expectedCount)
	}
	for _, component := range projection.Components {
		for _, binding := range component.Bindings {
			found := false
			for _, request := range requests {
				if request.Binding.Component == binding.Component && request.Binding.Distribution == binding.Distribution {
					found = true
					break
				}
			}
			if !found {
				return nil, fmt.Errorf("portable Python verified-wheel request is missing selected binding %q in component %q", binding.Distribution, component.Component)
			}
		}
	}

	shared := make(map[string]portableToolVerifiedWheelArtifactV1, len(declarations))
	declarationKeys := make([]string, 0, len(declarations))
	for key := range declarations {
		declarationKeys = append(declarationKeys, key)
	}
	sort.Strings(declarationKeys)
	for _, key := range declarationKeys {
		declaration := declarations[key]
		prepared, err := preparePortableToolPythonVerifiedWheelV1(ctx, store, declaration.request)
		if err != nil {
			return nil, fmt.Errorf("portable Python verified-wheel request %d: %w", declaration.index, err)
		}
		shared[key] = prepared
	}
	result := make([]PortableToolVerifiedWheelInputV1, 0, len(requests))
	for index := range requests {
		input, err := verifyPortableToolPythonVerifiedWheelV1(requests[index], shared[requestKeys[index]])
		if err != nil {
			return nil, fmt.Errorf("portable Python verified-wheel request %d: %w", index, err)
		}
		result = append(result, input)
	}
	sort.Slice(result, func(left, right int) bool {
		return portableToolVerifiedWheelInputKeyV1(result[left]) < portableToolVerifiedWheelInputKeyV1(result[right])
	})
	return result, nil
}

// PortableToolArtifactAcquisitionInputV1 converts a verified input into the
// existing lock-construction input. The source envelope must be present when
// assembling a lock; fresh callers that omitted SourceRecord can still use
// the verified input for resolver staging.
func (input PortableToolVerifiedWheelInputV1) PortableToolArtifactAcquisitionInputV1() (providerapi.PortableToolArtifactAcquisitionInputV1, error) {
	if input.Source.Reference.ID == "" || input.Source.Record.Schema == "" {
		return providerapi.PortableToolArtifactAcquisitionInputV1{}, fmt.Errorf("verified wheel does not retain an acquisition source record")
	}
	return providerapi.PortableToolArtifactAcquisitionInputV1{
		Scope: input.Scope, Tool: input.Tool, Artifact: input.Artifact,
		Descriptor: input.Descriptor, Source: clonePortableToolSelectedRecordV1(input.Source),
		Provenance: clonePortableToolAcquisitionProvenanceV1(input.Provenance),
	}, nil
}

func preflightPortableToolPythonVerifiedWheelRequestV1(request PortableToolPythonVerifiedWheelRequestV1) error {
	switch request.Mode {
	case PortableToolVerifiedWheelFreshV1:
		if request.LockedAcquisition != nil {
			return fmt.Errorf("fresh acquisition must not carry a locked acquisition")
		}
		if !hasPortableToolSelectedRecordV1(request.SourceRecord) {
			return fmt.Errorf("fresh acquisition requires the authorizing source record")
		}
	case PortableToolVerifiedWheelLockedReplayV1:
		if request.LockedAcquisition == nil {
			return fmt.Errorf("locked replay requires an existing portable-tool acquisition lock entry")
		}
	default:
		return fmt.Errorf("unsupported verified-wheel mode %q", request.Mode)
	}
	if err := providerapi.ValidatePortableToolPlanV1(providerapi.PortableToolPlanV1{
		Schema: providerapi.PortableToolPlanSchemaV1, Tools: []providerapi.PortableToolPlanEntryV1{request.SelectedPlanEntry},
	}); err != nil {
		return fmt.Errorf("selected plan entry: %w", err)
	}
	if request.Binding.Scope != request.SelectedPlanEntry.Scope ||
		request.Binding.SelectedClosureDigest != request.SelectedPlanEntry.SelectedClosureDigest {
		return fmt.Errorf("binding scope or selected closure does not match selected plan entry")
	}
	if _, err := CanonicalPortableToolPythonProjectionBytesV1(PortableToolPythonProjectionV1{
		Schema: PortableToolPythonProjectionSchemaV1, Components: []PortableToolPythonComponentV1{request.Component},
	}); err != nil {
		return fmt.Errorf("projection: %w", err)
	}
	if request.Component.Component != request.Binding.Component {
		return fmt.Errorf("binding component does not match projected component")
	}
	bindingFound := false
	for _, candidate := range request.Component.Bindings {
		if candidate.Distribution != request.Binding.Distribution {
			continue
		}
		equal, err := portableToolPythonBindingsEqualV1(candidate, request.Binding)
		if err != nil {
			return fmt.Errorf("projection binding comparison: %w", err)
		}
		if !equal {
			return fmt.Errorf("binding does not equal its projected binding")
		}
		bindingFound = true
		break
	}
	if !bindingFound {
		return fmt.Errorf("binding %q is absent from its projected component", request.Binding.Distribution)
	}
	contract, err := decodeVerifiedPortableToolBindingRecordV1[portabletool.BindingContractV1](
		request.ContractRecord, request.Binding.Contract, portabletool.BindingContractSchemaV1,
	)
	if err != nil {
		return fmt.Errorf("contract: %w", err)
	}
	artifact, err := decodeVerifiedPortableToolBindingRecordV1[portabletool.BindingArtifactRecordV1](
		request.ArtifactRecord, request.Binding.Artifact, portabletool.BindingArtifactSchemaV1,
	)
	if err != nil {
		return fmt.Errorf("artifact: %w", err)
	}
	member, err := portableToolPythonSelectedRecordMemberV1(request.ContractRecord, request.SelectedPlanEntry.Responsibilities.BindingContracts)
	if err != nil || !member {
		if err != nil {
			return fmt.Errorf("contract selected record membership: %w", err)
		}
		return fmt.Errorf("contract selected record is not an exact member of selected plan entry")
	}
	member, err = portableToolPythonSelectedRecordMemberV1(request.ArtifactRecord, request.SelectedPlanEntry.Responsibilities.BindingArtifacts)
	if err != nil || !member {
		if err != nil {
			return fmt.Errorf("artifact selected record membership: %w", err)
		}
		return fmt.Errorf("artifact selected record is not an exact member of selected plan entry")
	}
	if contract.CLI != request.Binding.CLI || artifact.Contract != request.Binding.Contract || artifact.Binding != contract.Name {
		return fmt.Errorf("selected contract and artifact do not agree with binding")
	}
	expected := portableToolVerifiedWheelDescriptorV1(request.Binding)
	if request.Acquisition.Artifact != expected {
		return fmt.Errorf("acquisition descriptor does not exactly match selected binding wheel")
	}
	if err := providerstore.ValidateArtifactSource(request.Acquisition.Source, expected); err != nil {
		return fmt.Errorf("acquisition source: %w", err)
	}
	if artifact.Size != expected.Size || artifact.SHA256 != expected.SHA256 || artifact.Filename != path.Base(expected.LogicalPath) {
		return fmt.Errorf("selected artifact content identity does not match acquisition descriptor")
	}
	if _, err := DecodeInterpreterFactsV2(request.Interpreter.Facts); err != nil {
		return fmt.Errorf("interpreter evidence: %w", err)
	}
	if hasPortableToolSelectedRecordV1(request.SourceRecord) {
		recordedSource, err := portableToolPythonSourceFromSelectedRecordV1(request.SourceRecord, expected)
		if err != nil {
			return fmt.Errorf("source record: %w", err)
		}
		if request.Acquisition.Source.ID != recordedSource.ID || request.Acquisition.Source.SHA256 != recordedSource.SHA256 || !stringSlicesEqualV1(request.Acquisition.Source.Mirrors, recordedSource.Mirrors) {
			return fmt.Errorf("acquisition source does not exactly match the authorizing source record")
		}
	}
	authorizedSource := request.SourceRecord
	if request.Mode == PortableToolVerifiedWheelLockedReplayV1 {
		authorizedSource = request.LockedAcquisition.Source
	}
	if err := providerapi.ValidatePortableToolArtifactSourceAuthorizationV1(
		request.SelectedPlanEntry, request.ArtifactRecord, authorizedSource,
		request.ManifestRecord, expected,
	); err != nil {
		return fmt.Errorf("selected artifact source authorization: %w", err)
	}
	if request.Mode == PortableToolVerifiedWheelLockedReplayV1 {
		if err := validatePortableToolLockedAcquisitionV1(request, expected); err != nil {
			return err
		}
	}
	return nil
}

type portableToolVerifiedWheelArtifactV1 struct {
	descriptor providerstore.ArtifactDescriptor
	inspection WheelInspectionV1
	provenance providerstore.AcquisitionProvenance
	source     providerapi.PortableToolSelectedRecordV1
}

func preparePortableToolPythonVerifiedWheelV1(
	ctx context.Context,
	store providerstore.Store,
	request PortableToolPythonVerifiedWheelRequestV1,
) (portableToolVerifiedWheelArtifactV1, error) {
	descriptor := request.Acquisition.Artifact
	provenance := providerstore.AcquisitionProvenance{}
	source := request.SourceRecord
	if request.Mode == PortableToolVerifiedWheelFreshV1 {
		acquired, err := store.AcquireArtifact(ctx, request.Acquisition)
		if err != nil {
			return portableToolVerifiedWheelArtifactV1{}, fmt.Errorf("acquire artifact: %w", err)
		}
		if acquired.Artifact != descriptor {
			return portableToolVerifiedWheelArtifactV1{}, fmt.Errorf("acquired descriptor does not match selected wheel")
		}
		descriptor, provenance = acquired.Artifact, acquired.Provenance
	} else {
		var err error
		descriptor, provenance, source, err = portableToolLockedReplayValuesV1(request)
		if err != nil {
			return portableToolVerifiedWheelArtifactV1{}, err
		}
	}
	file, err := store.OpenVerifiedArtifact(descriptor)
	if err != nil {
		return portableToolVerifiedWheelArtifactV1{}, fmt.Errorf("open verified store artifact: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return portableToolVerifiedWheelArtifactV1{}, fmt.Errorf("inspect verified wheel: %w", err)
	}
	inspection, err := InspectWheelReaderV1(ctx, file, info.Size(), path.Base(descriptor.LogicalPath), descriptor)
	if err != nil {
		return portableToolVerifiedWheelArtifactV1{}, fmt.Errorf("inspect verified wheel: %w", err)
	}
	if err := providerstore.VerifyOpenArtifact(file, descriptor); err != nil {
		return portableToolVerifiedWheelArtifactV1{}, fmt.Errorf("reverify inspected wheel: %w", err)
	}
	if request.Mode == PortableToolVerifiedWheelFreshV1 && hasPortableToolSelectedRecordV1(request.SourceRecord) {
		source = clonePortableToolSelectedRecordV1(request.SourceRecord)
	}
	return portableToolVerifiedWheelArtifactV1{
		descriptor: descriptor, inspection: cloneWheelInspectionV1(inspection),
		provenance: clonePortableToolAcquisitionProvenanceV1(provenance), source: source,
	}, nil
}

func verifyPortableToolPythonVerifiedWheelV1(
	request PortableToolPythonVerifiedWheelRequestV1,
	prepared portableToolVerifiedWheelArtifactV1,
) (PortableToolVerifiedWheelInputV1, error) {
	verified, err := VerifyPortableToolPythonBindingV1(PortableToolPythonBindingVerificationInputV1{
		SelectedPlanEntry: request.SelectedPlanEntry,
		Component:         request.Component, Binding: request.Binding,
		ContractRecord: request.ContractRecord, ArtifactRecord: request.ArtifactRecord,
		Interpreter: request.Interpreter, Descriptor: prepared.descriptor, Inspection: prepared.inspection,
	})
	if err != nil {
		return PortableToolVerifiedWheelInputV1{}, fmt.Errorf("verify selected wheel: %w", err)
	}
	source := clonePortableToolSelectedRecordV1(prepared.source)
	if hasPortableToolSelectedRecordV1(request.SourceRecord) {
		source = clonePortableToolSelectedRecordV1(request.SourceRecord)
	}
	return PortableToolVerifiedWheelInputV1{
		Scope: request.Binding.Scope, Tool: request.SelectedPlanEntry.Provenance.Tool,
		SelectedClosureDigest: request.Binding.SelectedClosureDigest,
		Contract:              request.Binding.Contract, Artifact: request.Binding.Artifact,
		Descriptor: verified.Descriptor, Inspection: cloneWheelInspectionV1(verified.Inspection),
		EligibleFilenameTags: append([]string{}, verified.EligibleFilenameTags...),
		ConsoleScript:        verified.ConsoleScript, Source: source,
		Provenance: clonePortableToolAcquisitionProvenanceV1(prepared.provenance),
	}, nil
}

func portableToolVerifiedWheelDescriptorV1(binding PortableToolPythonBindingV1) providerstore.ArtifactDescriptor {
	return providerstore.ArtifactDescriptor{
		LogicalPath: path.Join("wheels", binding.Wheel.Filename), Kind: "wheel",
		Size: binding.Wheel.Size, SHA256: binding.Wheel.SHA256,
	}
}

func portableToolVerifiedWheelKeyV1(request PortableToolPythonVerifiedWheelRequestV1) string {
	return request.Binding.Scope + "\x00" + request.SelectedPlanEntry.Provenance.Tool + "\x00" + portableToolVerifiedWheelArtifactKeyV1(request)
}

func portableToolVerifiedWheelArtifactKeyV1(request PortableToolPythonVerifiedWheelRequestV1) string {
	return request.Binding.Artifact.ID + "\x00" + string(request.Binding.Artifact.Digest)
}

func portableToolVerifiedWheelAcquisitionKeyV1(request PortableToolPythonVerifiedWheelRequestV1) (string, error) {
	source := request.SourceRecord
	if request.Mode == PortableToolVerifiedWheelLockedReplayV1 {
		source = request.LockedAcquisition.Source
	}
	key := portableToolVerifiedWheelArtifactKeyV1(request) + "\x00" + source.Reference.ID + "\x00" + string(source.Reference.Digest)
	if request.Mode == PortableToolVerifiedWheelLockedReplayV1 {
		outcome, err := canonical.Marshal(request.LockedAcquisition.Outcome)
		if err != nil {
			return "", err
		}
		key += "\x00" + string(outcome)
	}
	return key, nil
}

func comparePortableToolVerifiedWheelAcquisitionDeclarationsV1(
	left, right PortableToolPythonVerifiedWheelRequestV1,
) error {
	if left.Mode != right.Mode {
		return fmt.Errorf("shared artifact uses both fresh and locked-replay modes")
	}
	if left.Acquisition.Artifact != right.Acquisition.Artifact {
		return fmt.Errorf("acquisition descriptors differ")
	}
	if left.Acquisition.Source.ID != right.Acquisition.Source.ID ||
		left.Acquisition.Source.SHA256 != right.Acquisition.Source.SHA256 ||
		!stringSlicesEqualV1(left.Acquisition.Source.Mirrors, right.Acquisition.Source.Mirrors) {
		return fmt.Errorf("acquisition sources differ")
	}
	if left.Acquisition.Policy != right.Acquisition.Policy || left.Acquisition.OperationID != right.Acquisition.OperationID {
		return fmt.Errorf("acquisition policy or operation ID differs")
	}
	if hasPortableToolSelectedRecordV1(left.SourceRecord) && hasPortableToolSelectedRecordV1(right.SourceRecord) {
		leftBytes, err := canonical.Marshal(left.SourceRecord)
		if err != nil {
			return fmt.Errorf("encode first source record: %w", err)
		}
		rightBytes, err := canonical.Marshal(right.SourceRecord)
		if err != nil {
			return fmt.Errorf("encode second source record: %w", err)
		}
		if !bytes.Equal(leftBytes, rightBytes) {
			return fmt.Errorf("source records differ")
		}
	}
	if left.LockedAcquisition != nil && right.LockedAcquisition != nil {
		if left.LockedAcquisition.Descriptor != right.LockedAcquisition.Descriptor ||
			left.LockedAcquisition.Source.Reference != right.LockedAcquisition.Source.Reference ||
			left.LockedAcquisition.Outcome.Kind != right.LockedAcquisition.Outcome.Kind ||
			left.LockedAcquisition.Outcome.SuccessfulDeclaredLocator != right.LockedAcquisition.Outcome.SuccessfulDeclaredLocator ||
			left.LockedAcquisition.Outcome.RedirectHops != right.LockedAcquisition.Outcome.RedirectHops ||
			!stringSlicesEqualV1(left.LockedAcquisition.Outcome.HistoricalLocators, right.LockedAcquisition.Outcome.HistoricalLocators) {
			return fmt.Errorf("locked acquisition outcomes differ")
		}
		leftBytes, err := canonical.Marshal(left.LockedAcquisition.Source.Record)
		if err != nil {
			return fmt.Errorf("encode first locked source record: %w", err)
		}
		rightBytes, err := canonical.Marshal(right.LockedAcquisition.Source.Record)
		if err != nil {
			return fmt.Errorf("encode second locked source record: %w", err)
		}
		if !bytes.Equal(leftBytes, rightBytes) {
			return fmt.Errorf("locked acquisition source records differ")
		}
	}
	return nil
}

func portableToolVerifiedWheelInputKeyV1(input PortableToolVerifiedWheelInputV1) string {
	return input.Scope + "\x00" + input.Inspection.Distribution + "\x00" + input.Artifact.ID + "\x00" + string(input.Artifact.Digest)
}

func validatePortableToolLockedAcquisitionV1(request PortableToolPythonVerifiedWheelRequestV1, expected providerstore.ArtifactDescriptor) error {
	lock := request.LockedAcquisition
	if lock.Scope != request.Binding.Scope || lock.Tool != request.SelectedPlanEntry.Provenance.Tool || lock.Artifact != request.Binding.Artifact {
		return fmt.Errorf("locked acquisition does not bind the selected scope, tool, or artifact reference")
	}
	if lock.Descriptor != expected || request.Acquisition.Artifact != lock.Descriptor {
		return fmt.Errorf("locked acquisition descriptor does not exactly match selected wheel")
	}
	lockSource, err := portableToolPythonSourceFromSelectedRecordV1(lock.Source, expected)
	if err != nil {
		return fmt.Errorf("locked acquisition source: %w", err)
	}
	if request.Acquisition.Source.ID != lockSource.ID || request.Acquisition.Source.SHA256 != lockSource.SHA256 || !stringSlicesEqualV1(request.Acquisition.Source.Mirrors, lockSource.Mirrors) {
		return fmt.Errorf("locked acquisition source does not exactly match the acquisition request")
	}
	if hasPortableToolSelectedRecordV1(request.SourceRecord) {
		got, err := canonical.Marshal(request.SourceRecord)
		if err != nil {
			return err
		}
		want, err := canonical.Marshal(lock.Source)
		if err != nil {
			return err
		}
		if string(got) != string(want) {
			return fmt.Errorf("locked acquisition source record does not match supplied source record")
		}
	}
	if _, _, _, err := portableToolLockedReplayValuesV1(request); err != nil {
		return err
	}
	return nil
}

func portableToolLockedReplayValuesV1(request PortableToolPythonVerifiedWheelRequestV1) (providerstore.ArtifactDescriptor, providerstore.AcquisitionProvenance, providerapi.PortableToolSelectedRecordV1, error) {
	lock := request.LockedAcquisition
	if lock == nil {
		return providerstore.ArtifactDescriptor{}, providerstore.AcquisitionProvenance{}, providerapi.PortableToolSelectedRecordV1{}, fmt.Errorf("locked replay requires a lock entry")
	}
	source, err := portableToolPythonSourceFromSelectedRecordV1(lock.Source, lock.Descriptor)
	if err != nil {
		return providerstore.ArtifactDescriptor{}, providerstore.AcquisitionProvenance{}, providerapi.PortableToolSelectedRecordV1{}, fmt.Errorf("locked replay source: %w", err)
	}
	historical, err := portableToolPythonStringArrayV1(lock.Source.Record.Value, "provenance")
	if err != nil {
		return providerstore.ArtifactDescriptor{}, providerstore.AcquisitionProvenance{}, providerapi.PortableToolSelectedRecordV1{}, err
	}
	redirects, err := strconv.Atoi(lock.Outcome.RedirectHops)
	if err != nil || redirects < 0 || redirects > providerstore.CoreMaxArtifactRedirects || strconv.Itoa(redirects) != lock.Outcome.RedirectHops {
		return providerstore.ArtifactDescriptor{}, providerstore.AcquisitionProvenance{}, providerapi.PortableToolSelectedRecordV1{}, fmt.Errorf("locked replay redirect hops must be canonical and within provider cap")
	}
	provenance := providerstore.AcquisitionProvenance{
		OperationID: "locked-replay", Outcome: lock.Outcome.Kind, SourceID: source.ID,
		SuccessfulMirror: lock.Outcome.SuccessfulDeclaredLocator, Redirects: redirects,
		Attempts: []providerstore.AcquisitionAttempt{},
	}
	if !stringSlicesEqualV1(lock.Outcome.HistoricalLocators, historical) {
		return providerstore.ArtifactDescriptor{}, providerstore.AcquisitionProvenance{}, providerapi.PortableToolSelectedRecordV1{}, fmt.Errorf("locked replay historical locators do not match source-record provenance")
	}
	switch provenance.Outcome {
	case providerstore.AcquisitionOutcomeNetwork:
		if provenance.SuccessfulMirror == "" || !portableToolPythonContainsStringV1(source.Mirrors, provenance.SuccessfulMirror) {
			return providerstore.ArtifactDescriptor{}, providerstore.AcquisitionProvenance{}, providerapi.PortableToolSelectedRecordV1{}, fmt.Errorf("locked replay network outcome has no declared successful mirror")
		}
	case providerstore.AcquisitionOutcomeCacheHit:
		if provenance.SuccessfulMirror != "" || provenance.Redirects != 0 {
			return providerstore.ArtifactDescriptor{}, providerstore.AcquisitionProvenance{}, providerapi.PortableToolSelectedRecordV1{}, fmt.Errorf("locked replay cache-hit outcome must have no locator or redirects")
		}
	default:
		return providerstore.ArtifactDescriptor{}, providerstore.AcquisitionProvenance{}, providerapi.PortableToolSelectedRecordV1{}, fmt.Errorf("locked replay acquisition outcome must be network or cache-hit")
	}
	return lock.Descriptor, provenance, clonePortableToolSelectedRecordV1(lock.Source), nil
}

func portableToolPythonContainsStringV1(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func portableToolPythonSourceFromSelectedRecordV1(selected providerapi.PortableToolSelectedRecordV1, descriptor providerstore.ArtifactDescriptor) (providerstore.ArtifactSource, error) {
	if selected.Record.Schema != portabletool.ArtifactSourceRecordSchemaV1 {
		return providerstore.ArtifactSource{}, fmt.Errorf("source record schema must be %q", portabletool.ArtifactSourceRecordSchemaV1)
	}
	if err := portabletool.ValidateRecordEnvelopeV1(canonical.Envelope(selected.Record)); err != nil {
		return providerstore.ArtifactSource{}, err
	}
	id, ok := selected.Record.Value["id"].(string)
	if !ok || id != selected.Reference.ID {
		return providerstore.ArtifactSource{}, fmt.Errorf("source record ID does not match its reference")
	}
	digest, err := canonical.Sum("portable-tool-record", portabletool.RecordIdentitySchemaV1, selected.Record.Value)
	if err != nil {
		return providerstore.ArtifactSource{}, err
	}
	if digest != selected.Reference.Digest {
		return providerstore.ArtifactSource{}, fmt.Errorf("source record digest does not match its reference")
	}
	shaValue, ok := selected.Record.Value["sha256"]
	if !ok {
		return providerstore.ArtifactSource{}, fmt.Errorf("source record sha256 is missing")
	}
	var sha string
	switch value := shaValue.(type) {
	case string:
		sha = value
	case canonical.Digest:
		sha = string(value)
	default:
		return providerstore.ArtifactSource{}, fmt.Errorf("source record sha256 must be a digest string")
	}
	mirrors, err := portableToolPythonStringArrayV1(selected.Record.Value, "mirrors")
	if err != nil {
		return providerstore.ArtifactSource{}, err
	}
	source := providerstore.ArtifactSource{ID: id, SHA256: canonical.Digest(sha), Mirrors: mirrors}
	if err := providerstore.ValidateArtifactSource(source, descriptor); err != nil {
		return providerstore.ArtifactSource{}, err
	}
	return source, nil
}

func portableToolPythonStringArrayV1(value canonical.Object, field string) ([]string, error) {
	raw, ok := value[field]
	if !ok || raw == nil {
		return nil, fmt.Errorf("source record %s must use an explicit array", field)
	}
	if values, ok := raw.([]string); ok {
		return append([]string{}, values...), nil
	}
	values, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("source record %s must be a string array", field)
	}
	result := make([]string, len(values))
	for index, item := range values {
		value, ok := item.(string)
		if !ok {
			return nil, fmt.Errorf("source record %s item %d must be a string", field, index)
		}
		result[index] = value
	}
	return result, nil
}

func hasPortableToolSelectedRecordV1(value providerapi.PortableToolSelectedRecordV1) bool {
	return value.Reference.ID != "" || value.Reference.Digest != "" || value.Record.Schema != "" || value.Record.Value != nil
}

func clonePortableToolSelectedRecordV1(value providerapi.PortableToolSelectedRecordV1) providerapi.PortableToolSelectedRecordV1 {
	encoded, err := canonical.Marshal(value)
	if err != nil {
		return providerapi.PortableToolSelectedRecordV1{Reference: value.Reference, Record: value.Record}
	}
	var clone providerapi.PortableToolSelectedRecordV1
	if err := json.Unmarshal(encoded, &clone); err != nil {
		return providerapi.PortableToolSelectedRecordV1{Reference: value.Reference, Record: value.Record}
	}
	return clone
}

func clonePortableToolAcquisitionProvenanceV1(value providerstore.AcquisitionProvenance) providerstore.AcquisitionProvenance {
	value.Attempts = append([]providerstore.AcquisitionAttempt{}, value.Attempts...)
	return value
}
