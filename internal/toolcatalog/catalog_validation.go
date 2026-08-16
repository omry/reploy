package toolcatalog

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/omry/reploy/internal/canonical"
)

type catalogReferenceV1 struct {
	Reference RecordReferenceV1
	Schemas   []string
}

func (catalog *CatalogV1) validate() error {
	for _, id := range catalog.sortedRecordIDs() {
		record := catalog.records[id]
		if err := catalog.validateRecordReferences(record); err != nil {
			return err
		}
	}
	if err := catalog.validateAcyclicAndReachable(); err != nil {
		return err
	}
	for _, name := range catalog.Names() {
		toolID := catalog.tools[name]
		tool := catalog.records[toolID].Value.(*ToolRecordV1)
		for _, reference := range tool.Releases {
			manifestRecord := catalog.records[reference.ID]
			if err := catalog.validateManifest(manifestRecord); err != nil {
				return err
			}
		}
	}
	return nil
}

func (catalog *CatalogV1) sortedRecordIDs() []string {
	ids := make([]string, 0, len(catalog.records))
	for id := range catalog.records {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func (catalog *CatalogV1) validateRecordReferences(record loadedRecordV1) error {
	ownerTool, _ := recordToolNameV1(record.ID)
	ownerRelease, releaseErr := releaseNamespaceV1(record.ID)
	for _, edge := range catalogReferencesV1(record.Value) {
		target, exists := catalog.records[edge.Reference.ID]
		if !exists {
			return fmt.Errorf("record %q references missing record %q", record.ID, edge.Reference.ID)
		}
		if target.Digest != edge.Reference.Digest {
			return fmt.Errorf("record %q reference %q has digest %s, want %s", record.ID, edge.Reference.ID, edge.Reference.Digest, target.Digest)
		}
		if !containsRecordValueV1(edge.Schemas, target.Schema) {
			return fmt.Errorf("record %q reference %q has schema %q", record.ID, edge.Reference.ID, target.Schema)
		}
		targetTool, _ := recordToolNameV1(target.ID)
		if targetTool != ownerTool {
			return fmt.Errorf("record %q reference %q crosses tool namespaces", record.ID, target.ID)
		}
		if record.Schema != ToolRecordSchemaV1 {
			targetRelease, err := releaseNamespaceV1(target.ID)
			if releaseErr != nil || err != nil || targetRelease != ownerRelease {
				return fmt.Errorf("record %q reference %q escapes release namespace %q", record.ID, target.ID, ownerRelease)
			}
		}
	}
	if record.Schema != ToolRecordSchemaV1 && releaseErr != nil {
		return releaseErr
	}
	return nil
}

func catalogReferencesV1(value any) []catalogReferenceV1 {
	ref := func(reference RecordReferenceV1, schemas ...string) catalogReferenceV1 {
		return catalogReferenceV1{Reference: reference, Schemas: schemas}
	}
	result := []catalogReferenceV1{}
	switch record := value.(type) {
	case *ToolRecordV1:
		for _, reference := range record.Releases {
			result = append(result, ref(reference, ReleaseManifestSchemaV1))
		}
	case *ReleaseManifestV1:
		result = append(result, ref(record.Contract, ReleaseContractSchemaV1))
		result = append(result, ref(record.ValidationProfile, ValidationProfileSchemaV1))
		for _, reference := range record.Targets {
			result = append(result, ref(reference, TargetRecordSchemaV1))
		}
		for _, mapping := range record.ArtifactSources {
			result = append(result,
				ref(mapping.Artifact, BindingArtifactSchemaV1, PayloadRecordSchemaV1),
				ref(mapping.Source, ArtifactSourceRecordSchemaV1),
			)
		}
	case *TargetRecordV1:
		result = append(result,
			ref(record.IntegrationFixture, IntegrationFixtureSchemaV1),
			ref(record.ValidationProfile, ValidationProfileSchemaV1),
		)
		for _, reference := range record.PackageSets {
			result = append(result, ref(reference, NativePackageSetSchemaV1))
		}
		for _, binding := range record.Bindings {
			result = append(result,
				ref(binding.Contract, BindingContractSchemaV1),
				ref(binding.Artifact, BindingArtifactSchemaV1),
			)
		}
		for _, reference := range record.Payloads {
			result = append(result, ref(reference, PayloadRecordSchemaV1))
		}
		for _, selection := range record.Selections {
			for _, reference := range selection.Payloads {
				result = append(result, ref(reference, PayloadRecordSchemaV1))
			}
		}
	}
	return result
}

func (catalog *CatalogV1) validateAcyclicAndReachable() error {
	state := make(map[string]uint8, len(catalog.records))
	reachable := make(map[string]bool, len(catalog.records))
	var visit func(string, int) error
	visit = func(id string, depth int) error {
		if depth > maxCatalogGraphDepthV1 {
			return fmt.Errorf("record graph exceeds maximum depth")
		}
		if state[id] == 1 {
			return fmt.Errorf("record graph contains a cycle at %q", id)
		}
		if state[id] == 2 {
			reachable[id] = true
			return nil
		}
		state[id] = 1
		reachable[id] = true
		for _, edge := range catalogReferencesV1(catalog.records[id].Value) {
			if err := visit(edge.Reference.ID, depth+1); err != nil {
				return err
			}
		}
		state[id] = 2
		return nil
	}
	for _, name := range catalog.Names() {
		toolID := catalog.tools[name]
		if err := visit(toolID, 0); err != nil {
			return err
		}
	}
	for _, id := range catalog.sortedRecordIDs() {
		if !reachable[id] {
			return fmt.Errorf("catalog record %q is unreachable from a tool record", id)
		}
	}
	return nil
}

func (catalog *CatalogV1) validateManifest(record loadedRecordV1) error {
	manifest := record.Value.(*ReleaseManifestV1)
	releasePrefix, _ := releaseNamespaceV1(manifest.ID)
	if !strings.HasPrefix(manifest.ID, releasePrefix+"/revisions/"+manifest.Revision+"/") {
		return fmt.Errorf("release manifest %q is outside its revision namespace", manifest.ID)
	}
	contract := catalog.records[manifest.Contract.ID].Value.(*ReleaseContractV1)
	profile := catalog.records[manifest.ValidationProfile.ID].Value.(*ValidationProfileRecordV1)
	if profile.Tool != manifest.Tool || profile.Version != manifest.Version {
		return fmt.Errorf("release manifest %q validation profile does not match its release", manifest.ID)
	}
	targetTuples := map[TargetIdentityV1]string{}
	reachableArtifacts := map[string]map[string]loadedRecordV1{}
	addReachableArtifact := func(artifact loadedRecordV1) {
		key := string(artifactDigestV1(artifact.Value))
		if reachableArtifacts[key] == nil {
			reachableArtifacts[key] = map[string]loadedRecordV1{}
		}
		reachableArtifacts[key][artifact.ID] = artifact
	}
	for _, targetReference := range manifest.Targets {
		targetRecord := catalog.records[targetReference.ID]
		target := targetRecord.Value.(*TargetRecordV1)
		if target.ValidationProfile != manifest.ValidationProfile {
			return fmt.Errorf("release manifest %q target %q uses a different validation profile", manifest.ID, target.ID)
		}
		if previous, exists := targetTuples[target.Target]; exists {
			return fmt.Errorf("release manifest %q has ambiguous targets %q and %q", manifest.ID, previous, target.ID)
		}
		targetTuples[target.Target] = target.ID
		if err := catalog.validateTargetAvailability(contract, target); err != nil {
			return fmt.Errorf("release manifest %q target %q: %w", manifest.ID, target.ID, err)
		}
		for _, binding := range target.Bindings {
			addReachableArtifact(catalog.records[binding.Artifact.ID])
		}
		for _, reference := range target.Payloads {
			addReachableArtifact(catalog.records[reference.ID])
		}
		for _, selection := range target.Selections {
			for _, reference := range selection.Payloads {
				addReachableArtifact(catalog.records[reference.ID])
			}
		}
	}
	mappings := make(map[string]ArtifactSourceMappingV1, len(manifest.ArtifactSources))
	for _, mapping := range manifest.ArtifactSources {
		key := string(mapping.ArtifactSHA256)
		if _, exists := mappings[key]; exists {
			return fmt.Errorf("release manifest %q maps artifact digest %s more than once", manifest.ID, mapping.ArtifactSHA256)
		}
		artifact := catalog.records[mapping.Artifact.ID]
		if artifactDigestV1(artifact.Value) != mapping.ArtifactSHA256 {
			return fmt.Errorf("release manifest %q artifact mapping %s disagrees with artifact record", manifest.ID, mapping.ArtifactSHA256)
		}
		source := catalog.records[mapping.Source.ID].Value.(*ArtifactSourceRecordV1)
		if source.SHA256 != mapping.ArtifactSHA256 || !containsRecordValueV1(contract.ResolverPrimitives, source.Resolver) {
			return fmt.Errorf("release manifest %q artifact source %q disagrees with artifact identity or resolver contract", manifest.ID, source.ID)
		}
		sourcePrefix := releasePrefix + "/revisions/" + manifest.Revision + "/"
		if !strings.HasPrefix(source.ID, sourcePrefix) {
			return fmt.Errorf("release manifest %q source %q is outside its revision namespace", manifest.ID, source.ID)
		}
		group, reachable := reachableArtifacts[key]
		if !reachable {
			return fmt.Errorf("release manifest %q contains orphaned artifact source mapping %s", manifest.ID, mapping.ArtifactSHA256)
		}
		if _, belongs := group[artifact.ID]; !belongs {
			return fmt.Errorf("release manifest %q source mapping %s points at an artifact outside the selected target graph", manifest.ID, mapping.ArtifactSHA256)
		}
		mappings[key] = mapping
	}
	digests := make([]string, 0, len(reachableArtifacts))
	for digest := range reachableArtifacts {
		digests = append(digests, digest)
	}
	sort.Strings(digests)
	for _, digest := range digests {
		mapping, exists := mappings[digest]
		group := reachableArtifacts[digest]
		artifactIDs := make([]string, 0, len(group))
		for artifactID := range group {
			artifactIDs = append(artifactIDs, artifactID)
		}
		sort.Strings(artifactIDs)
		if !exists {
			return fmt.Errorf("release manifest %q has no source mapping for artifact digest %s used by %q", manifest.ID, digest, artifactIDs[0])
		}
		source := catalog.records[mapping.Source.ID].Value.(*ArtifactSourceRecordV1)
		for _, artifactID := range artifactIDs {
			artifact := group[artifactID]
			if source.Size != artifactSizeV1(artifact.Value) {
				return fmt.Errorf("release manifest %q artifact source %q size disagrees with artifact %q", manifest.ID, source.ID, artifact.ID)
			}
		}
	}
	return nil
}

func (catalog *CatalogV1) validateTargetAvailability(contract *ReleaseContractV1, target *TargetRecordV1) error {
	if target.Target.PackageManager != "apt" {
		return fmt.Errorf("unsupported package manager %q", target.Target.PackageManager)
	}
	fixture := catalog.records[target.IntegrationFixture.ID].Value.(*IntegrationFixtureRecordV1)
	if fixture.Target != target.Target || !containsRecordValueV1(contract.Contexts, fixture.Context) {
		return fmt.Errorf("integration fixture does not match the target or release context")
	}
	availableBindings := map[string]bool{}
	for _, binding := range target.Bindings {
		if !containsRecordValueV1(contract.Binding.Options, binding.Name) {
			return fmt.Errorf("binding %q is not declared by the release contract", binding.Name)
		}
		bindingContract := catalog.records[binding.Contract.ID].Value.(*BindingContractV1)
		bindingArtifact := catalog.records[binding.Artifact.ID].Value.(*BindingArtifactRecordV1)
		if bindingContract.Name != binding.Name || bindingArtifact.Binding != binding.Name || bindingArtifact.Platform != target.Target.Platform {
			return fmt.Errorf("binding %q records are incompatible with the target", binding.Name)
		}
		availableBindings[binding.Name] = true
	}
	if (contract.Binding.Required && len(availableBindings) == 0) || (contract.Binding.Default != "" && !availableBindings[contract.Binding.Default]) {
		return fmt.Errorf("target does not satisfy the release binding contract")
	}
	availableSelections := map[string]bool{}
	for _, selection := range target.Selections {
		if !containsRecordValueV1(contract.Selections.Options, selection.Name) {
			return fmt.Errorf("selection %q is not declared by the release contract", selection.Name)
		}
		for _, reference := range selection.Payloads {
			payload := catalog.records[reference.ID].Value.(*PayloadRecordV1)
			if payload.Selection != selection.Name || payload.Platform != target.Target.Platform {
				return fmt.Errorf("selection %q payload %q is incompatible with the target", selection.Name, payload.ID)
			}
		}
		availableSelections[selection.Name] = true
	}
	minimum, _ := strconv.ParseUint(contract.Selections.Minimum, 10, 63)
	if uint64(len(availableSelections)) < minimum {
		return fmt.Errorf("target does not provide the minimum selection count")
	}
	for _, selection := range contract.Selections.Defaults {
		if !availableSelections[selection] {
			return fmt.Errorf("target does not provide default selection %q", selection)
		}
	}
	if _, err := resolveBindingV1(contract.Binding, fixture.Binding); err != nil {
		return fmt.Errorf("integration fixture binding: %w", err)
	}
	fixtureSelections, err := resolveSelectionsV1(contract.Selections, fixture.Selections)
	if err != nil {
		return fmt.Errorf("integration fixture selections: %w", err)
	}
	if fixture.Binding != "" {
		if _, found := targetBindingV1(target.Bindings, fixture.Binding); !found {
			return fmt.Errorf("integration fixture binding %q is unavailable on the target", fixture.Binding)
		}
	}
	for _, selection := range fixtureSelections {
		if _, found := targetSelectionV1(target.Selections, selection); !found {
			return fmt.Errorf("integration fixture selection %q is unavailable on the target", selection)
		}
	}
	for _, reference := range target.PackageSets {
		packageSet := catalog.records[reference.ID].Value.(*NativePackageSetV1)
		if packageSet.Manager != target.Target.PackageManager {
			return fmt.Errorf("package set %q uses manager %q", packageSet.ID, packageSet.Manager)
		}
	}
	for _, reference := range target.Payloads {
		payload := catalog.records[reference.ID].Value.(*PayloadRecordV1)
		if payload.Selection != "" || payload.Platform != target.Target.Platform {
			return fmt.Errorf("target payload %q is not an unconditional compatible payload", payload.ID)
		}
	}
	return nil
}

func artifactDigestV1(value any) canonical.Digest {
	switch artifact := value.(type) {
	case *BindingArtifactRecordV1:
		return artifact.SHA256
	case *PayloadRecordV1:
		return artifact.SHA256
	default:
		return ""
	}
}

func artifactSizeV1(value any) string {
	switch artifact := value.(type) {
	case *BindingArtifactRecordV1:
		return artifact.Size
	case *PayloadRecordV1:
		return artifact.Size
	default:
		return ""
	}
}

func (catalog *CatalogV1) selectedSources(manifest *ReleaseManifestV1, references []RecordReferenceV1) ([]ArtifactSourceRecordV1, error) {
	wantedDigests := map[string]bool{}
	for _, reference := range references {
		if digest := artifactDigestV1(catalog.records[reference.ID].Value); digest != "" {
			wantedDigests[string(digest)] = true
		}
	}
	sources := []ArtifactSourceRecordV1{}
	for _, mapping := range manifest.ArtifactSources {
		if !wantedDigests[string(mapping.ArtifactSHA256)] {
			continue
		}
		source := cloneArtifactSourceV1(catalog.records[mapping.Source.ID].Value.(*ArtifactSourceRecordV1))
		sources = append(sources, source)
		delete(wantedDigests, string(mapping.ArtifactSHA256))
	}
	if len(wantedDigests) != 0 {
		return nil, fmt.Errorf("selected closure has unmapped artifacts")
	}
	sort.Slice(sources, func(left int, right int) bool { return sources[left].ID < sources[right].ID })
	return sources, nil
}

func (catalog *CatalogV1) validateSelectedContributions(references []RecordReferenceV1) error {
	logicalPaths := map[string]string{}
	installDirectories := map[string]string{}
	packageRequirements := map[string]string{}
	for _, reference := range references {
		switch value := catalog.records[reference.ID].Value.(type) {
		case *PayloadRecordV1:
			if previous, exists := logicalPaths[value.LogicalPath]; exists && previous != value.ID {
				return fmt.Errorf("selected closure has conflicting artifact logical path %q", value.LogicalPath)
			}
			logicalPaths[value.LogicalPath] = value.ID
			for directory, owner := range installDirectories {
				if overlappingRecordPathsV1(directory, value.InstallDirectory) && owner != value.ID {
					return fmt.Errorf("selected closure payload directories %q and %q overlap", directory, value.InstallDirectory)
				}
			}
			installDirectories[value.InstallDirectory] = value.ID
		case *NativePackageSetV1:
			for _, requirement := range value.Requirements {
				name := packageRequirementNameV1(requirement)
				if previous, exists := packageRequirements[name]; exists && previous != requirement {
					return fmt.Errorf("selected closure has incompatible package requirements %q and %q", previous, requirement)
				}
				packageRequirements[name] = requirement
			}
		}
	}
	return nil
}

func packageRequirementNameV1(requirement string) string {
	for index, character := range requirement {
		if character == '=' || character == '<' || character == '>' || character == '!' || character == '~' {
			return requirement[:index]
		}
	}
	return requirement
}

func overlappingRecordPathsV1(left string, right string) bool {
	return left == right || strings.HasPrefix(left, right+"/") || strings.HasPrefix(right, left+"/")
}
