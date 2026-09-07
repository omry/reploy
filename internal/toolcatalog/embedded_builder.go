package toolcatalog

import (
	"fmt"
	"regexp"

	"github.com/aquasecurity/go-version/pkg/semver"

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
// artifact-source record mapped to every selected payload. It reads catalog
// records only; it performs no acquisition.
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
		if len(closure.Records.BindingArtifacts) != 0 {
			return EmbeddedPortableToolLockRecordSetV1{}, fmt.Errorf(
				"selected closure %d (%s/%s) selects binding artifacts, which the embedded lock-record projection does not acquire yet",
				index, closure.Scope, closure.Provenance.Tool,
			)
		}
		for _, payload := range closure.Records.Payloads {
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
	for _, mapping := range manifest.ArtifactSources {
		if mapping.Artifact.ID != payload.Reference.ID || mapping.Artifact.Digest != payload.Reference.Digest {
			continue
		}
		if mapping.ArtifactSHA256 != payload.Record.SHA256 {
			return EmbeddedPortableToolArtifactSourceV1{}, fmt.Errorf("manifest source mapping for %s names content %s but the payload records %s", payload.Reference.ID, mapping.ArtifactSHA256, payload.Record.SHA256)
		}
		record, exists := catalog.records[recordKeyV1{ID: mapping.Source.ID, Digest: mapping.Source.Digest}]
		if !exists {
			return EmbeddedPortableToolArtifactSourceV1{}, fmt.Errorf("embedded catalog has no source record %s at digest %s", mapping.Source.ID, mapping.Source.Digest)
		}
		source, ok := record.Value.(*ArtifactSourceRecordV1)
		if !ok {
			return EmbeddedPortableToolArtifactSourceV1{}, fmt.Errorf("catalog record %s is not an artifact source", mapping.Source.ID)
		}
		if source.SHA256 != payload.Record.SHA256 {
			return EmbeddedPortableToolArtifactSourceV1{}, fmt.Errorf("source record %s authorizes content %s, not payload content %s", source.ID, source.SHA256, payload.Record.SHA256)
		}
		sourceRecord, err := portableToolRecordEnvelopeV1(source)
		if err != nil {
			return EmbeddedPortableToolArtifactSourceV1{}, err
		}
		descriptor := providerstore.ArtifactDescriptor{
			LogicalPath: payload.Record.LogicalPath, Kind: payload.Record.Kind,
			Size: payload.Record.Size, SHA256: payload.Record.SHA256,
		}
		if err := descriptor.Validate(); err != nil {
			return EmbeddedPortableToolArtifactSourceV1{}, fmt.Errorf("payload %s artifact descriptor: %w", payload.Reference.ID, err)
		}
		return EmbeddedPortableToolArtifactSourceV1{
			Scope: closure.Scope, Tool: closure.Provenance.Tool,
			Artifact:   providers.PortableToolRecordReferenceV1{ID: payload.Reference.ID, Digest: payload.Reference.Digest},
			Descriptor: descriptor,
			Source: providers.PortableToolSelectedRecordV1{
				Reference: providers.PortableToolRecordReferenceV1{ID: mapping.Source.ID, Digest: mapping.Source.Digest},
				Record:    sourceRecord,
			},
			Mirrors: append([]string{}, source.Mirrors...),
		}, nil
	}
	return EmbeddedPortableToolArtifactSourceV1{}, fmt.Errorf("release manifest %s maps no source for payload %s", manifest.ID, payload.Reference.ID)
}
