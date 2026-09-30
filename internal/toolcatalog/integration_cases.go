package toolcatalog

import (
	"fmt"
	"sort"

	"github.com/omry/reploy/internal/canonical"
)

const (
	integrationCaseIdentityKindV1   = "portable-tool-integration-case"
	integrationCaseIdentitySchemaV1 = "portable-tool-integration-case-v1"
)

// IntegrationCaseV1 is one advertised target support case paired with its
// exact fixture and selected profiles. Derivation does not run or validate an
// image and does not create external support evidence.
type IntegrationCaseV1 struct {
	ID                canonical.Digest
	ManifestReference RecordReferenceV1
	Manifest          ReleaseManifestV1
	TargetReference   RecordReferenceV1
	Target            TargetRecordV1
	Support           TargetSupportCaseV1
	Fixture           IntegrationFixtureRecordV1
	Profiles          []ValidationProfileRecordV1
}

// DeriveIntegrationCasesV1 preflights the complete catalog before returning
// any runnable cases. The target leaves, rather than release-wide context or
// option schemas, own the case set.
func (catalog *CatalogV1) DeriveIntegrationCasesV1() ([]IntegrationCaseV1, error) {
	if catalog == nil {
		return nil, fmt.Errorf("portable tool catalog is nil")
	}
	if err := catalog.validateReleaseGraphsV1(); err != nil {
		return nil, fmt.Errorf("integration case preflight: %w", err)
	}
	type orderedCase struct {
		key    string
		caseV1 IntegrationCaseV1
	}
	var ordered []orderedCase
	seen := make(map[canonical.Digest]struct{})
	for _, name := range catalog.Names() {
		toolKey := catalog.tools[name]
		tool, ok := catalog.records[toolKey].Value.(*ToolRecordV1)
		if !ok {
			return nil, fmt.Errorf("tool %q is not a tool record", name)
		}
		for _, manifestRef := range tool.Releases {
			view, err := catalog.resolvedViewV1(recordKeyV1{ID: manifestRef.ID, Digest: manifestRef.Digest})
			if err != nil {
				return nil, err
			}
			manifestRecord, err := resolvedRecordV1(view, manifestRef)
			if err != nil {
				return nil, err
			}
			manifest, ok := manifestRecord.Value.(*ReleaseManifestV1)
			if !ok {
				return nil, fmt.Errorf("release %q is not a manifest", manifestRef.ID)
			}
			contractRecord, err := resolvedRecordV1(view, manifest.Contract)
			if err != nil {
				return nil, err
			}
			contract, ok := contractRecord.Value.(*ReleaseContractV1)
			if !ok {
				return nil, fmt.Errorf("release %q contract is not a contract record", manifest.ID)
			}
			for _, targetRef := range manifest.Targets {
				targetRecord, err := resolvedRecordV1(view, targetRef)
				if err != nil {
					return nil, err
				}
				target, ok := targetRecord.Value.(*TargetRecordV1)
				if !ok {
					return nil, fmt.Errorf("release %q target %q is not a target record", manifest.ID, targetRef.ID)
				}
				supportCases, err := targetSupportTuplesV1(contract, target)
				if err != nil {
					return nil, fmt.Errorf("target %q support cases: %w", target.ID, err)
				}
				for _, support := range supportCases {
					tupleKey, err := supportTupleKeyV1(support)
					if err != nil {
						return nil, err
					}
					fixture, profiles, err := candidateValidationRecordsV1(view, target, support)
					if err != nil {
						return nil, fmt.Errorf("target %q support case: %w", target.ID, err)
					}
					identityInput := struct {
						Manifest RecordReferenceV1   `json:"manifest"`
						Target   RecordReferenceV1   `json:"target"`
						Support  TargetSupportCaseV1 `json:"support"`
					}{manifestRef, targetRef, support}
					id, err := canonical.Sum(integrationCaseIdentityKindV1, integrationCaseIdentitySchemaV1, identityInput)
					if err != nil {
						return nil, fmt.Errorf("integration case identity: %w", err)
					}
					if _, duplicate := seen[id]; duplicate {
						return nil, fmt.Errorf("integration case %s is duplicated", id)
					}
					seen[id] = struct{}{}
					selectedProfiles := make([]ValidationProfileRecordV1, 0, len(profiles))
					for _, profile := range profiles {
						selectedProfiles = append(selectedProfiles, cloneValidationProfileV1(profile))
					}
					ordered = append(ordered, orderedCase{
						key: name + "\x00" + manifestRef.ID + "\x00" + string(manifestRef.Digest) + "\x00" + targetRef.ID + "\x00" + tupleKey,
						caseV1: IntegrationCaseV1{
							ID: id, ManifestReference: manifestRef, Manifest: cloneReleaseManifestV1(manifest),
							TargetReference: targetRef, Target: cloneTargetRecordV1(target),
							Support: TargetSupportCaseV1{Context: support.Context, Bindings: cloneSliceV1(support.Bindings), Selections: cloneSelectionMapV1(support.Selections)},
							Fixture: cloneIntegrationFixtureV1(fixture), Profiles: selectedProfiles,
						},
					})
				}
			}
		}
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].key < ordered[j].key })
	cases := make([]IntegrationCaseV1, len(ordered))
	for index, item := range ordered {
		cases[index] = item.caseV1
	}
	return cases, nil
}

// EmbeddedIntegrationCasesV1 exposes the same preflight over the generated
// first-party catalog for integration harnesses.
func EmbeddedIntegrationCasesV1() ([]IntegrationCaseV1, error) {
	return mustLoadEmbeddedCatalogV1().DeriveIntegrationCasesV1()
}
