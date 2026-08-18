package toolcatalog

import (
	"fmt"
	"io"
	"io/fs"
	"path"
	"sort"
	"strings"
)

// Bounded hierarchical loading for portable tool catalogs.
//
// `bounded` here does not mean a ceiling on catalog size. The normative design
// defines no aggregate byte, record-count, or reference-edge ceiling, so this
// loader declares none. It means three properties that hold however large the
// catalog is:
//
//   - each record's parse is bounded by the per-unit limits before that record
//     is decoded, so one malformed file cannot exhaust the parser;
//   - reference traversal and recursion are bounded by the graph depth limit;
//   - no allocation, buffer, or traversal is ever sized by a count a record
//     declares, only by content the loader has already observed.
//
// Loading a catalog of n records therefore costs work and retention linear in
// n, which is the operator's own embedded input, rather than work a record can
// inflate.
const maxCatalogGraphDepthV1 = 64

// CatalogV1 is an immutable set of portable tool records indexed by record ID,
// with the tool records reachable by qualified tool name.
type CatalogV1 struct {
	records map[string]loadedRecordV1
	tools   map[string]string
}

// Names lists the qualified tool names this catalog defines, in canonical order.
func (catalog *CatalogV1) Names() []string {
	names := make([]string, 0, len(catalog.tools))
	for name := range catalog.tools {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// loadCatalogV1 loads every record below root in the injected filesystem.
func loadCatalogV1(files fs.FS, root string) (*CatalogV1, error) {
	catalog := &CatalogV1{
		records: make(map[string]loadedRecordV1),
		tools:   make(map[string]string),
	}
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
		payload, err := readCatalogRecordV1(files, filename)
		if err != nil {
			return err
		}
		// Per-record limits apply inside decodeRecordV1 before the record is
		// decoded, so a hostile file is rejected without the loader having to
		// know anything about the catalog as a whole.
		record, err := decodeRecordV1(filename, payload)
		if err != nil {
			return err
		}
		if _, exists := catalog.records[record.ID]; exists {
			return fmt.Errorf("catalog contains duplicate record ID %q", record.ID)
		}
		if err := catalog.placeRecordV1(record, filename, root); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(catalog.records) == 0 || len(catalog.tools) == 0 {
		return nil, fmt.Errorf("portable tool catalog is empty")
	}
	if err := catalog.verifyReferenceDepthV1(); err != nil {
		return nil, err
	}
	if err := catalog.validateCatalogGraphV1(); err != nil {
		return nil, err
	}
	if err := catalog.validateReleaseGraphsV1(); err != nil {
		return nil, err
	}
	return catalog, nil
}

// placeRecordV1 enforces that a record lives below the tool namespace its own
// ID declares, so a record cannot be introduced under another tool's ownership.
func (catalog *CatalogV1) placeRecordV1(record loadedRecordV1, filename string, root string) error {
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
}

// readCatalogRecordV1 reads one record file. The read is bounded by the same
// per-record byte limit decoding applies, so a single oversized file fails here
// rather than after the whole file is resident.
func readCatalogRecordV1(files fs.FS, filename string) ([]byte, error) {
	file, err := files.Open(filename)
	if err != nil {
		return nil, fmt.Errorf("catalog entry %q: %w", filename, err)
	}
	defer file.Close()
	payload, err := io.ReadAll(io.LimitReader(file, maxDefinitionFileBytes+1))
	if err != nil {
		return nil, fmt.Errorf("catalog entry %q: %w", filename, err)
	}
	if len(payload) > maxDefinitionFileBytes {
		return nil, fmt.Errorf("catalog entry %q exceeds %d bytes", filename, maxDefinitionFileBytes)
	}
	return payload, nil
}

// recordToolNameV1 extracts the qualified tool name a record ID declares.
func recordToolNameV1(id string) (string, error) {
	segments := strings.Split(id, "/")
	name, found := strings.CutPrefix(segments[0], "tool:")
	if !found || !validRecordIdentifierV1(name) {
		return "", fmt.Errorf("record ID %q does not declare a qualified tool name", id)
	}
	return name, nil
}

// verifyReferenceDepthV1 bounds recursion rather than catalog size: it walks
// every record's references and fails when a chain exceeds the graph depth
// limit, which is what stops a cyclic or pathologically deep definition from
// exhausting the stack. It never allocates from a declared count.
func (catalog *CatalogV1) verifyReferenceDepthV1() error {
	visiting := make(map[string]bool, len(catalog.records))
	settled := make(map[string]struct{}, len(catalog.records))
	var walk func(id string, depth int) error
	walk = func(id string, depth int) error {
		if depth > maxCatalogGraphDepthV1 {
			return fmt.Errorf("catalog reference chain through %q exceeds depth %d", id, maxCatalogGraphDepthV1)
		}
		if _, done := settled[id]; done {
			return nil
		}
		if visiting[id] {
			return fmt.Errorf("catalog references form a cycle through %q", id)
		}
		record, exists := catalog.records[id]
		if !exists {
			return nil
		}
		visiting[id] = true
		for _, edge := range catalogReferencesV1(record.Value) {
			if err := walk(edge.Reference.ID, depth+1); err != nil {
				return err
			}
		}
		visiting[id] = false
		settled[id] = struct{}{}
		return nil
	}
	for _, id := range catalog.sortedRecordIDsV1() {
		if err := walk(id, 0); err != nil {
			return err
		}
	}
	return nil
}

// validateReleaseGraphsV1 gives the release graph walker a production caller:
// every tool record's release index is validated, and every manifest it names
// has its resolved graph validated against the records actually loaded.
func (catalog *CatalogV1) validateReleaseGraphsV1() error {
	for _, toolName := range catalog.Names() {
		record := catalog.records[catalog.tools[toolName]]
		tool, ok := record.Value.(*ToolRecordV1)
		if !ok {
			return fmt.Errorf("tool %q does not resolve to a tool record", toolName)
		}
		if err := validateToolReleaseIndexV1(tool, catalog.records); err != nil {
			return fmt.Errorf("tool %q: %w", toolName, err)
		}
		for _, reference := range tool.Releases {
			manifestRecord, err := resolvedRecordV1(catalog.records, reference)
			if err != nil {
				return fmt.Errorf("tool %q: %w", toolName, err)
			}
			manifest, ok := manifestRecord.Value.(*ReleaseManifestV1)
			if !ok {
				return fmt.Errorf("tool %q release %q is not a manifest", toolName, reference.ID)
			}
			if err := validateManifestResolvedGraphV1(manifest, catalog.records); err != nil {
				return fmt.Errorf("tool %q release %q: %w", toolName, manifest.ID, err)
			}
		}
	}
	return nil
}

// sortedRecordIDsV1 gives traversal a deterministic order, so a defect is
// reported identically on every run rather than depending on map iteration.
func (catalog *CatalogV1) sortedRecordIDsV1() []string {
	ids := make([]string, 0, len(catalog.records))
	for id := range catalog.records {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// catalogReferenceV1 is one outgoing reference and the record schemas that may
// legitimately satisfy it.
type catalogReferenceV1 struct {
	Reference RecordReferenceV1
	Schemas   []string
}

// catalogReferencesV1 enumerates every outgoing reference a record declares.
//
// The parked source enumerated a singular integration fixture and a singular
// binding artifact per binding. The accepted model is plural on both, and adds
// binding- and selection-scoped package sets, so enumerating the parked shape
// would silently omit edges from traversal and from any check built on it.
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
		result = append(result,
			ref(record.Contract, ReleaseContractSchemaV1),
			ref(record.ValidationProfile, ValidationProfileSchemaV1))
		for _, reference := range record.Targets {
			result = append(result, ref(reference, TargetRecordSchemaV1))
		}
		for _, mapping := range record.ArtifactSources {
			result = append(result,
				ref(mapping.Artifact, BindingArtifactSchemaV1, PayloadRecordSchemaV1),
				ref(mapping.Source, ArtifactSourceRecordSchemaV1))
		}
	case *TargetRecordV1:
		result = append(result, ref(record.ValidationProfile, ValidationProfileSchemaV1))
		for _, reference := range record.IntegrationFixtures {
			result = append(result, ref(reference, IntegrationFixtureSchemaV1))
		}
		for _, reference := range record.PackageSets {
			result = append(result, ref(reference, NativePackageSetSchemaV1))
		}
		for _, binding := range record.Bindings {
			result = append(result, ref(binding.Contract, BindingContractSchemaV1))
			for _, reference := range binding.Artifacts {
				result = append(result, ref(reference, BindingArtifactSchemaV1))
			}
			for _, reference := range binding.PackageSets {
				result = append(result, ref(reference, NativePackageSetSchemaV1))
			}
		}
		for _, reference := range record.Payloads {
			result = append(result, ref(reference, PayloadRecordSchemaV1))
		}
		for _, selection := range record.Selections {
			for _, reference := range selection.Payloads {
				result = append(result, ref(reference, PayloadRecordSchemaV1))
			}
			for _, reference := range selection.PackageSets {
				result = append(result, ref(reference, NativePackageSetSchemaV1))
			}
		}
	case *BindingArtifactRecordV1:
		result = append(result, ref(record.Contract, BindingContractSchemaV1))
	}
	return result
}
