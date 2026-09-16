package toolcatalog

import (
	"fmt"
	"path"
	"regexp"

	"github.com/aquasecurity/go-version/pkg/semver"

	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providerstore"
)

// embeddedResolverPrimitivesV1 lists the acquisition primitives this Reploy
// build implements for embedded-catalog candidates. Release contracts require
// every primitive they name to be present here before a candidate is eligible.
var embeddedResolverPrimitivesV1 = []string{"https-sha256"}

var reployReleaseCoordinateV1 = regexp.MustCompile(`^([0-9]+\.[0-9]+\.[0-9]+)(?:[.-]?(?:dev|a|b|rc|post)[0-9]*)?$`)

// ClientReployVersionV1 maps Reploy's release identity to the semantic release
// coordinate that contract `supported_reploy` constraints are written against.
// A development or pre-release build carries the capabilities of the release
// it is building toward, so its suffix is dropped rather than treated as a
// semantic prerelease that no constraint would admit.
func ClientReployVersionV1(raw string) (string, error) {
	match := reployReleaseCoordinateV1.FindStringSubmatch(raw)
	if match == nil {
		return "", fmt.Errorf("Reploy version %q is not a release coordinate", raw)
	}
	if _, err := semver.Parse(match[1]); err != nil {
		return "", fmt.Errorf("Reploy version %q release coordinate: %w", raw, err)
	}
	return match[1], nil
}

// EmbeddedClientCapabilitiesV1 describes the running Reploy client for
// embedded-catalog candidate selection.
func EmbeddedClientCapabilitiesV1(reployVersion string) (ClientCapabilitiesV1, error) {
	version, err := ClientReployVersionV1(reployVersion)
	if err != nil {
		return ClientCapabilitiesV1{}, err
	}
	return ClientCapabilitiesV1{
		ReployVersion:      version,
		ResolverPrimitives: append([]string{}, embeddedResolverPrimitivesV1...),
	}, nil
}

// EmbeddedPortableToolArtifactSourceV1 joins one selected artifact with the
// exact catalog source record that authorizes its acquisition.
type EmbeddedPortableToolArtifactSourceV1 struct {
	Scope      string
	Tool       string
	Artifact   providers.PortableToolRecordReferenceV1
	Descriptor providerstore.ArtifactDescriptor
	Source     providers.PortableToolSelectedRecordV1
	Mirrors    []string
}

// EmbeddedPortableToolLockRecordSetV1 carries the manifest and source records a
// build lock must retain for selected closures.
type EmbeddedPortableToolLockRecordSetV1 struct {
	Releases  []providers.PortableToolReleaseManifestInputV1
	Artifacts []EmbeddedPortableToolArtifactSourceV1
}

// EmbeddedPortableToolLockRecordsV1 resolves, from the immutable embedded
// catalog, the release manifest that authorized each selected closure and the
// artifact-source record mapped to every selected payload or binding artifact.
// It reads catalog records only; it performs no acquisition.
func EmbeddedPortableToolLockRecordsV1(closures []SelectedClosureV1) (EmbeddedPortableToolLockRecordSetV1, error) {
	catalog := mustLoadEmbeddedCatalogV1()
	result := EmbeddedPortableToolLockRecordSetV1{
		Releases:  make([]providers.PortableToolReleaseManifestInputV1, 0, len(closures)),
		Artifacts: []EmbeddedPortableToolArtifactSourceV1{},
	}
	for index := range closures {
		closure := &closures[index]
		manifestKey, manifest, err := catalog.embeddedManifestForClosureV1(closure)
		if err != nil {
			return EmbeddedPortableToolLockRecordSetV1{}, fmt.Errorf("selected closure %d (%s/%s): %w", index, closure.Scope, closure.Provenance.Tool, err)
		}
		manifestRecord, err := portableToolRecordEnvelopeV1(manifest)
		if err != nil {
			return EmbeddedPortableToolLockRecordSetV1{}, err
		}
		result.Releases = append(result.Releases, providers.PortableToolReleaseManifestInputV1{
			Scope: closure.Scope, Tool: closure.Provenance.Tool,
			Manifest: providers.PortableToolSelectedRecordV1{
				Reference: providers.PortableToolRecordReferenceV1{ID: manifestKey.ID, Digest: manifestKey.Digest},
				Record:    manifestRecord,
			},
		})
		seenArtifacts := make(map[RecordReferenceV1]struct{}, len(closure.Records.BindingArtifacts)+len(closure.Records.Payloads))
		for _, bindingArtifact := range closure.Records.BindingArtifacts {
			if _, exists := seenArtifacts[bindingArtifact.Reference]; exists {
				return EmbeddedPortableToolLockRecordSetV1{}, fmt.Errorf("selected closure %d (%s/%s) repeats artifact %s", index, closure.Scope, closure.Provenance.Tool, bindingArtifact.Reference.ID)
			}
			seenArtifacts[bindingArtifact.Reference] = struct{}{}
			artifact, err := catalog.embeddedBindingArtifactSourceV1(closure, manifest, bindingArtifact)
			if err != nil {
				return EmbeddedPortableToolLockRecordSetV1{}, fmt.Errorf("selected closure %d (%s/%s): %w", index, closure.Scope, closure.Provenance.Tool, err)
			}
			result.Artifacts = append(result.Artifacts, artifact)
		}
		for _, payload := range closure.Records.Payloads {
			if _, exists := seenArtifacts[payload.Reference]; exists {
				return EmbeddedPortableToolLockRecordSetV1{}, fmt.Errorf("selected closure %d (%s/%s) repeats artifact %s", index, closure.Scope, closure.Provenance.Tool, payload.Reference.ID)
			}
			seenArtifacts[payload.Reference] = struct{}{}
			artifact, err := catalog.embeddedArtifactSourceV1(closure, manifest, payload)
			if err != nil {
				return EmbeddedPortableToolLockRecordSetV1{}, fmt.Errorf("selected closure %d (%s/%s): %w", index, closure.Scope, closure.Provenance.Tool, err)
			}
			result.Artifacts = append(result.Artifacts, artifact)
		}
	}
	return result, nil
}

func (catalog *CatalogV1) embeddedManifestForClosureV1(closure *SelectedClosureV1) (recordKeyV1, *ReleaseManifestV1, error) {
	for _, key := range catalog.sortedRecordKeysV1() {
		record := catalog.records[key]
		if record.Schema != ReleaseManifestSchemaV1 || record.Digest != closure.Provenance.ManifestDigest {
			continue
		}
		manifest, ok := record.Value.(*ReleaseManifestV1)
		if !ok {
			return recordKeyV1{}, nil, fmt.Errorf("catalog record %s is not a release manifest", key.ID)
		}
		if manifest.Tool != closure.Provenance.Tool || manifest.Version != closure.Provenance.Version || manifest.Revision != closure.Provenance.Revision {
			return recordKeyV1{}, nil, fmt.Errorf("release manifest %s does not match the selected provenance %s %s~%s", key.ID, closure.Provenance.Tool, closure.Provenance.Version, closure.Provenance.Revision)
		}
		return key, manifest, nil
	}
	return recordKeyV1{}, nil, fmt.Errorf("embedded catalog has no release manifest with digest %s", closure.Provenance.ManifestDigest)
}

func (catalog *CatalogV1) embeddedArtifactSourceV1(
	closure *SelectedClosureV1,
	manifest *ReleaseManifestV1,
	payload SelectedPayloadRecordV1,
) (EmbeddedPortableToolArtifactSourceV1, error) {
	return catalog.embeddedSelectedArtifactSourceV1(
		closure, manifest, payload.Reference, &payload.Record, PayloadRecordSchemaV1,
	)
}

func (catalog *CatalogV1) embeddedBindingArtifactSourceV1(
	closure *SelectedClosureV1,
	manifest *ReleaseManifestV1,
	artifact SelectedBindingArtifactRecordV1,
) (EmbeddedPortableToolArtifactSourceV1, error) {
	return catalog.embeddedSelectedArtifactSourceV1(
		closure, manifest, artifact.Reference, &artifact.Record, BindingArtifactSchemaV1,
	)
}

func (catalog *CatalogV1) embeddedSelectedArtifactSourceV1(
	closure *SelectedClosureV1,
	manifest *ReleaseManifestV1,
	artifactReference RecordReferenceV1,
	artifactValue any,
	expectedSchema string,
) (EmbeddedPortableToolArtifactSourceV1, error) {
	if closure == nil || manifest == nil {
		return EmbeddedPortableToolArtifactSourceV1{}, fmt.Errorf("embedded artifact source projection requires a closure and release manifest")
	}
	loaded, err := catalog.exactRecordV1(artifactReference)
	if err != nil {
		return EmbeddedPortableToolArtifactSourceV1{}, fmt.Errorf("selected %s %s: %w", expectedSchema, artifactReference.ID, err)
	}
	if loaded.Schema != expectedSchema {
		return EmbeddedPortableToolArtifactSourceV1{}, fmt.Errorf("selected artifact %s resolves to schema %q, want %q", artifactReference.ID, loaded.Schema, expectedSchema)
	}
	selectedDigest, err := canonical.Sum("portable-tool-record", portableToolRecordIdentityV1, artifactValue)
	if err != nil {
		return EmbeddedPortableToolArtifactSourceV1{}, fmt.Errorf("selected artifact %s identity: %w", artifactReference.ID, err)
	}
	if selectedDigest != artifactReference.Digest || loaded.Digest != artifactReference.Digest {
		return EmbeddedPortableToolArtifactSourceV1{}, fmt.Errorf("selected artifact %s record does not match its exact reference digest", artifactReference.ID)
	}

	var (
		artifactSHA256 canonical.Digest
		artifactSize   string
		descriptor     providerstore.ArtifactDescriptor
	)
	switch record := artifactValue.(type) {
	case *BindingArtifactRecordV1:
		if record.ID != artifactReference.ID {
			return EmbeddedPortableToolArtifactSourceV1{}, fmt.Errorf("selected binding artifact %s record ID is %q", artifactReference.ID, record.ID)
		}
		if record.Filename == "" || path.Base(record.Filename) != record.Filename {
			return EmbeddedPortableToolArtifactSourceV1{}, fmt.Errorf("selected binding artifact %s filename is not a single basename", artifactReference.ID)
		}
		artifactSHA256, artifactSize = record.SHA256, record.Size
		descriptor = providerstore.ArtifactDescriptor{
			LogicalPath: "wheels/" + record.Filename,
			Kind:        "wheel",
			Size:        record.Size,
			SHA256:      record.SHA256,
		}
	case *PayloadRecordV1:
		if record.ID != artifactReference.ID {
			return EmbeddedPortableToolArtifactSourceV1{}, fmt.Errorf("selected payload %s record ID is %q", artifactReference.ID, record.ID)
		}
		artifactSHA256, artifactSize = record.SHA256, record.Size
		descriptor = providerstore.ArtifactDescriptor{
			LogicalPath: record.LogicalPath,
			Kind:        record.Kind,
			Size:        record.Size,
			SHA256:      record.SHA256,
		}
	default:
		return EmbeddedPortableToolArtifactSourceV1{}, fmt.Errorf("selected artifact %s has unsupported record type %T", artifactReference.ID, artifactValue)
	}
	if expectedSchema == BindingArtifactSchemaV1 {
		binding, ok := artifactValue.(*BindingArtifactRecordV1)
		if !ok || binding.Filename == "" || descriptor.LogicalPath != "wheels/"+binding.Filename || descriptor.Kind != "wheel" {
			return EmbeddedPortableToolArtifactSourceV1{}, fmt.Errorf("selected binding artifact %s does not produce an exact wheel descriptor", artifactReference.ID)
		}
	}
	if artifactSize == "" || artifactSHA256 == "" {
		return EmbeddedPortableToolArtifactSourceV1{}, fmt.Errorf("selected artifact %s has incomplete content identity", artifactReference.ID)
	}
	if err := descriptor.Validate(); err != nil {
		return EmbeddedPortableToolArtifactSourceV1{}, fmt.Errorf("selected artifact %s descriptor: %w", artifactReference.ID, err)
	}

	var mapping *ArtifactSourceMappingV1
	for index := range manifest.ArtifactSources {
		candidate := &manifest.ArtifactSources[index]
		if candidate.Artifact.ID != artifactReference.ID || candidate.Artifact.Digest != artifactReference.Digest {
			continue
		}
		if mapping != nil {
			return EmbeddedPortableToolArtifactSourceV1{}, fmt.Errorf("release manifest %s has duplicate source mappings for artifact %s", manifest.ID, artifactReference.ID)
		}
		mapping = candidate
	}
	if mapping == nil {
		return EmbeddedPortableToolArtifactSourceV1{}, fmt.Errorf("release manifest %s maps no source for artifact %s", manifest.ID, artifactReference.ID)
	}
	if mapping.ArtifactSHA256 != artifactSHA256 {
		return EmbeddedPortableToolArtifactSourceV1{}, fmt.Errorf("manifest source mapping for %s names content %s but the selected artifact records %s", artifactReference.ID, mapping.ArtifactSHA256, artifactSHA256)
	}

	sourceRecord, err := catalog.exactRecordV1(mapping.Source)
	if err != nil {
		return EmbeddedPortableToolArtifactSourceV1{}, fmt.Errorf("embedded catalog source record %s: %w", mapping.Source.ID, err)
	}
	if sourceRecord.Schema != ArtifactSourceRecordSchemaV1 {
		return EmbeddedPortableToolArtifactSourceV1{}, fmt.Errorf("catalog record %s is not an artifact source", mapping.Source.ID)
	}
	source, ok := sourceRecord.Value.(*ArtifactSourceRecordV1)
	if !ok || source.ID != mapping.Source.ID {
		return EmbeddedPortableToolArtifactSourceV1{}, fmt.Errorf("catalog record %s is not an artifact source", mapping.Source.ID)
	}
	sourceDigest, err := canonical.Sum("portable-tool-record", portableToolRecordIdentityV1, source)
	if err != nil {
		return EmbeddedPortableToolArtifactSourceV1{}, fmt.Errorf("source record %s identity: %w", source.ID, err)
	}
	if sourceDigest != mapping.Source.Digest || sourceRecord.Digest != mapping.Source.Digest {
		return EmbeddedPortableToolArtifactSourceV1{}, fmt.Errorf("source record %s does not match its exact reference digest", source.ID)
	}
	if source.SHA256 != artifactSHA256 {
		return EmbeddedPortableToolArtifactSourceV1{}, fmt.Errorf("source record %s authorizes content %s, not selected artifact content %s", source.ID, source.SHA256, artifactSHA256)
	}
	if err := providerstore.ValidateArtifactSource(providerstore.ArtifactSource{
		ID: source.ID, SHA256: source.SHA256, Mirrors: source.Mirrors,
	}, descriptor); err != nil {
		return EmbeddedPortableToolArtifactSourceV1{}, fmt.Errorf("source record %s: %w", source.ID, err)
	}
	sourceEnvelope, err := portableToolRecordEnvelopeV1(source)
	if err != nil {
		return EmbeddedPortableToolArtifactSourceV1{}, err
	}
	return EmbeddedPortableToolArtifactSourceV1{
		Scope: closure.Scope, Tool: closure.Provenance.Tool,
		Artifact:   providers.PortableToolRecordReferenceV1{ID: artifactReference.ID, Digest: artifactReference.Digest},
		Descriptor: descriptor,
		Source: providers.PortableToolSelectedRecordV1{
			Reference: providers.PortableToolRecordReferenceV1{ID: mapping.Source.ID, Digest: mapping.Source.Digest},
			Record:    sourceEnvelope,
		},
		Mirrors: append([]string{}, source.Mirrors...),
	}, nil
}
