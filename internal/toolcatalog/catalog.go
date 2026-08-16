package toolcatalog

import (
	"embed"
	"fmt"
	"io"
	"io/fs"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/omry/reploy/internal/canonical"
)

const (
	maxCatalogRecordsV1        = 4096
	maxCatalogBytesV1          = 64 << 20
	maxCatalogReferenceEdgesV1 = 65536
	maxCatalogGraphDepthV1     = 64
	maxSelectedContributionsV1 = 4096
)

type catalogLimitsV1 struct {
	Records        int
	Bytes          int
	ReferenceEdges int
}

var defaultCatalogLimitsV1 = catalogLimitsV1{
	Records: maxCatalogRecordsV1, Bytes: maxCatalogBytesV1, ReferenceEdges: maxCatalogReferenceEdgesV1,
}

//go:embed definitions
var definitionFilesV1 embed.FS

type CatalogV1 struct {
	records map[string]loadedRecordV1
	tools   map[string]string
}

type ToolRequestV1 struct {
	Name       string
	Version    string
	Revision   string
	Context    string
	Binding    string
	Selections []string
}

type ReleaseProvenanceV1 struct {
	Tool           string           `json:"tool"`
	Version        string           `json:"version"`
	Revision       string           `json:"revision"`
	ManifestDigest canonical.Digest `json:"manifest_digest"`
}

type SelectedClosureV1 struct {
	Tool               ToolRecordV1
	Manifest           ReleaseManifestV1
	Contract           ReleaseContractV1
	Target             TargetRecordV1
	Binding            *BindingContractV1
	BindingArtifact    *BindingArtifactRecordV1
	Payloads           []PayloadRecordV1
	PackageSets        []NativePackageSetV1
	Sources            []ArtifactSourceRecordV1
	IntegrationFixture IntegrationFixtureRecordV1
	ValidationProfile  ValidationProfileRecordV1
	SelectedRecords    []RecordReferenceV1
	BindingName        string
	Selections         []string
	ReleaseProvenance  ReleaseProvenanceV1
	Digest             canonical.Digest
}

type selectedClosureIdentityInputV1 struct {
	Tool     string                         `json:"tool"`
	Version  string                         `json:"version"`
	Contract selectedContractContributionV1 `json:"contract"`
	Target   selectedTargetContributionV1   `json:"target"`
	Records  []RecordReferenceV1            `json:"records"`
}

type selectedContractContributionV1 struct {
	Context    string           `json:"context"`
	Binding    string           `json:"binding"`
	Selections []string         `json:"selections"`
	Runtime    *RecordRuntimeV1 `json:"runtime,omitempty"`
	Probes     []RecordProbeV1  `json:"probes"`
	Exports    []ToolExportV1   `json:"exports"`
}

type selectedTargetContributionV1 struct {
	Identity           TargetIdentityV1    `json:"identity"`
	PackageSets        []RecordReferenceV1 `json:"package_sets"`
	Binding            *TargetBindingV1    `json:"binding,omitempty"`
	Payloads           []RecordReferenceV1 `json:"payloads"`
	Selections         []TargetSelectionV1 `json:"selections"`
	Probes             []RecordProbeV1     `json:"probes"`
	IntegrationFixture RecordReferenceV1   `json:"integration_fixture"`
	ValidationProfile  RecordReferenceV1   `json:"validation_profile"`
}

var (
	embeddedCatalogOnceV1 sync.Once
	embeddedCatalogV1     *CatalogV1
)

func mustLoadCatalogV1() *CatalogV1 {
	embeddedCatalogOnceV1.Do(func() {
		catalog, err := loadCatalogV1(definitionFilesV1, "definitions")
		if err != nil {
			panic(fmt.Sprintf("load embedded portable tool catalog: %v", err))
		}
		embeddedCatalogV1 = catalog
	})
	return embeddedCatalogV1
}

func loadCatalogV1(files fs.FS, root string) (*CatalogV1, error) {
	return loadCatalogWithLimitsV1(files, root, defaultCatalogLimitsV1)
}

func loadCatalogWithLimitsV1(files fs.FS, root string, limits catalogLimitsV1) (*CatalogV1, error) {
	if limits.Records <= 0 || limits.Bytes <= 0 || limits.ReferenceEdges <= 0 {
		return nil, fmt.Errorf("catalog limits must be positive")
	}
	catalog := &CatalogV1{
		records: make(map[string]loadedRecordV1),
		tools:   make(map[string]string),
	}
	fileCount := 0
	totalBytes := 0
	referenceEdges := 0
	err := fs.WalkDir(files, root, func(filename string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		if path.Ext(filename) != ".json" {
			return fmt.Errorf("catalog entry %q must be a JSON file", filename)
		}
		fileCount++
		if fileCount > limits.Records {
			return fmt.Errorf("catalog contains more than %d records", limits.Records)
		}
		payload, err := readCatalogRecordV1(files, filename)
		if err != nil {
			return err
		}
		totalBytes += len(payload)
		if totalBytes > limits.Bytes {
			return fmt.Errorf("catalog contains more than %d bytes", limits.Bytes)
		}
		record, err := decodeRecordV1(filename, payload)
		if err != nil {
			return err
		}
		referenceEdges += len(catalogReferencesV1(record.Value))
		if referenceEdges > limits.ReferenceEdges {
			return fmt.Errorf("catalog contains more than %d reference edges", limits.ReferenceEdges)
		}
		if _, exists := catalog.records[record.ID]; exists {
			return fmt.Errorf("catalog contains duplicate record ID %q", record.ID)
		}
		relative := strings.TrimPrefix(filename, strings.TrimSuffix(root, "/")+"/")
		toolName, err := recordToolNameV1(record.ID)
		if err != nil {
			return fmt.Errorf("catalog entry %q: %w", filename, err)
		}
		if relative == filename || !strings.HasPrefix(relative, toolName+"/") {
			return fmt.Errorf("catalog entry %q must live below %q", filename, toolName)
		}
		if record.Schema == ToolRecordSchemaV1 {
			if relative != toolName+"/tool.json" {
				return fmt.Errorf("tool record %q must use path %q", record.ID, toolName+"/tool.json")
			}
			if _, exists := catalog.tools[toolName]; exists {
				return fmt.Errorf("catalog contains duplicate tool %q", toolName)
			}
			catalog.tools[toolName] = record.ID
		}
		catalog.records[record.ID] = record
		return nil
	})
	if err != nil {
		return nil, err
	}
	if fileCount == 0 || len(catalog.tools) == 0 {
		return nil, fmt.Errorf("portable tool catalog is empty")
	}
	if err := catalog.validate(); err != nil {
		return nil, err
	}
	return catalog, nil
}

func readCatalogRecordV1(files fs.FS, filename string) ([]byte, error) {
	file, err := files.Open(filename)
	if err != nil {
		return nil, err
	}
	payload, readErr := io.ReadAll(io.LimitReader(file, maxDefinitionFileBytes+1))
	closeErr := file.Close()
	if readErr != nil {
		return nil, readErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if len(payload) > maxDefinitionFileBytes {
		return nil, fmt.Errorf("catalog entry %q exceeds the %d-byte record limit", filename, maxDefinitionFileBytes)
	}
	return payload, nil
}

func Names() []string {
	return mustLoadCatalogV1().Names()
}

func (catalog *CatalogV1) Names() []string {
	names := make([]string, 0, len(catalog.tools))
	for name := range catalog.tools {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func Resolve(request ToolRequestV1, target TargetIdentityV1) (SelectedClosureV1, error) {
	return mustLoadCatalogV1().Resolve(request, target)
}

func (catalog *CatalogV1) Resolve(request ToolRequestV1, observed TargetIdentityV1) (SelectedClosureV1, error) {
	if !validRecordIdentifierV1(request.Name) {
		return SelectedClosureV1{}, fmt.Errorf("tool name is invalid")
	}
	if request.Version != "" && !validRecordSegmentV1(request.Version) {
		return SelectedClosureV1{}, fmt.Errorf("tool version is invalid")
	}
	if request.Revision != "" {
		if err := validateCanonicalDecimalV1("tool definition revision", request.Revision, true); err != nil {
			return SelectedClosureV1{}, err
		}
	}
	if err := validateTargetIdentityV1(observed); err != nil {
		return SelectedClosureV1{}, fmt.Errorf("observed target: %w", err)
	}
	toolID, exists := catalog.tools[request.Name]
	if !exists {
		return SelectedClosureV1{}, fmt.Errorf("portable tool %q is not defined", request.Name)
	}
	toolRecord := catalog.records[toolID]
	tool := toolRecord.Value.(*ToolRecordV1)
	manifestRecord, err := catalog.selectManifest(tool, request.Version, request.Revision)
	if err != nil {
		return SelectedClosureV1{}, err
	}
	manifest := manifestRecord.Value.(*ReleaseManifestV1)
	contractRecord := catalog.records[manifest.Contract.ID]
	contract := contractRecord.Value.(*ReleaseContractV1)
	if !containsRecordValueV1(contract.Contexts, request.Context) {
		return SelectedClosureV1{}, fmt.Errorf("portable tool %q does not support context %q", request.Name, request.Context)
	}
	targetRecord, err := catalog.selectTarget(manifest, observed)
	if err != nil {
		return SelectedClosureV1{}, err
	}
	target := targetRecord.Value.(*TargetRecordV1)
	fixture := catalog.records[target.IntegrationFixture.ID].Value.(*IntegrationFixtureRecordV1)
	profile := catalog.records[target.ValidationProfile.ID].Value.(*ValidationProfileRecordV1)
	bindingName, err := resolveBindingV1(contract.Binding, request.Binding)
	if err != nil {
		return SelectedClosureV1{}, fmt.Errorf("portable tool %q binding: %w", request.Name, err)
	}
	selections, err := resolveSelectionsV1(contract.Selections, request.Selections)
	if err != nil {
		return SelectedClosureV1{}, fmt.Errorf("portable tool %q selections: %w", request.Name, err)
	}
	selectedReferences := []RecordReferenceV1{}
	selectedReferences = append(selectedReferences, target.PackageSets...)
	selectedReferences = append(selectedReferences, target.Payloads...)

	var selectedBinding *BindingContractV1
	var selectedBindingArtifact *BindingArtifactRecordV1
	var selectedTargetBinding *TargetBindingV1
	if bindingName != "" {
		binding, found := targetBindingV1(target.Bindings, bindingName)
		if !found {
			return SelectedClosureV1{}, fmt.Errorf("portable tool %q target does not provide binding %q", request.Name, bindingName)
		}
		bindingRecord := catalog.records[binding.Contract.ID]
		artifactRecord := catalog.records[binding.Artifact.ID]
		bindingValue := cloneBindingContractV1(bindingRecord.Value.(*BindingContractV1))
		artifactValue := cloneBindingArtifactV1(artifactRecord.Value.(*BindingArtifactRecordV1))
		selectedBinding = &bindingValue
		selectedBindingArtifact = &artifactValue
		bindingCopy := binding
		selectedTargetBinding = &bindingCopy
		selectedReferences = append(selectedReferences, binding.Contract, binding.Artifact)
	}
	selectedTargetSelections := make([]TargetSelectionV1, 0, len(selections))
	for _, selectionName := range selections {
		selection, found := targetSelectionV1(target.Selections, selectionName)
		if !found {
			return SelectedClosureV1{}, fmt.Errorf("portable tool %q target does not provide selection %q", request.Name, selectionName)
		}
		selection.Payloads = append([]RecordReferenceV1{}, selection.Payloads...)
		selectedTargetSelections = append(selectedTargetSelections, selection)
		selectedReferences = append(selectedReferences, selection.Payloads...)
	}
	selectedReferences, err = canonicalReferenceUnionV1(selectedReferences)
	if err != nil {
		return SelectedClosureV1{}, err
	}
	if err := catalog.validateSelectedContributions(selectedReferences); err != nil {
		return SelectedClosureV1{}, err
	}
	payloads, packageSets := catalog.selectedMaterializationRecords(selectedReferences)
	sources, err := catalog.selectedSources(manifest, selectedReferences)
	if err != nil {
		return SelectedClosureV1{}, err
	}
	closureDigest, err := selectedClosureDigestV1(
		request.Name, manifest.Version, request.Context, bindingName, selections,
		contract, target, selectedTargetBinding, selectedTargetSelections, selectedReferences,
	)
	if err != nil {
		return SelectedClosureV1{}, fmt.Errorf("compute selected closure: %w", err)
	}
	return SelectedClosureV1{
		Tool:               cloneToolRecordV1(tool),
		Manifest:           cloneReleaseManifestV1(manifest),
		Contract:           cloneReleaseContractV1(contract),
		Target:             cloneTargetRecordV1(target),
		Binding:            selectedBinding,
		BindingArtifact:    selectedBindingArtifact,
		Payloads:           payloads,
		PackageSets:        packageSets,
		Sources:            sources,
		IntegrationFixture: cloneIntegrationFixtureV1(fixture),
		ValidationProfile:  *profile,
		SelectedRecords:    append([]RecordReferenceV1{}, selectedReferences...),
		BindingName:        bindingName,
		Selections:         append([]string{}, selections...),
		ReleaseProvenance:  ReleaseProvenanceV1{Tool: request.Name, Version: manifest.Version, Revision: manifest.Revision, ManifestDigest: manifestRecord.Digest},
		Digest:             closureDigest,
	}, nil
}

func selectedClosureDigestV1(
	tool string,
	version string,
	contextName string,
	bindingName string,
	selections []string,
	contract *ReleaseContractV1,
	target *TargetRecordV1,
	selectedBinding *TargetBindingV1,
	selectedTargetSelections []TargetSelectionV1,
	selectedReferences []RecordReferenceV1,
) (canonical.Digest, error) {
	return canonical.Sum(
		"portable-tool-selected-closure",
		SelectedClosureIdentityV1,
		selectedClosureIdentityInputV1{
			Tool:    tool,
			Version: version,
			Contract: selectedContractContributionV1{
				Context: contextName, Binding: bindingName, Selections: append([]string{}, selections...),
				Runtime: cloneRuntimeV1(contract.Runtime), Probes: cloneProbesV1(contract.Probes),
				Exports: append([]ToolExportV1{}, contract.Exports...),
			},
			Target: selectedTargetContributionV1{
				Identity: target.Target, PackageSets: append([]RecordReferenceV1{}, target.PackageSets...),
				Binding: selectedBinding, Payloads: append([]RecordReferenceV1{}, target.Payloads...),
				Selections: cloneTargetSelectionsV1(selectedTargetSelections), Probes: cloneProbesV1(target.Probes),
				IntegrationFixture: target.IntegrationFixture, ValidationProfile: target.ValidationProfile,
			},
			Records: append([]RecordReferenceV1{}, selectedReferences...),
		},
	)
}

func (catalog *CatalogV1) selectManifest(tool *ToolRecordV1, version string, revision string) (loadedRecordV1, error) {
	candidates := make([]loadedRecordV1, 0, len(tool.Releases))
	for _, reference := range tool.Releases {
		record := catalog.records[reference.ID]
		manifest := record.Value.(*ReleaseManifestV1)
		if version != "" && !matchesRequestedVersionV1(manifest.Version, manifest.Aliases, version) {
			continue
		}
		if revision != "" && manifest.Revision != revision {
			continue
		}
		candidates = append(candidates, record)
	}
	if len(candidates) == 0 {
		return loadedRecordV1{}, fmt.Errorf("portable tool %q has no release matching version %q revision %q", tool.Name, version, revision)
	}
	selectedVersion := candidates[0].Value.(*ReleaseManifestV1).Version
	for _, candidate := range candidates[1:] {
		if candidate.Value.(*ReleaseManifestV1).Version != selectedVersion {
			if version == "" {
				return loadedRecordV1{}, fmt.Errorf("portable tool %q has multiple releases; an explicit version is required", tool.Name)
			}
			return loadedRecordV1{}, fmt.Errorf("portable tool %q version %q is ambiguous", tool.Name, version)
		}
	}
	sort.Slice(candidates, func(left int, right int) bool {
		leftManifest := candidates[left].Value.(*ReleaseManifestV1)
		rightManifest := candidates[right].Value.(*ReleaseManifestV1)
		leftRevision, _ := strconv.ParseUint(leftManifest.Revision, 10, 63)
		rightRevision, _ := strconv.ParseUint(rightManifest.Revision, 10, 63)
		return leftRevision > rightRevision
	})
	return candidates[0], nil
}

func matchesRequestedVersionV1(upstream string, aliases []string, requested string) bool {
	if upstream == requested {
		return true
	}
	return containsRecordValueV1(aliases, requested)
}

func (catalog *CatalogV1) selectTarget(manifest *ReleaseManifestV1, observed TargetIdentityV1) (loadedRecordV1, error) {
	var selected *loadedRecordV1
	for _, reference := range manifest.Targets {
		record := catalog.records[reference.ID]
		target := record.Value.(*TargetRecordV1)
		if target.Target == observed {
			if selected != nil {
				return loadedRecordV1{}, fmt.Errorf("portable tool %q has ambiguous target %s %s on %s", manifest.Tool, observed.OSReleaseID, observed.VersionID, observed.Platform)
			}
			copy := record
			selected = &copy
		}
	}
	if selected == nil {
		return loadedRecordV1{}, fmt.Errorf("portable tool %q has no target for %s %s on %s (%s)", manifest.Tool, observed.OSReleaseID, observed.VersionID, observed.Platform, observed.NativeArchitecture)
	}
	return *selected, nil
}

func resolveBindingV1(contract BindingRequestV1, requested string) (string, error) {
	if requested == "" {
		requested = contract.Default
	}
	if requested == "" && contract.Required {
		return "", fmt.Errorf("a binding is required")
	}
	if requested != "" && !containsRecordValueV1(contract.Options, requested) {
		return "", fmt.Errorf("binding %q is not supported", requested)
	}
	return requested, nil
}

func resolveSelectionsV1(contract SelectionRequestV1, requested []string) ([]string, error) {
	if requested == nil {
		requested = contract.Defaults
	}
	selected := append([]string{}, requested...)
	sort.Strings(selected)
	for index, value := range selected {
		if !containsRecordValueV1(contract.Options, value) {
			return nil, fmt.Errorf("selection %q is not supported", value)
		}
		if index > 0 && selected[index-1] == value {
			return nil, fmt.Errorf("selection %q is duplicated", value)
		}
	}
	minimum, _ := strconv.ParseUint(contract.Minimum, 10, 63)
	if uint64(len(selected)) < minimum {
		return nil, fmt.Errorf("at least %s selection(s) are required", contract.Minimum)
	}
	return selected, nil
}

func targetBindingV1(bindings []TargetBindingV1, name string) (TargetBindingV1, bool) {
	for _, binding := range bindings {
		if binding.Name == name {
			return binding, true
		}
	}
	return TargetBindingV1{}, false
}

func targetSelectionV1(selections []TargetSelectionV1, name string) (TargetSelectionV1, bool) {
	for _, selection := range selections {
		if selection.Name == name {
			return selection, true
		}
	}
	return TargetSelectionV1{}, false
}

func recordReferenceV1(record loadedRecordV1) RecordReferenceV1 {
	return RecordReferenceV1{ID: record.ID, Digest: record.Digest}
}

func canonicalReferenceUnionV1(references []RecordReferenceV1) ([]RecordReferenceV1, error) {
	if len(references) > maxSelectedContributionsV1 {
		return nil, fmt.Errorf("selected closure contains more than %d contributions", maxSelectedContributionsV1)
	}
	byID := make(map[string]RecordReferenceV1, len(references))
	for _, reference := range references {
		if previous, exists := byID[reference.ID]; exists && previous.Digest != reference.Digest {
			return nil, fmt.Errorf("selected closure contains conflicting references for %q", reference.ID)
		}
		byID[reference.ID] = reference
	}
	result := make([]RecordReferenceV1, 0, len(byID))
	for _, reference := range byID {
		result = append(result, reference)
	}
	sort.Slice(result, func(left int, right int) bool { return result[left].ID < result[right].ID })
	return result, nil
}

func recordToolNameV1(id string) (string, error) {
	if !strings.HasPrefix(id, "tool:") {
		return "", fmt.Errorf("record ID %q is not rooted at a tool", id)
	}
	remainder := strings.TrimPrefix(id, "tool:")
	name := strings.SplitN(remainder, "/", 2)[0]
	if !validRecordIdentifierV1(name) {
		return "", fmt.Errorf("record ID %q has an invalid tool name", id)
	}
	return name, nil
}

func releaseNamespaceV1(id string) (string, error) {
	parts := strings.Split(id, "/")
	if len(parts) < 3 || parts[1] != "releases" {
		return "", fmt.Errorf("record ID %q is outside a release namespace", id)
	}
	return strings.Join(parts[:3], "/"), nil
}

func (catalog *CatalogV1) selectedMaterializationRecords(references []RecordReferenceV1) ([]PayloadRecordV1, []NativePackageSetV1) {
	payloads := []PayloadRecordV1{}
	packageSets := []NativePackageSetV1{}
	for _, reference := range references {
		switch value := catalog.records[reference.ID].Value.(type) {
		case *PayloadRecordV1:
			payloads = append(payloads, clonePayloadRecordV1(value))
		case *NativePackageSetV1:
			packageSets = append(packageSets, cloneNativePackageSetV1(value))
		}
	}
	return payloads, packageSets
}

func cloneToolRecordV1(value *ToolRecordV1) ToolRecordV1 {
	result := *value
	result.Releases = append([]RecordReferenceV1{}, value.Releases...)
	return result
}

func cloneReleaseManifestV1(value *ReleaseManifestV1) ReleaseManifestV1 {
	result := *value
	result.Aliases = append([]string{}, value.Aliases...)
	result.Targets = append([]RecordReferenceV1{}, value.Targets...)
	result.ArtifactSources = append([]ArtifactSourceMappingV1{}, value.ArtifactSources...)
	result.Provenance = append([]string{}, value.Provenance...)
	return result
}

func cloneReleaseContractV1(value *ReleaseContractV1) ReleaseContractV1 {
	result := *value
	result.Contexts = append([]string{}, value.Contexts...)
	result.Binding.Options = append([]string{}, value.Binding.Options...)
	result.Selections.Options = append([]string{}, value.Selections.Options...)
	result.Selections.Defaults = append([]string{}, value.Selections.Defaults...)
	result.Probes = cloneProbesV1(value.Probes)
	result.Exports = append([]ToolExportV1{}, value.Exports...)
	result.ResolverPrimitives = append([]string{}, value.ResolverPrimitives...)
	result.Runtime = cloneRuntimeV1(value.Runtime)
	return result
}

func cloneTargetRecordV1(value *TargetRecordV1) TargetRecordV1 {
	result := *value
	result.PackageSets = append([]RecordReferenceV1{}, value.PackageSets...)
	result.Bindings = append([]TargetBindingV1{}, value.Bindings...)
	result.Payloads = append([]RecordReferenceV1{}, value.Payloads...)
	result.Selections = cloneTargetSelectionsV1(value.Selections)
	result.Probes = cloneProbesV1(value.Probes)
	return result
}

func cloneTargetSelectionsV1(values []TargetSelectionV1) []TargetSelectionV1 {
	result := append([]TargetSelectionV1{}, values...)
	for index := range result {
		result[index].Payloads = append([]RecordReferenceV1{}, values[index].Payloads...)
	}
	return result
}

func cloneRuntimeV1(value *RecordRuntimeV1) *RecordRuntimeV1 {
	if value == nil {
		return nil
	}
	result := *value
	result.Environment = append([]RecordEnvironmentVariableV1{}, value.Environment...)
	return &result
}

func cloneProbesV1(values []RecordProbeV1) []RecordProbeV1 {
	result := append([]RecordProbeV1{}, values...)
	for index := range result {
		result[index].Args = append([]string{}, values[index].Args...)
	}
	return result
}

func cloneBindingContractV1(value *BindingContractV1) BindingContractV1 {
	result := *value
	result.Requirements = append([]string{}, value.Requirements...)
	result.SupportedPython = append([]string{}, value.SupportedPython...)
	return result
}

func cloneBindingArtifactV1(value *BindingArtifactRecordV1) BindingArtifactRecordV1 {
	result := *value
	result.Tags = append([]string{}, value.Tags...)
	result.BundledComponents = append([]BundledComponentV1{}, value.BundledComponents...)
	return result
}

func clonePayloadRecordV1(value *PayloadRecordV1) PayloadRecordV1 { return *value }

func cloneNativePackageSetV1(value *NativePackageSetV1) NativePackageSetV1 {
	result := *value
	result.Requirements = append([]string{}, value.Requirements...)
	return result
}

func cloneArtifactSourceV1(value *ArtifactSourceRecordV1) ArtifactSourceRecordV1 {
	result := *value
	result.Mirrors = append([]string{}, value.Mirrors...)
	result.Provenance = append([]string{}, value.Provenance...)
	return result
}

func cloneIntegrationFixtureV1(value *IntegrationFixtureRecordV1) IntegrationFixtureRecordV1 {
	result := *value
	result.Selections = append([]string{}, value.Selections...)
	return result
}
