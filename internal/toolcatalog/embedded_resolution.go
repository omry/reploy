package toolcatalog

import (
	"fmt"

	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/providers"
)

// embeddedCatalogIdentitySchemaV1 identifies the catalog identity envelope
// placed in an operation snapshot by ResolveEmbeddedPortableToolPlanV1.
//
// The identity is computed from every record in the immutable embedded
// catalog. Callers provide the other operation inputs, but cannot substitute
// a different catalog identity.
const embeddedCatalogIdentitySchemaV1 = "portable-tool-catalog-v1"
const embeddedTargetIdentitySchemaV1 = "portable-tool-observed-target-v1"

type embeddedCatalogRecordIdentityV1 struct {
	ID     string           `json:"id"`
	Schema string           `json:"schema"`
	Digest canonical.Digest `json:"digest"`
}

type embeddedCatalogIdentityV1 struct {
	Records []embeddedCatalogRecordIdentityV1 `json:"records"`
}

// ResolveEmbeddedPortableToolPlanV1 selects and jointly resolves canonical
// portable-tool requirement groups against Reploy's immutable embedded
// catalog, then compiles the selected closures into a provider-neutral plan.
// It returns the compiled plan and the joint result so callers can carry the
// selected closures and immutable operation snapshot into later stages.
//
// The Catalog and Platform fields of operation are deliberately ignored. The
// helper replaces them with identities derived from the embedded catalog and
// the exact validated target used for selection, preventing callers from
// fabricating either input. This function performs no acquisition or
// materialization.
func ResolveEmbeddedPortableToolPlanV1(
	groups []CanonicalRequirementGroupV1,
	target TargetIdentityV1,
	client ClientCapabilitiesV1,
	activeBindings []string,
	domains []ProviderDomainSetV1,
	operation ResolutionOperationInputsV1,
) (providers.PortableToolPlanV1, JointResolutionV1, error) {
	catalog := mustLoadEmbeddedCatalogV1()
	if err := validateTargetIdentityV1(target); err != nil {
		return providers.PortableToolPlanV1{}, JointResolutionV1{}, err
	}
	operation.Catalog = embeddedCatalogOperationEnvelopeV1(catalog)
	operation.Platform = embeddedTargetOperationEnvelopeV1(target)

	sets := make([]ReleaseCandidateSetV1, 0, len(groups))
	for index, group := range groups {
		candidates, err := catalog.SelectReleaseCandidatesV1(group, target, client, activeBindings)
		if err != nil {
			return providers.PortableToolPlanV1{}, JointResolutionV1{}, fmt.Errorf(
				"select portable tool requirement group %d (%s/%s): %w", index, group.Scope, group.Tool, err)
		}
		sets = append(sets, ReleaseCandidateSetV1{Group: group, Candidates: candidates})
	}

	resolution, err := catalog.ResolveSelectedClosuresV1(sets, domains, operation)
	if err != nil {
		return providers.PortableToolPlanV1{}, JointResolutionV1{}, fmt.Errorf("resolve embedded portable tool closures: %w", err)
	}
	plan, err := CompilePortableToolPlanV1(resolution.Closures)
	if err != nil {
		return providers.PortableToolPlanV1{}, JointResolutionV1{}, fmt.Errorf("compile embedded portable tool plan: %w", err)
	}
	return plan, resolution, nil
}

func embeddedTargetOperationEnvelopeV1(target TargetIdentityV1) canonical.Envelope {
	return canonical.Envelope{
		Schema: embeddedTargetIdentitySchemaV1,
		Value: canonical.Object{
			"platform":            target.Platform,
			"os_release_id":       target.OSReleaseID,
			"version_id":          target.VersionID,
			"oci_architecture":    target.OCIArchitecture,
			"native_architecture": target.NativeArchitecture,
			"package_manager":     target.PackageManager,
		},
	}
}

func embeddedCatalogOperationEnvelopeV1(catalog *CatalogV1) canonical.Envelope {
	keys := catalog.sortedRecordKeysV1()
	records := make([]embeddedCatalogRecordIdentityV1, 0, len(keys))
	for _, key := range keys {
		record := catalog.records[key]
		records = append(records, embeddedCatalogRecordIdentityV1{
			ID: record.ID, Schema: record.Schema, Digest: record.Digest,
		})
	}
	identityInput := embeddedCatalogIdentityV1{Records: records}
	identity, err := canonical.Sum("portable-tool-catalog", embeddedCatalogIdentitySchemaV1, identityInput)
	if err != nil {
		// The embedded catalog is loaded and validated before this helper can be
		// called. Its identity input contains only validated scalar fields, so a
		// failure indicates an invariant violation rather than caller input.
		panic(fmt.Sprintf("derive embedded portable tool catalog identity: %v", err))
	}
	return canonical.Envelope{
		Schema: embeddedCatalogIdentitySchemaV1,
		Value:  canonical.Object{"identity": string(identity)},
	}
}
