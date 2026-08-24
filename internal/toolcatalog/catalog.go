package toolcatalog

import (
	"fmt"
	"io"
	"io/fs"
	"path"
	"sort"
	"strings"

	"github.com/omry/reploy/internal/canonical"
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

// recordKeyV1 is a record's exact identity. The design permits one record ID to
// appear at different digests when separate immutable release revisions select
// them, so the catalog index cannot be keyed by ID alone.
type recordKeyV1 struct {
	ID     string
	Digest canonical.Digest
}

// CatalogV1 is an immutable set of portable tool records indexed by exact
// identity, with the tool records reachable by qualified tool name.
type CatalogV1 struct {
	records map[recordKeyV1]loadedRecordV1
	tools   map[string]recordKeyV1
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
		records: make(map[recordKeyV1]loadedRecordV1),
		tools:   make(map[string]recordKeyV1),
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
	if err := catalog.validateReleaseGraphsV1(); err != nil {
		return nil, err
	}
	return catalog, nil
}

// placeRecordV1 enforces that a record lives below the tool namespace its own
// ID declares, so a record cannot be introduced under another tool's ownership.
func (catalog *CatalogV1) placeRecordV1(record loadedRecordV1, filename string, root string) error {
	relative := filename
	if trimmed := strings.TrimSuffix(root, "/"); trimmed != "" && trimmed != "." {
		relative = strings.TrimPrefix(filename, trimmed+"/")
		if relative == filename {
			return fmt.Errorf("catalog entry %q must live below %q", filename, root)
		}
	}
	toolName, err := recordToolNameV1(record.ID)
	if err != nil {
		return fmt.Errorf("catalog entry %q: %w", filename, err)
	}
	if !strings.HasPrefix(relative, toolName+"/") {
		return fmt.Errorf("catalog entry %q must live below %q", filename, toolName)
	}
	// Two files describing the exact same (id, digest) pair are a duplicate
	// definition. The same ID at a different digest is legal and belongs to a
	// different immutable release revision.
	key := recordKeyV1{ID: record.ID, Digest: record.Digest}
	if _, exists := catalog.records[key]; exists {
		return fmt.Errorf("catalog contains duplicate definition of %q at digest %s", record.ID, record.Digest)
	}
	if record.Schema == ToolRecordSchemaV1 {
		if relative != toolName+"/tool.json" {
			return fmt.Errorf("tool record %q must use path %q", record.ID, toolName+"/tool.json")
		}
		if _, exists := catalog.tools[toolName]; exists {
			return fmt.Errorf("catalog contains duplicate tool %q", toolName)
		}
		catalog.tools[toolName] = key
	}
	catalog.records[key] = record
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
	visiting := make(map[recordKeyV1]bool, len(catalog.records))
	// Memoize each settled node's longest remaining chain, not merely that it
	// was visited. Recording only visitation lets a node reached first from a
	// short prefix report zero remaining depth, so a later walk through a long
	// prefix would clear the bound it should have failed.
	suffix := make(map[recordKeyV1]int, len(catalog.records))
	var walk func(key recordKeyV1, depth int) (int, error)
	walk = func(key recordKeyV1, depth int) (int, error) {
		if depth > maxCatalogGraphDepthV1 {
			return 0, fmt.Errorf("catalog reference chain through %q exceeds depth %d", key.ID, maxCatalogGraphDepthV1)
		}
		if known, done := suffix[key]; done {
			if depth+known > maxCatalogGraphDepthV1 {
				return 0, fmt.Errorf("catalog reference chain through %q exceeds depth %d", key.ID, maxCatalogGraphDepthV1)
			}
			return known, nil
		}
		if visiting[key] {
			return 0, fmt.Errorf("catalog references form a cycle through %q", key.ID)
		}
		record, exists := catalog.records[key]
		if !exists {
			return 0, nil
		}
		visiting[key] = true
		deepest := 0
		for _, edge := range catalogReferencesV1(record.Value) {
			below, err := walk(recordKeyV1{ID: edge.Reference.ID, Digest: edge.Reference.Digest}, depth+1)
			if err != nil {
				return 0, err
			}
			if below+1 > deepest {
				deepest = below + 1
			}
		}
		visiting[key] = false
		suffix[key] = deepest
		return deepest, nil
	}
	for _, key := range catalog.sortedRecordKeysV1() {
		if _, err := walk(key, 0); err != nil {
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
		toolKey := catalog.tools[toolName]
		record := catalog.records[toolKey]
		tool, ok := record.Value.(*ToolRecordV1)
		if !ok {
			return fmt.Errorf("tool %q does not resolve to a tool record", toolName)
		}
		// The release index resolves the tool's manifest references only.
		// Traversing their closures and merging them would put two revisions'
		// records into one ID-keyed view, where differing digests of the same
		// semantic record collide even though they belong to separate release
		// graphs, which is exactly what the design permits.
		index, err := catalog.releaseIndexViewV1(toolKey, tool)
		if err != nil {
			return fmt.Errorf("tool %q: %w", toolName, err)
		}
		if err := validateToolReleaseIndexV1(tool, index); err != nil {
			return fmt.Errorf("tool %q: %w", toolName, err)
		}
		for _, reference := range tool.Releases {
			manifestKey := recordKeyV1{ID: reference.ID, Digest: reference.Digest}
			view, err := catalog.resolvedViewV1(manifestKey)
			if err != nil {
				return fmt.Errorf("tool %q: %w", toolName, err)
			}
			manifestRecord, err := resolvedRecordV1(view, reference)
			if err != nil {
				return fmt.Errorf("tool %q: %w", toolName, err)
			}
			manifest, ok := manifestRecord.Value.(*ReleaseManifestV1)
			if !ok {
				return fmt.Errorf("tool %q release %q is not a manifest", toolName, reference.ID)
			}
			if err := validateManifestResolvedGraphV1(manifest, view); err != nil {
				return fmt.Errorf("tool %q release %q: %w", toolName, manifest.ID, err)
			}
		}
	}
	return nil
}

// sortedRecordIDsV1 gives traversal a deterministic order, so a defect is
// reported identically on every run rather than depending on map iteration.
func (catalog *CatalogV1) sortedRecordKeysV1() []recordKeyV1 {
	keys := make([]recordKeyV1, 0, len(catalog.records))
	for key := range catalog.records {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(left int, right int) bool {
		if keys[left].ID != keys[right].ID {
			return keys[left].ID < keys[right].ID
		}
		return keys[left].Digest < keys[right].Digest
	})
	return keys
}

// releaseIndexViewV1 builds the shallow view the release index needs: the tool
// record and the exact manifests it names, with no transitive closure. Each
// manifest's own graph is validated separately against its own view.
func (catalog *CatalogV1) releaseIndexViewV1(toolKey recordKeyV1, tool *ToolRecordV1) (map[string]loadedRecordV1, error) {
	view := map[string]loadedRecordV1{toolKey.ID: catalog.records[toolKey]}
	for _, reference := range tool.Releases {
		key := recordKeyV1{ID: reference.ID, Digest: reference.Digest}
		record, exists := catalog.records[key]
		if !exists {
			return nil, fmt.Errorf("release %q at digest %s is not in the catalog", reference.ID, reference.Digest)
		}
		if previous, seen := view[reference.ID]; seen && previous.Digest != reference.Digest {
			return nil, fmt.Errorf("release index names %q at two digests, %s and %s",
				reference.ID, previous.Digest, reference.Digest)
		}
		view[reference.ID] = record
	}
	return view, nil
}

// resolvedViewV1 projects the exact records one manifest selects into an
// ID-keyed view, which is what the release graph walker consumes. Two digests
// for one ID inside a single resolved graph is an error, so the projection
// fails rather than choosing between them.
func (catalog *CatalogV1) resolvedViewV1(root recordKeyV1) (map[string]loadedRecordV1, error) {
	view := make(map[string]loadedRecordV1)
	var walk func(key recordKeyV1, depth int) error
	walk = func(key recordKeyV1, depth int) error {
		if depth > maxCatalogGraphDepthV1 {
			return fmt.Errorf("resolved graph through %q exceeds depth %d", key.ID, maxCatalogGraphDepthV1)
		}
		record, exists := catalog.records[key]
		if !exists {
			return nil
		}
		if previous, seen := view[key.ID]; seen {
			if previous.Digest != key.Digest {
				return fmt.Errorf("resolved graph selects %q at two digests, %s and %s",
					key.ID, previous.Digest, key.Digest)
			}
			return nil
		}
		view[key.ID] = record
		for _, edge := range catalogReferencesV1(record.Value) {
			if err := walk(recordKeyV1{ID: edge.Reference.ID, Digest: edge.Reference.Digest}, depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(root, 0); err != nil {
		return nil, err
	}
	return view, nil
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
		result = append(result, ref(record.Contract, ReleaseContractSchemaV1))
		for _, reference := range record.Targets {
			result = append(result, ref(reference, TargetRecordSchemaV1))
		}
		for _, mapping := range record.ArtifactSources {
			result = append(result,
				ref(mapping.Artifact, BindingArtifactSchemaV1, PayloadRecordSchemaV1),
				ref(mapping.Source, ArtifactSourceRecordSchemaV1))
		}
		for _, reference := range record.ValidationProfiles {
			result = append(result, ref(reference, ValidationProfileSchemaV1))
		}
	case *TargetRecordV1:
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
			for _, reference := range binding.Payloads {
				result = append(result, ref(reference, PayloadRecordSchemaV1))
			}
			for _, reference := range binding.PackageSets {
				result = append(result, ref(reference, NativePackageSetSchemaV1))
			}
			for _, reference := range binding.ValidationProfiles {
				result = append(result, ref(reference, ValidationProfileSchemaV1))
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
			for _, reference := range selection.ValidationProfiles {
				result = append(result, ref(reference, ValidationProfileSchemaV1))
			}
		}
		for _, reference := range record.ValidationProfiles {
			result = append(result, ref(reference, ValidationProfileSchemaV1))
		}
	case *BindingArtifactRecordV1:
		result = append(result, ref(record.Contract, BindingContractSchemaV1))
	case *IntegrationFixtureRecordV1:
		for _, reference := range record.ValidationProfiles {
			result = append(result, ref(reference, ValidationProfileSchemaV1))
		}
	}
	return result
}
