package toolcatalog

import (
	"fmt"
	"path"
	"sort"

	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/portabletool"
)

// PortableToolRecordV1 is retained as a compatibility alias for the shared
// canonical portable-tool record contract.
type PortableToolRecordV1 = portabletool.RecordV1

// PortableToolDefinitionRecordV1 places one typed authoring record at its
// intended catalog path. References must name an exact record ID. A missing
// digest is resolved by the composer when that ID is unambiguous; a supplied
// digest is always checked and is required when multiple revisions define the
// same ID.
type PortableToolDefinitionRecordV1 struct {
	Path   string
	Record PortableToolRecordV1
}

// CanonicalCatalogRecordV1 is one validated record ready for embedding.
// CanonicalBytes contains the exact bytes over which Digest was computed.
type CanonicalCatalogRecordV1 struct {
	Path           string
	ID             string
	Digest         canonical.Digest
	CanonicalBytes []byte
}

type definitionComposerNodeV1 struct {
	path     string
	id       string
	schema   string
	value    any
	digest   canonical.Digest
	payload  []byte
	visiting bool
	done     bool
}

type mutableCatalogReferenceV1 struct {
	reference *RecordReferenceV1
	schemas   []string
}

// ComposePortableToolCatalogV1 resolves authoring references, validates the
// closed graph, and emits canonical JSON records in path order. It clones all
// input records and never performs acquisition, execution, inheritance, or
// overlay processing.
func ComposePortableToolCatalogV1(definitions []PortableToolDefinitionRecordV1) ([]CanonicalCatalogRecordV1, error) {
	if len(definitions) == 0 {
		return nil, fmt.Errorf("portable tool definition must contain records")
	}

	nodes := make([]*definitionComposerNodeV1, 0, len(definitions))
	byPath := make(map[string]*definitionComposerNodeV1, len(definitions))
	byID := make(map[string][]*definitionComposerNodeV1, len(definitions))
	for index, definition := range definitions {
		if err := validateRecordPathV1(definition.Path, false); err != nil {
			return nil, fmt.Errorf("definition record %d path: %w", index, err)
		}
		if path.Ext(definition.Path) != ".json" {
			return nil, fmt.Errorf("definition record %d path %q must be a JSON file", index, definition.Path)
		}
		if _, exists := byPath[definition.Path]; exists {
			return nil, fmt.Errorf("definition contains conflicting path %q", definition.Path)
		}
		id, schema, value, err := clonePortableToolRecordV1(definition.Record)
		if err != nil {
			return nil, fmt.Errorf("definition record %d: %w", index, err)
		}
		node := &definitionComposerNodeV1{path: definition.Path, id: id, schema: schema, value: value}
		nodes = append(nodes, node)
		byPath[node.path] = node
		byID[node.id] = append(byID[node.id], node)
	}
	sort.Slice(nodes, func(left, right int) bool { return nodes[left].path < nodes[right].path })
	for _, candidates := range byID {
		sort.Slice(candidates, func(left, right int) bool { return candidates[left].path < candidates[right].path })
	}

	var compose func(*definitionComposerNodeV1, int) error
	compose = func(node *definitionComposerNodeV1, depth int) error {
		if depth > maxCatalogGraphDepthV1 {
			return fmt.Errorf("definition reference graph exceeds depth %d", maxCatalogGraphDepthV1)
		}

		if node.done {
			return nil
		}
		if node.visiting {
			return fmt.Errorf("definition references form a cycle through %q", node.id)
		}
		node.visiting = true
		defer func() { node.visiting = false }()

		for _, edge := range mutableCatalogReferencesV1(node.value) {
			reference := edge.reference
			if reference.ID == "" {
				return fmt.Errorf("record %q contains a reference without an ID", node.id)
			}
			if reference.Digest != "" {
				if err := reference.Digest.Validate(); err != nil {
					return fmt.Errorf("record %q reference %q: %w", node.id, reference.ID, err)
				}
			}
			candidates := compatibleDefinitionNodesV1(byID[reference.ID], edge.schemas)
			if len(candidates) == 0 {
				if len(byID[reference.ID]) > 0 {
					return fmt.Errorf("record %q reference %q has an incompatible record schema", node.id, reference.ID)
				}
				return fmt.Errorf("record %q references missing record %q", node.id, reference.ID)
			}
			if len(candidates) > 1 && reference.Digest == "" {
				return fmt.Errorf("record %q reference %q is ambiguous without an exact digest", node.id, reference.ID)
			}

			var matched *definitionComposerNodeV1
			for _, candidate := range candidates {
				if err := compose(candidate, depth+1); err != nil {
					return err
				}
				if reference.Digest == "" || candidate.digest == reference.Digest {
					if matched != nil {
						return fmt.Errorf("record %q reference %q matches conflicting definitions", node.id, reference.ID)
					}
					matched = candidate
				}
			}
			if matched == nil {
				return fmt.Errorf("record %q reference %q digest %s does not match any definition", node.id, reference.ID, reference.Digest)
			}
			reference.Digest = matched.digest
		}

		loaded := loadedRecordV1{ID: node.id, Schema: node.schema, Path: node.path, Value: node.value}
		if err := validateLoadedRecordV1(loaded); err != nil {
			return fmt.Errorf("validate definition record %q: %w", node.id, err)
		}
		payload, err := canonical.Marshal(node.value)
		if err != nil {
			return fmt.Errorf("canonicalize definition record %q: %w", node.id, err)
		}
		digest, err := canonical.Sum("portable-tool-record", portableToolRecordIdentityV1, node.value)
		if err != nil {
			return fmt.Errorf("digest definition record %q: %w", node.id, err)
		}
		node.payload = payload
		node.digest = digest
		node.done = true
		return nil
	}

	for _, node := range nodes {
		if err := compose(node, 0); err != nil {
			return nil, err
		}
	}

	catalog := &CatalogV1{
		records: make(map[recordKeyV1]loadedRecordV1, len(nodes)),
		tools:   make(map[string]recordKeyV1),
	}
	for _, node := range nodes {
		record, err := decodeRecordV1(node.path, node.payload)
		if err != nil {
			return nil, fmt.Errorf("decode composed portable tool catalog: %w", err)
		}
		if err := catalog.placeRecordV1(record, node.path, "."); err != nil {
			return nil, fmt.Errorf("place composed portable tool catalog: %w", err)
		}
	}
	if err := catalog.validateLoadedCatalogV1(); err != nil {
		return nil, fmt.Errorf("validate composed portable tool catalog: %w", err)
	}

	result := make([]CanonicalCatalogRecordV1, 0, len(nodes))
	for _, node := range nodes {
		result = append(result, CanonicalCatalogRecordV1{
			Path:           node.path,
			ID:             node.id,
			Digest:         node.digest,
			CanonicalBytes: append([]byte(nil), node.payload...),
		})
	}
	return result, nil
}

func compatibleDefinitionNodesV1(nodes []*definitionComposerNodeV1, schemas []string) []*definitionComposerNodeV1 {
	result := make([]*definitionComposerNodeV1, 0, len(nodes))
	for _, node := range nodes {
		for _, schema := range schemas {
			if node.schema == schema {
				result = append(result, node)
				break
			}
		}
	}
	return result
}

func clonePortableToolRecordV1(record PortableToolRecordV1) (string, string, any, error) {
	switch value := record.(type) {
	case *ToolRecordV1:
		if value == nil {
			break
		}
		clone := cloneToolRecordV1(value)
		return clone.ID, clone.Schema, &clone, nil
	case *ReleaseManifestV1:
		if value == nil {
			break
		}
		clone := cloneReleaseManifestV1(value)
		return clone.ID, clone.Schema, &clone, nil
	case *ReleaseContractV1:
		if value == nil {
			break
		}
		clone := cloneReleaseContractV1(value)
		return clone.ID, clone.Schema, &clone, nil
	case *TargetRecordV1:
		if value == nil {
			break
		}
		clone := cloneTargetRecordV1(value)
		return clone.ID, clone.Schema, &clone, nil
	case *BindingContractV1:
		if value == nil {
			break
		}
		clone := cloneBindingContractV1(value)
		return clone.ID, clone.Schema, &clone, nil
	case *BindingArtifactRecordV1:
		if value == nil {
			break
		}
		clone := cloneBindingArtifactV1(value)
		return clone.ID, clone.Schema, &clone, nil
	case *PayloadRecordV1:
		if value == nil {
			break
		}
		clone := clonePayloadRecordV1(value)
		return clone.ID, clone.Schema, &clone, nil
	case *ArtifactSourceRecordV1:
		if value == nil {
			break
		}
		clone := cloneArtifactSourceV1(value)
		return clone.ID, clone.Schema, &clone, nil
	case *NativePackageSetV1:
		if value == nil {
			break
		}
		clone := cloneNativePackageSetV1(value)
		return clone.ID, clone.Schema, &clone, nil
	case *IntegrationFixtureRecordV1:
		if value == nil {
			break
		}
		clone := cloneIntegrationFixtureV1(value)
		return clone.ID, clone.Schema, &clone, nil
	case *ValidationProfileRecordV1:
		if value == nil {
			break
		}
		clone := cloneValidationProfileV1(value)
		return clone.ID, clone.Schema, &clone, nil
	}
	return "", "", nil, fmt.Errorf("portable tool record must not be nil")
}

func mutableCatalogReferencesV1(value any) []mutableCatalogReferenceV1 {
	ref := func(reference *RecordReferenceV1, schemas ...string) mutableCatalogReferenceV1 {
		return mutableCatalogReferenceV1{reference: reference, schemas: schemas}
	}
	result := []mutableCatalogReferenceV1{}
	switch record := value.(type) {
	case *ToolRecordV1:
		for index := range record.Releases {
			result = append(result, ref(&record.Releases[index], ReleaseManifestSchemaV1))
		}
	case *ReleaseManifestV1:
		result = append(result, ref(&record.Contract, ReleaseContractSchemaV1))
		for index := range record.Targets {
			result = append(result, ref(&record.Targets[index], TargetRecordSchemaV1))
		}
		for index := range record.ArtifactSources {
			result = append(result,
				ref(&record.ArtifactSources[index].Artifact, BindingArtifactSchemaV1, PayloadRecordSchemaV1),
				ref(&record.ArtifactSources[index].Source, ArtifactSourceRecordSchemaV1),
			)
		}
		for index := range record.ValidationProfiles {
			result = append(result, ref(&record.ValidationProfiles[index], ValidationProfileSchemaV1))
		}
	case *TargetRecordV1:
		for index := range record.IntegrationFixtures {
			result = append(result, ref(&record.IntegrationFixtures[index], IntegrationFixtureSchemaV1))
		}
		for index := range record.PackageSets {
			result = append(result, ref(&record.PackageSets[index], NativePackageSetSchemaV1))
		}
		for bindingIndex := range record.Bindings {
			binding := &record.Bindings[bindingIndex]
			result = append(result, ref(&binding.Contract, BindingContractSchemaV1))
			for index := range binding.Artifacts {
				result = append(result, ref(&binding.Artifacts[index], BindingArtifactSchemaV1))
			}
			for index := range binding.Payloads {
				result = append(result, ref(&binding.Payloads[index], PayloadRecordSchemaV1))
			}
			for index := range binding.PackageSets {
				result = append(result, ref(&binding.PackageSets[index], NativePackageSetSchemaV1))
			}
			for index := range binding.ValidationProfiles {
				result = append(result, ref(&binding.ValidationProfiles[index], ValidationProfileSchemaV1))
			}
		}
		for index := range record.Payloads {
			result = append(result, ref(&record.Payloads[index], PayloadRecordSchemaV1))
		}
		for selectionIndex := range record.Selections {
			selection := &record.Selections[selectionIndex]
			for index := range selection.Payloads {
				result = append(result, ref(&selection.Payloads[index], PayloadRecordSchemaV1))
			}
			for index := range selection.PackageSets {
				result = append(result, ref(&selection.PackageSets[index], NativePackageSetSchemaV1))
			}
			for index := range selection.ValidationProfiles {
				result = append(result, ref(&selection.ValidationProfiles[index], ValidationProfileSchemaV1))
			}
		}
		for index := range record.ValidationProfiles {
			result = append(result, ref(&record.ValidationProfiles[index], ValidationProfileSchemaV1))
		}
	case *BindingArtifactRecordV1:
		result = append(result, ref(&record.Contract, BindingContractSchemaV1))
	case *IntegrationFixtureRecordV1:
		for index := range record.ValidationProfiles {
			result = append(result, ref(&record.ValidationProfiles[index], ValidationProfileSchemaV1))
		}
	}
	return result
}
