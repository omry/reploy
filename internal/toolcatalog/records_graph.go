package toolcatalog

import (
	"fmt"
	"sort"
	"strings"

	pep440 "github.com/aquasecurity/go-pep440-version"
	"github.com/aquasecurity/go-version/pkg/semver"
	"github.com/omry/reploy/internal/canonical"
)

// Release graph validation for the portable tool record model. Record-local
// validation lives in records_validate.go and per-target composition in
// records_compose.go; this file resolves a whole release graph and validates
// what only becomes visible once every referenced record is present.

func validateEvidenceFixtureIDV1(evidence ValidationEvidenceV1) error {
	versionSegment, err := encodeToolVersionSegmentV1(evidence.Version)
	if err != nil {
		return fmt.Errorf("validation evidence fixture version: %w", err)
	}
	segments := strings.Split(evidence.Fixture, "/")
	if len(segments) != 6 || segments[0] != "tool:"+evidence.Tool || segments[1] != "releases" || segments[2] != versionSegment || segments[3] != "validation" || segments[4] != "fixtures" || !validRecordIdentifierV1(segments[5]) {
		return fmt.Errorf("validation evidence fixture must be a canonical ID in the evidence tool and version namespace")
	}
	return validateRecordIDV1(evidence.Fixture)
}

func validateValidationEvidenceV1(evidence ValidationEvidenceV1) error {
	if evidence.Schema != ValidationEvidenceSchemaV1 || !validRecordIdentifierV1(evidence.Tool) {
		return fmt.Errorf("validation evidence identity is incomplete")
	}
	if _, err := encodeToolVersionSegmentV1(evidence.Version); err != nil {
		return fmt.Errorf("validation evidence version: %w", err)
	}
	if err := validateCanonicalDecimalV1("validation evidence revision", evidence.Revision, true); err != nil {
		return err
	}
	if err := evidence.ManifestDigest.Validate(); err != nil {
		return fmt.Errorf("validation evidence manifest digest: %w", err)
	}
	if err := evidence.SelectedClosureDigest.Validate(); err != nil {
		return fmt.Errorf("validation evidence selected closure digest: %w", err)
	}
	if err := evidence.BaseImageDigest.Validate(); err != nil {
		return fmt.Errorf("validation evidence base image digest: %w", err)
	}
	if err := validateTargetIdentityV1(evidence.Target); err != nil {
		return fmt.Errorf("validation evidence target: %w", err)
	}
	if evidence.Context != "build" && evidence.Context != "runtime" {
		return fmt.Errorf("validation evidence context is unsupported")
	}
	if err := validateSortedUniqueStringsV1("validation evidence bindings", evidence.Bindings, false); err != nil {
		return err
	}
	for _, binding := range evidence.Bindings {
		if !validRecordIdentifierV1(binding) {
			return fmt.Errorf("validation evidence bindings must be canonical identifiers")
		}
	}
	if evidence.Selections == nil || len(evidence.Selections) > maxDefinitionReferences {
		return fmt.Errorf("validation evidence selections must use a bounded map")
	}
	dimensions := make([]string, 0, len(evidence.Selections))
	for dimension := range evidence.Selections {
		dimensions = append(dimensions, dimension)
	}
	sort.Strings(dimensions)
	for _, dimension := range dimensions {
		if !validRecordIdentifierV1(dimension) {
			return fmt.Errorf("validation evidence selection dimension %q is invalid", dimension)
		}
		values := evidence.Selections[dimension]
		if err := requireNonemptySortedStringsV1("validation evidence selection values", values); err != nil {
			return err
		}
		for _, value := range values {
			if !validRecordIdentifierV1(value) {
				return fmt.Errorf("validation evidence selections must be canonical identifiers")
			}
		}
	}
	if err := validateEvidenceFixtureIDV1(evidence); err != nil {
		return err
	}
	if !validRecordTokenV1(evidence.ValidatorVersion) || evidence.Result != "pass" && evidence.Result != "fail" {
		return fmt.Errorf("validation evidence validator or result is invalid")
	}
	if err := evidence.ValidatorOutputDigest.Validate(); err != nil {
		return fmt.Errorf("validation evidence validator output digest: %w", err)
	}
	return nil
}

func validateToolVersionAliasV1(scheme string, value string) error {
	switch scheme {
	case "semver":
		if parsed, err := semver.Parse(value); err == nil {
			if parsed.String() != value {
				return fmt.Errorf("alias %q is not canonical SemVer", value)
			}
		} else {
			parts := strings.Split(value, ".")
			if len(parts) > 2 {
				return fmt.Errorf("alias %q is invalid under SemVer", value)
			}
			for _, part := range parts {
				if err := validateCanonicalDecimalV1("SemVer alias component", part, false); err != nil {
					return fmt.Errorf("alias %q is invalid under SemVer", value)
				}
			}
		}
	case "pep440":
		parsed, err := pep440.Parse(value)
		if err != nil || parsed.String() != value {
			return fmt.Errorf("alias %q is invalid under PEP 440", value)
		}
	case "integer":
		if err := validateCanonicalDecimalV1("integer tool version alias", value, false); err != nil {
			return err
		}
	case "opaque":
		if _, err := encodeToolVersionSegmentV1(value); err != nil {
			return err
		}
	default:
		return fmt.Errorf("tool version scheme is unsupported")
	}
	return nil
}

func validateToolReleaseManifestsV1(tool *ToolRecordV1, manifests []*ReleaseManifestV1) error {
	if tool == nil {
		return fmt.Errorf("tool release manifests require a tool record")
	}
	coordinates := make(map[string]string, len(manifests))
	exactVersions := make(map[string]struct{}, len(manifests))
	for index, manifest := range manifests {
		if manifest == nil || manifest.Tool != tool.Name {
			return fmt.Errorf("tool release index manifest %d belongs to a different tool", index)
		}
		if err := validateToolVersionV1(tool.VersionScheme, manifest.Version); err != nil {
			return fmt.Errorf("tool release index manifest %d version: %w", index, err)
		}
		exactVersions[manifest.Version] = struct{}{}
		for aliasIndex, alias := range manifest.Aliases {
			if err := validateToolVersionAliasV1(tool.VersionScheme, alias); err != nil {
				return fmt.Errorf("tool release index manifest %d alias %d: %w", index, aliasIndex, err)
			}
		}
		for _, token := range append([]string{manifest.Version}, manifest.Aliases...) {
			if existing, found := coordinates[token]; found && existing != manifest.Version {
				return fmt.Errorf("tool release index token %q maps to both %q and %q", token, existing, manifest.Version)
			}
			coordinates[token] = manifest.Version
		}
	}
	if tool.VersionScheme == "opaque" {
		if _, found := exactVersions[tool.DefaultVersion]; !found {
			return fmt.Errorf("opaque default version %q is not an advertised exact release", tool.DefaultVersion)
		}
	}
	return nil
}

func validateToolReleaseIndexV1(tool *ToolRecordV1, records map[string]loadedRecordV1) error {
	if tool == nil {
		return fmt.Errorf("tool release index requires a tool record")
	}
	if len(tool.Releases) == 0 || len(tool.Releases) > maxDefinitionReferences {
		return fmt.Errorf("tool release index must contain a nonempty bounded manifest list")
	}
	manifests := make([]*ReleaseManifestV1, 0, len(tool.Releases))
	for index, reference := range tool.Releases {
		record, err := resolvedRecordV1(records, reference)
		if err != nil {
			return fmt.Errorf("tool release index reference %d: %w", index, err)
		}
		manifest, ok := record.Value.(*ReleaseManifestV1)
		if !ok {
			return fmt.Errorf("tool release index reference %d resolves to a non-manifest record", index)
		}
		manifests = append(manifests, manifest)
	}
	return validateToolReleaseManifestsV1(tool, manifests)
}

func validateManifestResolvedGraphV1(manifest *ReleaseManifestV1, records map[string]loadedRecordV1) error {
	if manifest == nil {
		return fmt.Errorf("release graph requires a manifest")
	}
	contractRecord, err := resolvedRecordV1(records, manifest.Contract)
	if err != nil {
		return fmt.Errorf("release contract: %w", err)
	}
	contract, ok := contractRecord.Value.(*ReleaseContractV1)
	if !ok {
		return fmt.Errorf("release contract reference resolves to a non-contract record")
	}
	manifestProfiles := make(map[string]RecordReferenceV1, len(manifest.ValidationProfiles))
	for index, reference := range manifest.ValidationProfiles {
		profileRecord, err := resolvedRecordV1(records, reference)
		if err != nil {
			return fmt.Errorf("release validation profile %d: %w", index, err)
		}
		profile, ok := profileRecord.Value.(*ValidationProfileRecordV1)
		if !ok || profile.Tool != manifest.Tool || profile.Version != manifest.Version {
			return fmt.Errorf("release validation profile %d is incompatible with the release", index)
		}
		if previous, exists := manifestProfiles[reference.ID]; exists && previous != reference {
			return fmt.Errorf("release validation profile %q has conflicting exact identities", reference.ID)
		}
		manifestProfiles[reference.ID] = reference
	}
	validateDeclaredProfiles := func(field string, references []RecordReferenceV1) error {
		for _, reference := range references {
			declared, exists := manifestProfiles[reference.ID]
			if !exists || declared != reference {
				return fmt.Errorf("%s %q is not declared exactly by the release manifest", field, reference.ID)
			}
		}
		return nil
	}
	reachableArtifacts := make(map[canonical.Digest]map[string]struct{})
	artifactSizes := make(map[canonical.Digest]string)
	addArtifact := func(reference RecordReferenceV1, target *TargetRecordV1, binding string) error {
		record, err := resolvedRecordV1(records, reference)
		if err != nil {
			return err
		}
		var digest canonical.Digest
		var size string
		switch value := record.Value.(type) {
		case *BindingArtifactRecordV1:
			if binding == "" || value.Binding != binding || value.Platform != target.Target.Platform {
				return fmt.Errorf("binding artifact %q is incompatible with target %q", value.ID, target.ID)
			}
			digest, size = value.SHA256, value.Size
		case *PayloadRecordV1:
			if value.Platform != target.Target.Platform {
				return fmt.Errorf("payload %q is incompatible with target %q", value.ID, target.ID)
			}
			digest, size = value.SHA256, value.Size
		default:
			return fmt.Errorf("target artifact reference %q resolves to a non-artifact record", reference.ID)
		}
		// A content digest fixes the bytes, so every record claiming that digest
		// must agree on how many there are. The source mapping only size-checks
		// the one record it names, which leaves a second reachable record free
		// to declare the same digest at a different size.
		if previous, exists := artifactSizes[digest]; exists && previous != size {
			return fmt.Errorf("artifacts sharing content digest %s disagree on size: %q and %q", digest, previous, size)
		}
		artifactSizes[digest] = size
		if reachableArtifacts[digest] == nil {
			reachableArtifacts[digest] = make(map[string]struct{})
		}
		reachableArtifacts[digest][record.ID] = struct{}{}
		return nil
	}

	targets := make(map[TargetIdentityV1]string, len(manifest.Targets))
	for _, targetReference := range manifest.Targets {
		targetRecord, err := resolvedRecordV1(records, targetReference)
		if err != nil {
			return fmt.Errorf("release target: %w", err)
		}
		target, ok := targetRecord.Value.(*TargetRecordV1)
		if !ok {
			return fmt.Errorf("release target reference %q resolves to a non-target record", targetReference.ID)
		}
		if err := validateTargetAgainstContractV1(contract, target); err != nil {
			return fmt.Errorf("target %q: %w", target.ID, err)
		}
		// The per-artifact contract check below proves one wheel is usable by
		// some advertised interpreter. Only the set-level check proves the
		// artifacts a binding selects cover every interpreter its contract
		// advertises, so the walker must reach it or that rule never runs.
		if err := validateTargetBindingsAgainstContractsV1(records, target); err != nil {
			return fmt.Errorf("target %q: %w", target.ID, err)
		}
		if previous, exists := targets[target.Target]; exists {
			return fmt.Errorf("release targets %q and %q describe the same target", previous, target.ID)
		}
		targets[target.Target] = target.ID
		if err := validateDeclaredProfiles("target validation profile", target.ValidationProfiles); err != nil {
			return fmt.Errorf("target %q: %w", target.ID, err)
		}
		if err := validatePackageSetReferencesV1(records, target.PackageSets, target); err != nil {
			return err
		}
		fixtures := make([]*IntegrationFixtureRecordV1, 0, len(target.IntegrationFixtures))
		for _, fixtureReference := range target.IntegrationFixtures {
			fixtureRecord, err := resolvedRecordV1(records, fixtureReference)
			if err != nil {
				return fmt.Errorf("target %q integration fixture: %w", target.ID, err)
			}
			fixture, ok := fixtureRecord.Value.(*IntegrationFixtureRecordV1)
			if !ok || fixture.Target != target.Target {
				return fmt.Errorf("target %q integration fixture %q describes a different target", target.ID, fixtureReference.ID)
			}
			if err := validateFixtureAgainstTargetV1(contract, target, fixture); err != nil {
				return fmt.Errorf("target %q integration fixture %q: %w", target.ID, fixture.ID, err)
			}
			if err := validateDeclaredProfiles("integration fixture validation profile", fixture.ValidationProfiles); err != nil {
				return fmt.Errorf("target %q integration fixture %q: %w", target.ID, fixture.ID, err)
			}
			fixtures = append(fixtures, fixture)
		}
		for _, binding := range target.Bindings {
			bindingContractRecord, err := resolvedRecordV1(records, binding.Contract)
			if err != nil {
				return fmt.Errorf("target %q binding %q contract: %w", target.ID, binding.Name, err)
			}
			bindingContract, ok := bindingContractRecord.Value.(*BindingContractV1)
			if !ok || bindingContract.Name != binding.Name {
				return fmt.Errorf("target %q binding %q resolves an incompatible contract", target.ID, binding.Name)
			}
			if err := validatePackageSetReferencesV1(records, binding.PackageSets, target); err != nil {
				return err
			}
			if err := validateDeclaredProfiles("target binding validation profile", binding.ValidationProfiles); err != nil {
				return fmt.Errorf("target %q binding %q: %w", target.ID, binding.Name, err)
			}
			for _, reference := range binding.Artifacts {
				if err := addArtifact(reference, target, binding.Name); err != nil {
					return err
				}
			}
			for _, reference := range binding.Payloads {
				if err := addArtifact(reference, target, ""); err != nil {
					return err
				}
			}
		}
		for _, reference := range target.Payloads {
			if err := addArtifact(reference, target, ""); err != nil {
				return err
			}
		}
		for _, selection := range target.Selections {
			if err := validatePackageSetReferencesV1(records, selection.PackageSets, target); err != nil {
				return err
			}
			if err := validateDeclaredProfiles("target selection validation profile", selection.ValidationProfiles); err != nil {
				return fmt.Errorf("target %q selection %q=%q: %w", target.ID, selection.Dimension, selection.Value, err)
			}
			for _, reference := range selection.Payloads {
				if err := addArtifact(reference, target, ""); err != nil {
					return err
				}
			}
		}
		if err := validateTargetFixtureCoverageV1(records, contract, target, fixtures); err != nil {
			return fmt.Errorf("target %q: %w", target.ID, err)
		}
	}

	mappedDigests := make(map[canonical.Digest]struct{}, len(manifest.ArtifactSources))
	for index, mapping := range manifest.ArtifactSources {
		artifact, exists := records[mapping.Artifact.ID]
		if !exists || artifact.ID != mapping.Artifact.ID || artifact.Digest != mapping.Artifact.Digest {
			return fmt.Errorf("artifact source mapping %d does not resolve its exact artifact record", index)
		}
		var artifactSHA256 canonical.Digest
		var artifactSize string
		var artifactResolver string
		switch value := artifact.Value.(type) {
		case *BindingArtifactRecordV1:
			artifactSHA256, artifactSize, artifactResolver = value.SHA256, value.Size, value.Resolver
		case *PayloadRecordV1:
			artifactSHA256, artifactSize, artifactResolver = value.SHA256, value.Size, value.Resolver
		default:
			return fmt.Errorf("artifact source mapping %d references a non-artifact record", index)
		}

		sourceRecord, exists := records[mapping.Source.ID]
		if !exists || sourceRecord.ID != mapping.Source.ID || sourceRecord.Digest != mapping.Source.Digest {
			return fmt.Errorf("artifact source mapping %d does not resolve its exact source record", index)
		}
		source, ok := sourceRecord.Value.(*ArtifactSourceRecordV1)
		if !ok {
			return fmt.Errorf("artifact source mapping %d references a non-source record", index)
		}
		if mapping.ArtifactSHA256 != artifactSHA256 || mapping.ArtifactSHA256 != source.SHA256 {
			return fmt.Errorf("artifact source mapping %d content digests disagree", index)
		}
		if artifactSize == "" {
			return fmt.Errorf("artifact source mapping %d references an artifact without an exact size", index)
		}
		if !containsRecordValueV1(contract.ResolverPrimitives, artifactResolver) {
			return fmt.Errorf("artifact source mapping %d uses a resolver outside the release contract", index)
		}
		group, reachable := reachableArtifacts[mapping.ArtifactSHA256]
		if !reachable {
			return fmt.Errorf("artifact source mapping %d is not reachable from an advertised target", index)
		}
		if _, belongs := group[artifact.ID]; !belongs {
			return fmt.Errorf("artifact source mapping %d names an artifact outside its reachable content group", index)
		}
		if _, duplicate := mappedDigests[mapping.ArtifactSHA256]; duplicate {
			return fmt.Errorf("artifact content digest %s has more than one source mapping", mapping.ArtifactSHA256)
		}
		mappedDigests[mapping.ArtifactSHA256] = struct{}{}
	}
	for digest := range reachableArtifacts {
		if _, exists := mappedDigests[digest]; !exists {
			return fmt.Errorf("reachable artifact content digest %s has no source mapping", digest)
		}
	}
	return nil
}
