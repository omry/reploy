package python

import (
	"fmt"
	"path"
	"strings"

	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/portabletool"
	providerapi "github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providerstore"
	"github.com/omry/reploy/internal/wheelinventory"
)

// PortableToolPythonBindingVerificationInputV1 joins the immutable values
// produced by the portable-tool projection, interpreter inspection, wheel
// acquisition, and wheel metadata inspection. Verification consumes this
// value only; it neither opens an artifact nor performs any other I/O.
//
// ContractRecord and ArtifactRecord are retained as selected-record envelopes
// because the provider projection intentionally carries references rather than
// duplicating bundled-component declarations.
type PortableToolPythonBindingVerificationInputV1 struct {
	Component      PortableToolPythonComponentV1
	Binding        PortableToolPythonBindingV1
	ContractRecord providerapi.PortableToolSelectedRecordV1
	ArtifactRecord providerapi.PortableToolSelectedRecordV1
	Interpreter    providerapi.ExecutableEvidence
	Descriptor     providerstore.ArtifactDescriptor
	Inspection     WheelInspectionV1
}

// PortableToolPythonBindingVerificationV1 is the authenticated result of
// verifying one selected binding wheel. The selected script target and the
// eligible filename tags are observations derived from the exact wheel bytes;
// callers may carry them into resolver or lock inputs without reparsing the
// wheel or recomputing interpreter compatibility.
type PortableToolPythonBindingVerificationV1 struct {
	Binding              PortableToolPythonBindingV1
	Descriptor           providerstore.ArtifactDescriptor
	Inspection           WheelInspectionV1
	EligibleFilenameTags []string
	ConsoleScript        WheelConsoleScriptV1
}

// VerifyPortableToolPythonBindingV1 verifies one selected projected binding
// against its exact selected records, interpreter evidence, acquired
// descriptor, and descriptor-authenticated wheel inspection.
func VerifyPortableToolPythonBindingV1(
	input PortableToolPythonBindingVerificationInputV1,
) (PortableToolPythonBindingVerificationV1, error) {
	var result PortableToolPythonBindingVerificationV1

	// Validate the projected component before accepting a binding supplied by a
	// caller. This also confirms its static wheel identity, supported-tag
	// envelope, scope, closure digest, CLI export, and canonical collections.
	if _, err := CanonicalPortableToolPythonProjectionBytesV1(PortableToolPythonProjectionV1{
		Schema:     PortableToolPythonProjectionSchemaV1,
		Components: []PortableToolPythonComponentV1{input.Component},
	}); err != nil {
		return result, fmt.Errorf("portable Python binding projection: %w", err)
	}
	bindingIndex := -1
	for index, candidate := range input.Component.Bindings {
		if candidate.Distribution != input.Binding.Distribution {
			continue
		}
		equal, err := portableToolPythonBindingsEqualV1(candidate, input.Binding)
		if err != nil {
			return result, fmt.Errorf("portable Python binding projection comparison: %w", err)
		}
		if !equal {
			return result, fmt.Errorf("portable Python binding %q does not equal its projected binding", input.Binding.Distribution)
		}
		bindingIndex = index
		break
	}
	if bindingIndex < 0 {
		return result, fmt.Errorf("portable Python binding %q is absent from its projected component", input.Binding.Distribution)
	}

	contract, err := decodeVerifiedPortableToolBindingRecordV1[portabletool.BindingContractV1](
		input.ContractRecord, input.Binding.Contract, portabletool.BindingContractSchemaV1,
	)
	if err != nil {
		return result, fmt.Errorf("portable Python binding contract: %w", err)
	}
	artifact, err := decodeVerifiedPortableToolBindingRecordV1[portabletool.BindingArtifactRecordV1](
		input.ArtifactRecord, input.Binding.Artifact, portabletool.BindingArtifactSchemaV1,
	)
	if err != nil {
		return result, fmt.Errorf("portable Python binding artifact: %w", err)
	}
	if input.ArtifactRecord.Reference != input.Binding.Artifact {
		return result, fmt.Errorf("portable Python binding artifact reference does not match its projection")
	}
	if input.ContractRecord.Reference != input.Binding.Contract {
		return result, fmt.Errorf("portable Python binding contract reference does not match its projection")
	}
	if artifact.Contract != input.Binding.Contract {
		return result, fmt.Errorf("portable Python binding artifact contract reference does not match its projection")
	}
	if artifact.Binding != contract.Name {
		return result, fmt.Errorf("portable Python binding artifact binding %q does not match contract %q", artifact.Binding, contract.Name)
	}
	if err := portabletool.ValidateBindingBundledComponentAgreementV1(contract.BundledComponents, artifact.BundledComponents); err != nil {
		return result, fmt.Errorf("portable Python binding bundled components: %w", err)
	}
	if err := validatePortableToolPythonBundledPathsV1(artifact.BundledComponents, input.Inspection.Inventory); err != nil {
		return result, err
	}

	contractDistribution, err := PackageRootDistributionNameV1(contract.Package)
	if err != nil {
		return result, fmt.Errorf("portable Python binding contract package: %w", err)
	}
	artifactDistribution, err := PackageRootDistributionNameV1(artifact.Name)
	if err != nil {
		return result, fmt.Errorf("portable Python binding artifact distribution: %w", err)
	}
	if contractDistribution != input.Binding.Distribution || artifactDistribution != input.Binding.Distribution ||
		input.Binding.Wheel.Distribution != input.Binding.Distribution {
		return result, fmt.Errorf("portable Python binding distribution does not agree across projection and selected records")
	}
	if artifact.EcosystemVersion != input.Binding.Wheel.EcosystemVersion ||
		artifact.Filename != input.Binding.Wheel.Filename ||
		artifact.Platform != input.Binding.Wheel.Platform ||
		!stringSlicesEqualV1(artifact.Tags, input.Binding.Wheel.Tags) ||
		artifact.Size != input.Binding.Wheel.Size || artifact.SHA256 != input.Binding.Wheel.SHA256 ||
		artifact.RequiresPython != input.Binding.Wheel.RequiresPython {
		return result, fmt.Errorf("portable Python binding artifact metadata does not match its projection")
	}
	if !stringSlicesEqualV1(
		input.Binding.SupportedTags,
		sortedStringIntersectionV1(contract.SupportedTags, artifact.Tags),
	) {
		return result, fmt.Errorf("portable Python binding supported tags do not match the selected contract and artifact")
	}
	if !stringSlicesEqualV1(contract.Requirements, input.Binding.Requirements) ||
		!stringSlicesEqualV1(contract.SupportedPython, input.Binding.SupportedPython) ||
		contract.CLI != input.Binding.CLI {
		return result, fmt.Errorf("portable Python binding contract metadata does not match its projection")
	}

	if err := input.Descriptor.Validate(); err != nil {
		return result, fmt.Errorf("portable Python binding acquired descriptor: %w", err)
	}
	if input.Descriptor.Kind != "wheel" || input.Descriptor.LogicalPath != path.Join("wheels", input.Binding.Wheel.Filename) {
		return result, fmt.Errorf("portable Python binding acquired descriptor does not match wheel filename or kind")
	}
	if input.Descriptor.Size != input.Binding.Wheel.Size || input.Descriptor.SHA256 != input.Binding.Wheel.SHA256 {
		return result, fmt.Errorf("portable Python binding acquired descriptor does not match selected artifact identity")
	}
	if input.Inspection.Artifact != input.Descriptor {
		return result, fmt.Errorf("portable Python binding wheel inspection descriptor does not match acquired descriptor")
	}
	if err := validatePortableToolPythonWheelInspectionV1(input.Inspection, input.Binding.Wheel); err != nil {
		return result, err
	}

	facts, err := DecodeInterpreterFactsV2(input.Interpreter.Facts)
	if err != nil {
		return result, fmt.Errorf("portable Python binding interpreter evidence: %w", err)
	}
	if !stringSlicesEqualV1(facts.TestedTags, input.Component.TestedTags) {
		return result, fmt.Errorf("portable Python binding interpreter tested tags do not match its projection")
	}
	claims, err := providerapi.IntersectSupportedPythonClaimsV1(input.Binding.SupportedPython, []string{facts.Version})
	if err != nil {
		return result, fmt.Errorf("portable Python binding supported interpreter claims: %w", err)
	}
	if len(claims) == 0 {
		return result, fmt.Errorf("portable Python binding %q does not support inspected interpreter %s", input.Binding.Distribution, facts.Version)
	}
	if matches, err := InterpreterVersionSatisfies(input.Binding.Wheel.RequiresPython, facts.Version); err != nil {
		return result, fmt.Errorf("portable Python binding %q Requires-Python: %w", input.Binding.Distribution, err)
	} else if !matches {
		return result, fmt.Errorf("portable Python binding %q Requires-Python excludes inspected interpreter %s", input.Binding.Distribution, facts.Version)
	}
	eligible, err := portableToolPythonBindingEligibleFilenameTagsV1(input.Binding, facts)
	if err != nil {
		return result, err
	}
	if !portableToolPythonInternalTagsAgreeV1(eligible, input.Inspection.InternalTags, input.Inspection.RootIsPurelib) {
		return result, fmt.Errorf("portable Python binding %q internal wheel tags do not agree with eligible filename tags", input.Binding.Distribution)
	}

	if err := validatePortableToolPythonBindingCLIExportV1(input.Binding.CLI, input.Inspection.ConsoleScripts); err != nil {
		return result, err
	}
	var script WheelConsoleScriptV1
	for _, candidate := range input.Inspection.ConsoleScripts {
		if candidate.Name == input.Binding.CLI.Name {
			script = candidate
		}
	}

	result.Binding = clonePortableToolPythonBindingV1(input.Binding)
	result.Descriptor = input.Descriptor
	result.Inspection = cloneWheelInspectionV1(input.Inspection)
	result.EligibleFilenameTags = append([]string{}, eligible...)
	result.ConsoleScript = script
	return result, nil
}

// Internal tags cannot make a filename tag eligible. A purelib wheel may use
// an internally generic platform tag for the same Python and ABI dimensions
// as an eligible, platform-limited filename tag. The already accepted Python
// and ABI dimensions make the generic platform tag compatible without adding
// it to the pre-acquisition interpreter probe or pip's selected filename.
func portableToolPythonInternalTagsAgreeV1(
	eligible, internal []string,
	rootIsPurelib bool,
) bool {
	if len(sortedStringIntersectionV1(eligible, internal)) != 0 {
		return true
	}
	if !rootIsPurelib {
		return false
	}
	for _, filenameTag := range eligible {
		filenameParts := strings.Split(filenameTag, "-")
		for _, internalTag := range internal {
			internalParts := strings.Split(internalTag, "-")
			if internalParts[2] == "any" && filenameParts[0] == internalParts[0] &&
				filenameParts[1] == internalParts[1] {
				return true
			}
		}
	}
	return false
}

// validatePortableToolPythonBindingCLIExportV1 requires exactly one selected
// console-script entry and revalidates its generic target grammar. The target
// is observed wheel data; schema v1 has no independent expected target.
func validatePortableToolPythonBindingCLIExportV1(
	export portabletool.ToolExportV1,
	scripts []WheelConsoleScriptV1,
) error {
	if err := portabletool.ValidateBindingCLIExportV1(export); err != nil {
		return fmt.Errorf("portable Python binding CLI: %w", err)
	}
	found := 0
	for _, script := range scripts {
		if script.Name != export.Name {
			continue
		}
		if err := ValidateWheelConsoleScriptTargetV1(script.Target); err != nil {
			return fmt.Errorf("portable Python binding CLI %q target: %w", export.Name, err)
		}
		found++
	}
	if found != 1 {
		return fmt.Errorf("portable Python binding CLI %q requires exactly one console script, found %d", export.Name, found)
	}
	return nil
}

func decodeVerifiedPortableToolBindingRecordV1[T any](
	selected providerapi.PortableToolSelectedRecordV1,
	expected providerapi.PortableToolRecordReferenceV1,
	schema string,
) (T, error) {
	var result T
	if selected.Reference != expected {
		return result, fmt.Errorf("selected record reference does not match projection")
	}
	if err := selected.Reference.Digest.Validate(); err != nil {
		return result, fmt.Errorf("selected record reference digest: %w", err)
	}
	if selected.Record.Schema != schema {
		return result, fmt.Errorf("record schema must be %q", schema)
	}
	if err := portabletool.ValidateRecordEnvelopeV1(canonical.Envelope(selected.Record)); err != nil {
		return result, err
	}
	if id, ok := selected.Record.Value["id"].(string); !ok || id != selected.Reference.ID {
		return result, fmt.Errorf("selected record ID does not match its reference")
	}
	digest, err := canonical.Sum("portable-tool-record", portabletool.RecordIdentitySchemaV1, selected.Record.Value)
	if err != nil {
		return result, fmt.Errorf("selected record digest: %w", err)
	}
	if digest != selected.Reference.Digest {
		return result, fmt.Errorf("selected record digest does not match its reference")
	}
	return decodePortableToolRecordV1[T](selected, schema)
}

func validatePortableToolPythonWheelInspectionV1(
	inspection WheelInspectionV1,
	wheel PortableToolExactWheelConstraintV1,
) error {
	if inspection.Filename != wheel.Filename {
		return fmt.Errorf("portable Python wheel inspection filename does not match selected artifact")
	}
	filename, err := portabletool.ProjectWheelFilenameV1(inspection.Filename)
	if err != nil {
		return fmt.Errorf("portable Python wheel inspection filename: %w", err)
	}
	if filename.Distribution != wheel.Distribution || filename.EcosystemVersion != wheel.EcosystemVersion ||
		!stringSlicesEqualV1(filename.Tags, wheel.Tags) ||
		!stringSlicesEqualV1(inspection.FilenameTags, filename.Tags) {
		return fmt.Errorf("portable Python wheel inspection filename metadata does not match selected artifact")
	}
	if inspection.Distribution != wheel.Distribution || inspection.Version != wheel.EcosystemVersion ||
		inspection.RequiresPython != wheel.RequiresPython {
		return fmt.Errorf("portable Python wheel core metadata does not match selected artifact")
	}
	if err := portabletool.ValidatePythonRequiresPythonV1(inspection.RequiresPython); err != nil {
		return fmt.Errorf("portable Python wheel Requires-Python: %w", err)
	}
	if err := validatePortableToolPythonObservedTagsV1(inspection.InternalTags, "internal"); err != nil {
		return err
	}
	if err := validatePortableToolPythonObservedInventoryV1(inspection.Inventory); err != nil {
		return err
	}
	return nil
}

func validatePortableToolPythonObservedTagsV1(tags []string, field string) error {
	if tags == nil {
		return fmt.Errorf("portable Python wheel %s tags must use an array", field)
	}
	for index, tag := range tags {
		if err := portabletool.ValidateWheelTagV1(tag); err != nil {
			return fmt.Errorf("portable Python wheel %s tag %q: %w", field, tag, err)
		}
		if index > 0 && tags[index-1] >= tag {
			return fmt.Errorf("portable Python wheel %s tags must be unique and sorted", field)
		}
	}
	return nil
}

func validatePortableToolPythonObservedInventoryV1(inventory []WheelInventoryPathV1) error {
	if inventory == nil {
		return fmt.Errorf("portable Python wheel inventory must use an array")
	}
	seen := map[string]struct{}{}
	seenPortable := map[string]string{}
	for index, entry := range inventory {
		if entry.Kind != wheelinventory.Regular && entry.Kind != wheelinventory.Directory {
			return fmt.Errorf("portable Python wheel inventory path %q has unsupported kind %q", entry.Path, entry.Kind)
		}
		normalized, err := providerstore.NormalizeArchivePath(entry.Path, entry.Kind == wheelinventory.Directory)
		if err != nil || normalized != entry.Path {
			return fmt.Errorf("portable Python wheel inventory path %q is not canonical", entry.Path)
		}
		if entry.Kind == wheelinventory.Directory && entry.UncompressedSize != 0 {
			return fmt.Errorf("portable Python wheel inventory directory %q has a nonzero size", entry.Path)
		}
		if _, found := seen[entry.Path]; found {
			return fmt.Errorf("portable Python wheel inventory repeats path %q", entry.Path)
		}
		seen[entry.Path] = struct{}{}
		portableKey := providerstore.PortableArchiveDestinationKey(entry.Path)
		if previous, found := seenPortable[portableKey]; found && previous != entry.Path {
			return fmt.Errorf("portable Python wheel inventory paths %q and %q conflict", previous, entry.Path)
		}
		seenPortable[portableKey] = entry.Path
		if index > 0 && inventory[index-1].Path >= entry.Path {
			return fmt.Errorf("portable Python wheel inventory must be unique and sorted")
		}
	}
	return nil
}

func validatePortableToolPythonBundledPathsV1(
	declared []portabletool.BundledComponentV1,
	inventory []WheelInventoryPathV1,
) error {
	portablePaths := map[string]string{}
	for _, component := range declared {
		normalized, err := providerstore.NormalizeArchivePath(component.Path, false)
		if err != nil || normalized != component.Path {
			return fmt.Errorf("bundled component %q path %q is unsafe", component.Name, component.Path)
		}
		portableKey := providerstore.PortableArchiveDestinationKey(component.Path)
		for previousKey, previous := range portablePaths {
			if portableKey == previousKey || strings.HasPrefix(portableKey, previousKey+"/") || strings.HasPrefix(previousKey, portableKey+"/") {
				return fmt.Errorf("bundled component paths %q and %q conflict", previous, component.Path)
			}
		}
		portablePaths[portableKey] = component.Path
	}
	for _, component := range declared {
		foundFile := false
		foundDescendantFile := false
		for _, entry := range inventory {
			if entry.Path == component.Path && entry.Kind == wheelinventory.Regular {
				foundFile = entry.UncompressedSize > 0
				break
			}
			if strings.HasPrefix(entry.Path, component.Path+"/") && entry.Kind == wheelinventory.Regular && entry.UncompressedSize > 0 {
				foundDescendantFile = true
			}
		}
		if !foundFile && !foundDescendantFile {
			return fmt.Errorf("bundled component %q path %q is absent or empty in the inspected wheel", component.Name, component.Path)
		}
	}
	return nil
}

func clonePortableToolPythonBindingV1(binding PortableToolPythonBindingV1) PortableToolPythonBindingV1 {
	binding.Requirements = append([]string{}, binding.Requirements...)
	binding.SupportedPython = append([]string{}, binding.SupportedPython...)
	binding.SupportedTags = append([]string{}, binding.SupportedTags...)
	binding.Wheel.Tags = append([]string{}, binding.Wheel.Tags...)
	return binding
}

func cloneWheelInspectionV1(inspection WheelInspectionV1) WheelInspectionV1 {
	inspection.FilenameTags = append([]string{}, inspection.FilenameTags...)
	inspection.InternalTags = append([]string{}, inspection.InternalTags...)
	inspection.ConsoleScripts = append([]WheelConsoleScriptV1{}, inspection.ConsoleScripts...)
	inspection.Inventory = append([]WheelInventoryPathV1{}, inspection.Inventory...)
	inspection.DeclaredDependencies = append([]string{}, inspection.DeclaredDependencies...)
	return inspection
}
