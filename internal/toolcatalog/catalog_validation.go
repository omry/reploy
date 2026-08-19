package toolcatalog

import (
	"fmt"
	"strings"

	"github.com/omry/reploy/internal/canonical"
)

// Catalog-wide graph validation. Record-local validation proves one record is
// well formed, and the release graph walker proves one manifest resolves. This
// file proves the properties that only exist across the whole catalog: every
// reference resolves to a record of the right schema at the right digest inside
// the right namespace, every record is reachable from a tool record, and every
// reachable artifact has exactly one consistent acquisition source.

// releaseNamespaceV1 extracts the release namespace a record ID belongs to.
func releaseNamespaceV1(id string) (string, error) {
	segments := strings.Split(id, "/")
	if len(segments) < 3 || segments[1] != "releases" {
		return "", fmt.Errorf("record ID %q does not belong to a release namespace", id)
	}
	return strings.Join(segments[:3], "/"), nil
}

// validateCatalogGraphV1 runs every catalog-wide invariant.
func (catalog *CatalogV1) validateCatalogGraphV1() error {
	for _, key := range catalog.sortedRecordKeysV1() {
		if err := catalog.validateRecordReferencesV1(catalog.records[key]); err != nil {
			return err
		}
	}
	if err := catalog.validateReachabilityV1(); err != nil {
		return err
	}
	return catalog.validateAcquisitionMappingsV1()
}

// validateRecordReferencesV1 proves each outgoing reference resolves exactly:
// the record exists, its digest matches, its schema is one the reference
// permits, and it stays inside the referring record's tool and release
// namespace. A tool record indexes releases and so is exempt from the release
// namespace rule, which is the only exception the design allows.
func (catalog *CatalogV1) validateRecordReferencesV1(record loadedRecordV1) error {
	ownerTool, ownerToolErr := recordToolNameV1(record.ID)
	ownerRelease, ownerReleaseErr := releaseNamespaceV1(record.ID)
	if ownerToolErr != nil {
		return ownerToolErr
	}
	if record.Schema != ToolRecordSchemaV1 && ownerReleaseErr != nil {
		return ownerReleaseErr
	}
	for _, edge := range catalogReferencesV1(record.Value) {
		target, exists := catalog.records[recordKeyV1{ID: edge.Reference.ID, Digest: edge.Reference.Digest}]
		if !exists {
			// A record with this ID may exist at another digest; the reference
			// is exact, so naming a digest the catalog does not hold is a
			// missing record rather than a digest mismatch.
			return fmt.Errorf("record %q references missing record %q at digest %s",
				record.ID, edge.Reference.ID, edge.Reference.Digest)
		}
		if !containsRecordValueV1(edge.Schemas, target.Schema) {
			return fmt.Errorf("record %q reference %q resolves to schema %q, which the reference does not permit",
				record.ID, edge.Reference.ID, target.Schema)
		}
		targetTool, err := recordToolNameV1(target.ID)
		if err != nil || targetTool != ownerTool {
			return fmt.Errorf("record %q reference %q crosses tool namespaces", record.ID, target.ID)
		}
		if record.Schema == ToolRecordSchemaV1 {
			continue
		}
		targetRelease, err := releaseNamespaceV1(target.ID)
		if err != nil || targetRelease != ownerRelease {
			return fmt.Errorf("record %q reference %q escapes release namespace %q",
				record.ID, target.ID, ownerRelease)
		}
	}
	return nil
}

// validateReachabilityV1 proves the graph is acyclic from every tool record and
// that no record is orphaned. An orphan is not harmless: it is catalog data no
// request can ever select, so it can drift out of agreement with the records
// that are reachable without anything failing.
func (catalog *CatalogV1) validateReachabilityV1() error {
	const (
		unvisited uint8 = iota
		visiting
		settled
	)
	state := make(map[recordKeyV1]uint8, len(catalog.records))
	reachable := make(map[recordKeyV1]struct{}, len(catalog.records))
	var visit func(key recordKeyV1, depth int) error
	visit = func(key recordKeyV1, depth int) error {
		if depth > maxCatalogGraphDepthV1 {
			return fmt.Errorf("catalog reference chain through %q exceeds depth %d", key.ID, maxCatalogGraphDepthV1)
		}
		if state[key] == visiting {
			return fmt.Errorf("catalog records form a cycle at %q", key.ID)
		}
		reachable[key] = struct{}{}
		if state[key] == settled {
			return nil
		}
		state[key] = visiting
		for _, edge := range catalogReferencesV1(catalog.records[key].Value) {
			if err := visit(recordKeyV1{ID: edge.Reference.ID, Digest: edge.Reference.Digest}, depth+1); err != nil {
				return err
			}
		}
		state[key] = settled
		return nil
	}
	for _, name := range catalog.Names() {
		if err := visit(catalog.tools[name], 0); err != nil {
			return err
		}
	}
	for _, key := range catalog.sortedRecordKeysV1() {
		if _, found := reachable[key]; !found {
			return fmt.Errorf("catalog record %q at digest %s is unreachable from any tool record", key.ID, key.Digest)
		}
	}
	return nil
}

// artifactContentV1 reports the content identity an artifact record declares.
func artifactContentV1(value any) (canonical.Digest, string, bool) {
	switch record := value.(type) {
	case *BindingArtifactRecordV1:
		return record.SHA256, record.Size, true
	case *PayloadRecordV1:
		return record.SHA256, record.Size, true
	}
	return "", "", false
}

// validateAcquisitionMappingsV1 proves every artifact the catalog holds has
// exactly one acquisition source, and that records sharing a content digest
// agree on size catalog-wide rather than only within one manifest.
func (catalog *CatalogV1) validateAcquisitionMappingsV1() error {
	type mapped struct {
		manifest string
		source   string
	}
	sizes := make(map[canonical.Digest]string)
	owners := make(map[canonical.Digest]string)
	mappings := make(map[canonical.Digest]mapped)
	for _, key := range catalog.sortedRecordKeysV1() {
		record := catalog.records[key]
		if digest, size, ok := artifactContentV1(record.Value); ok {
			if previous, exists := sizes[digest]; exists && previous != size {
				return fmt.Errorf("catalog artifacts %q and %q share content digest %s but declare sizes %q and %q",
					owners[digest], key.ID, digest, previous, size)
			}
			sizes[digest] = size
			owners[digest] = key.ID
		}
		manifest, ok := record.Value.(*ReleaseManifestV1)
		if !ok {
			continue
		}
		for _, mapping := range manifest.ArtifactSources {
			if previous, exists := mappings[mapping.ArtifactSHA256]; exists {
				return fmt.Errorf("content digest %s has source mappings in both %q and %q",
					mapping.ArtifactSHA256, previous.manifest, manifest.ID)
			}
			mappings[mapping.ArtifactSHA256] = mapped{manifest: manifest.ID, source: mapping.Source.ID}
		}
	}
	for digest, owner := range owners {
		if _, found := mappings[digest]; !found {
			return fmt.Errorf("catalog artifact %q has content digest %s with no acquisition source mapping", owner, digest)
		}
	}
	return nil
}
