package providers

import (
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/portabletool"
	"github.com/omry/reploy/internal/providerstore"
)

const (
	PortableToolLockSchemaV1                  = "portable-tool-lock-v1"
	PortableToolReleaseManifestRecordSchemaV1 = portabletool.ReleaseManifestSchemaV1
	portableToolLockMaxRecordReferencesV1     = 1024
)

// PortableToolLockV1 is the complete portable-tool portion of a build lock.
// The provider DAG retains the selected closure, release, base-plan, and
// provider identities. Acquisitions bind every selected artifact to the exact
// source record and verified bytes used by the build.
type PortableToolLockV1 struct {
	Schema       string                                  `json:"schema"`
	Plan         PortableToolProviderDAGV1               `json:"plan"`
	Releases     []PortableToolReleaseManifestLockV1     `json:"releases"`
	Acquisitions []PortableToolArtifactAcquisitionLockV1 `json:"acquisitions"`
}

type PortableToolReleaseManifestInputV1 struct {
	Scope    string
	Tool     string
	Manifest PortableToolSelectedRecordV1
}

type PortableToolReleaseManifestLockV1 struct {
	Scope    string                       `json:"scope"`
	Tool     string                       `json:"tool"`
	Manifest PortableToolSelectedRecordV1 `json:"manifest"`
}

// PortableToolArtifactAcquisitionInputV1 joins a verified acquisition result
// with the exact catalog records that authorized it. It is construction input,
// not persisted directly, so process-local operation IDs and failed-attempt
// details cannot accidentally become lock identity.
type PortableToolArtifactAcquisitionInputV1 struct {
	Scope      string
	Tool       string
	Artifact   PortableToolRecordReferenceV1
	Descriptor providerstore.ArtifactDescriptor
	Source     PortableToolSelectedRecordV1
	Provenance providerstore.AcquisitionProvenance
}

type PortableToolArtifactAcquisitionLockV1 struct {
	Scope      string                               `json:"scope"`
	Tool       string                               `json:"tool"`
	Artifact   PortableToolRecordReferenceV1        `json:"artifact"`
	Descriptor providerstore.ArtifactDescriptor     `json:"descriptor"`
	Source     PortableToolSelectedRecordV1         `json:"source"`
	Outcome    PortableToolAcquisitionOutcomeLockV1 `json:"outcome"`
}

// PortableToolAcquisitionOutcomeLockV1 keeps locator roles unambiguous. A
// network outcome names the successful declared locator, a verified-cache hit
// names none, and source-record provenance is retained only in the explicitly
// historical collection. Redirect targets are never persisted; only their
// sanitized count is retained.
type PortableToolAcquisitionOutcomeLockV1 struct {
	Kind                      string   `json:"kind"`
	SuccessfulDeclaredLocator string   `json:"successful_declared_locator,omitempty"`
	RedirectHops              string   `json:"redirect_hops"`
	HistoricalLocators        []string `json:"historical_locators"`
}

// BuildPortableToolLockV1 constructs a canonical lock without retaining
// process-local or unsanitized acquisition diagnostics.
func BuildPortableToolLockV1(
	plan PortableToolProviderDAGV1,
	releases []PortableToolReleaseManifestInputV1,
	inputs []PortableToolArtifactAcquisitionInputV1,
) (PortableToolLockV1, error) {
	if err := ValidatePortableToolProviderDAGV1(plan); err != nil {
		return PortableToolLockV1{}, fmt.Errorf("portable tool lock plan: %w", err)
	}
	if releases == nil || inputs == nil {
		return PortableToolLockV1{}, fmt.Errorf("portable tool release manifests and acquisition inputs must use explicit arrays")
	}
	lock := PortableToolLockV1{
		Schema:       PortableToolLockSchemaV1,
		Plan:         clonePortableToolLockPlanV1(plan),
		Releases:     make([]PortableToolReleaseManifestLockV1, 0, len(releases)),
		Acquisitions: make([]PortableToolArtifactAcquisitionLockV1, 0, len(inputs)),
	}
	for _, release := range releases {
		lock.Releases = append(lock.Releases, PortableToolReleaseManifestLockV1{
			Scope: release.Scope, Tool: release.Tool,
			Manifest: PortableToolSelectedRecordV1{
				Reference: release.Manifest.Reference,
				Record: CanonicalProviderData{
					Schema: release.Manifest.Record.Schema,
					Value:  clonePortableToolCanonicalObjectV1(release.Manifest.Record.Value),
				},
			},
		})
	}
	sort.Slice(lock.Releases, func(left int, right int) bool {
		return portableToolReleaseLockKeyV1(lock.Releases[left].Scope, lock.Releases[left].Tool) <
			portableToolReleaseLockKeyV1(lock.Releases[right].Scope, lock.Releases[right].Tool)
	})
	for index, input := range inputs {
		if input.Provenance.SourceID != input.Source.Reference.ID {
			return PortableToolLockV1{}, fmt.Errorf("portable tool acquisition %d provenance source does not match the authorizing source record", index)
		}
		historical, err := portableToolSourceStringArrayV1(input.Source, "provenance")
		if err != nil {
			return PortableToolLockV1{}, fmt.Errorf("portable tool acquisition %d: %w", index, err)
		}
		lock.Acquisitions = append(lock.Acquisitions, PortableToolArtifactAcquisitionLockV1{
			Scope: input.Scope, Tool: input.Tool, Artifact: input.Artifact,
			Descriptor: input.Descriptor,
			Source: PortableToolSelectedRecordV1{
				Reference: input.Source.Reference,
				Record: CanonicalProviderData{
					Schema: input.Source.Record.Schema,
					Value:  clonePortableToolCanonicalObjectV1(input.Source.Record.Value),
				},
			},
			Outcome: PortableToolAcquisitionOutcomeLockV1{
				Kind: input.Provenance.Outcome, SuccessfulDeclaredLocator: input.Provenance.SuccessfulMirror,
				RedirectHops: strconv.Itoa(input.Provenance.Redirects), HistoricalLocators: historical,
			},
		})
	}
	sort.Slice(lock.Acquisitions, func(left int, right int) bool {
		return comparePortableToolAcquisitionLocksV1(lock.Acquisitions[left], lock.Acquisitions[right]) < 0
	})
	if err := ValidatePortableToolLockV1(lock); err != nil {
		return PortableToolLockV1{}, err
	}
	return lock, nil
}

func ValidatePortableToolLockV1(lock PortableToolLockV1) error {
	if lock.Schema != PortableToolLockSchemaV1 {
		return fmt.Errorf("portable tool lock schema must be %q", PortableToolLockSchemaV1)
	}
	if err := ValidatePortableToolProviderDAGV1(lock.Plan); err != nil {
		return fmt.Errorf("portable tool lock plan: %w", err)
	}
	if err := validatePortableToolLockedCatalogRecordsV1(lock.Plan.PortableToolPlan); err != nil {
		return err
	}
	if lock.Releases == nil || lock.Acquisitions == nil {
		return fmt.Errorf("portable tool lock releases and acquisitions must use explicit arrays")
	}
	releases, err := validatePortableToolReleaseManifestLocksV1(lock.Plan.PortableToolPlan, lock.Releases)
	if err != nil {
		return err
	}
	expected := portableToolLockArtifactsV1(lock.Plan.PortableToolPlan)
	seen := make(map[string]struct{}, len(lock.Acquisitions))
	for index, acquisition := range lock.Acquisitions {
		if index > 0 && comparePortableToolAcquisitionLocksV1(lock.Acquisitions[index-1], acquisition) >= 0 {
			return fmt.Errorf("portable tool lock acquisitions must be unique and sorted by scope, tool, and artifact")
		}
		key := portableToolAcquisitionKeyV1(acquisition.Scope, acquisition.Tool, acquisition.Artifact)
		selected, exists := expected[key]
		if !exists {
			return fmt.Errorf("portable tool lock acquisition %d does not name a selected artifact", index)
		}
		if _, exists := seen[key]; exists {
			return fmt.Errorf("portable tool lock repeats acquisition for artifact %q", acquisition.Artifact.ID)
		}
		seen[key] = struct{}{}
		release := releases[portableToolReleaseLockKeyV1(acquisition.Scope, acquisition.Tool)]
		if err := validatePortableToolArtifactAcquisitionLockV1(acquisition, selected.entry, selected.artifact, release.Manifest, expected); err != nil {
			return fmt.Errorf("portable tool lock acquisition %d: %w", index, err)
		}
	}
	if len(seen) != len(expected) {
		return fmt.Errorf("portable tool lock must record one acquisition for every selected artifact")
	}
	if _, err := canonical.Marshal(lock); err != nil {
		return fmt.Errorf("portable tool lock canonical form: %w", err)
	}
	return nil
}

func validatePortableToolLockedCatalogRecordsV1(plan PortableToolPlanV1) error {
	for _, entry := range plan.Tools {
		categories := []struct {
			name    string
			records []PortableToolSelectedRecordV1
		}{
			{name: "binding contract", records: entry.Responsibilities.BindingContracts},
			{name: "binding artifact", records: entry.Responsibilities.BindingArtifacts},
			{name: "payload", records: entry.Responsibilities.Payloads},
			{name: "native package set", records: entry.Responsibilities.NativePackageSets},
		}
		for _, category := range categories {
			for _, selected := range category.records {
				if err := ValidatePortableToolCatalogRecordV1(selected.Record); err != nil {
					return fmt.Errorf("portable tool lock %s record %q: %w", category.name, selected.Reference.ID, err)
				}
			}
		}
		for _, profile := range entry.ValidationProfiles {
			if err := ValidatePortableToolCatalogRecordV1(profile.Record); err != nil {
				return fmt.Errorf("portable tool lock validation profile record %q: %w", profile.Reference.ID, err)
			}
		}
	}
	return nil
}

func CanonicalPortableToolLockBytesV1(lock PortableToolLockV1) ([]byte, error) {
	if err := ValidatePortableToolLockV1(lock); err != nil {
		return nil, err
	}
	return canonical.Marshal(lock)
}

// ClonePortableToolLockV1 returns an ownership-independent copy suitable for
// crossing the build-lock assembly boundary.
func ClonePortableToolLockV1(lock PortableToolLockV1) PortableToolLockV1 {
	result := lock
	result.Plan = clonePortableToolLockPlanV1(lock.Plan)
	result.Releases = append([]PortableToolReleaseManifestLockV1{}, lock.Releases...)
	for index := range result.Releases {
		result.Releases[index].Manifest.Record.Value = clonePortableToolCanonicalObjectV1(lock.Releases[index].Manifest.Record.Value)
	}
	result.Acquisitions = append([]PortableToolArtifactAcquisitionLockV1{}, lock.Acquisitions...)
	for index := range result.Acquisitions {
		result.Acquisitions[index].Source.Record.Value = clonePortableToolCanonicalObjectV1(lock.Acquisitions[index].Source.Record.Value)
		result.Acquisitions[index].Outcome.HistoricalLocators = append([]string{}, lock.Acquisitions[index].Outcome.HistoricalLocators...)
	}
	return result
}

func validatePortableToolReleaseManifestLocksV1(
	plan PortableToolPlanV1,
	releases []PortableToolReleaseManifestLockV1,
) (map[string]PortableToolReleaseManifestLockV1, error) {
	expected := make(map[string]PortableToolPlanEntryV1, len(plan.Tools))
	for _, entry := range plan.Tools {
		expected[portableToolReleaseLockKeyV1(entry.Scope, entry.Provenance.Tool)] = entry
	}
	result := make(map[string]PortableToolReleaseManifestLockV1, len(releases))
	for index, release := range releases {
		key := portableToolReleaseLockKeyV1(release.Scope, release.Tool)
		if index > 0 && portableToolReleaseLockKeyV1(releases[index-1].Scope, releases[index-1].Tool) >= key {
			return nil, fmt.Errorf("portable tool lock releases must be unique and sorted by scope and tool")
		}
		entry, exists := expected[key]
		if !exists {
			return nil, fmt.Errorf("portable tool lock release %d does not name a selected tool", index)
		}
		if err := validatePortableToolReleaseManifestLockV1(release, entry); err != nil {
			return nil, fmt.Errorf("portable tool lock release %d: %w", index, err)
		}
		result[key] = release
	}
	if len(result) != len(expected) {
		return nil, fmt.Errorf("portable tool lock must carry one release manifest for every selected tool")
	}
	return result, nil
}

func validatePortableToolReleaseManifestLockV1(
	release PortableToolReleaseManifestLockV1,
	entry PortableToolPlanEntryV1,
) error {
	namespace, err := portableToolReleaseNamespaceV1(entry.Provenance)
	if err != nil {
		return err
	}
	wantID := namespace + "/revisions/" + entry.Provenance.Revision + "/manifest"
	if release.Manifest.Reference.ID != wantID || release.Manifest.Reference.Digest != entry.Provenance.ManifestDigest {
		return fmt.Errorf("release manifest identity does not match selected release provenance")
	}
	if err := validatePortableToolRecordEnvelopeV1(
		"portable tool release manifest", PortableToolReleaseManifestRecordSchemaV1,
		release.Manifest.Reference, release.Manifest.Record,
	); err != nil {
		return err
	}
	if err := ValidatePortableToolCatalogRecordV1(release.Manifest.Record); err != nil {
		return fmt.Errorf("release manifest: %w", err)
	}
	value := release.Manifest.Record.Value
	if portableToolObjectStringV1(value, "tool") != entry.Provenance.Tool ||
		portableToolObjectStringV1(value, "version") != entry.Provenance.Version ||
		portableToolObjectStringV1(value, "revision") != entry.Provenance.Revision {
		return fmt.Errorf("release manifest fields do not match selected release provenance")
	}
	profileValues, err := portableToolObjectArrayV1(value, "validation_profiles")
	if err != nil {
		return fmt.Errorf("release manifest: %w", err)
	}
	declaredProfiles := make(map[PortableToolRecordReferenceV1]int, len(profileValues))
	for index, raw := range profileValues {
		profile, ok := portableToolObjectReferenceV1(raw)
		if !ok {
			return fmt.Errorf("release manifest validation profile %d must be a record reference", index)
		}
		declaredProfiles[profile]++
	}
	for _, profile := range entry.ValidationProfiles {
		if declaredProfiles[profile.Reference] != 1 {
			return fmt.Errorf("release manifest does not authorize selected validation profile %q", profile.Reference.ID)
		}
	}
	return nil
}

type portableToolLockArtifactV1 struct {
	entry    PortableToolPlanEntryV1
	artifact PortableToolSelectedRecordV1
}

func portableToolLockArtifactsV1(plan PortableToolPlanV1) map[string]portableToolLockArtifactV1 {
	result := make(map[string]portableToolLockArtifactV1)
	for _, entry := range plan.Tools {
		for _, artifact := range entry.Responsibilities.BindingArtifacts {
			result[portableToolAcquisitionKeyV1(entry.Scope, entry.Provenance.Tool, artifact.Reference)] = portableToolLockArtifactV1{entry: entry, artifact: artifact}
		}
		for _, artifact := range entry.Responsibilities.Payloads {
			result[portableToolAcquisitionKeyV1(entry.Scope, entry.Provenance.Tool, artifact.Reference)] = portableToolLockArtifactV1{entry: entry, artifact: artifact}
		}
	}
	return result
}

func validatePortableToolArtifactAcquisitionLockV1(
	acquisition PortableToolArtifactAcquisitionLockV1,
	entry PortableToolPlanEntryV1,
	artifact PortableToolSelectedRecordV1,
	manifest PortableToolSelectedRecordV1,
	selectedArtifacts map[string]portableToolLockArtifactV1,
) error {
	if err := acquisition.Descriptor.Validate(); err != nil {
		return fmt.Errorf("descriptor: %w", err)
	}
	if acquisition.Descriptor.SHA256 != portableToolObjectDigestV1(artifact.Record.Value, "sha256") ||
		acquisition.Descriptor.Size != portableToolObjectStringV1(artifact.Record.Value, "size") {
		return fmt.Errorf("descriptor does not match the selected artifact record")
	}
	if artifact.Record.Schema == portableToolPayloadSchemaV1 {
		if acquisition.Descriptor.LogicalPath != portableToolObjectStringV1(artifact.Record.Value, "logical_path") ||
			acquisition.Descriptor.Kind != portableToolObjectStringV1(artifact.Record.Value, "kind") {
			return fmt.Errorf("descriptor does not match the selected payload record")
		}
	} else if artifact.Record.Schema == portableToolBindingArtifactSchemaV1 {
		filename := portableToolObjectStringV1(artifact.Record.Value, "filename")
		if filename == "" || path.Base(acquisition.Descriptor.LogicalPath) != filename || acquisition.Descriptor.Kind != "wheel" {
			return fmt.Errorf("descriptor does not match the selected binding artifact record")
		}
	}
	releaseNamespace, err := portableToolReleaseNamespaceV1(entry.Provenance)
	if err != nil {
		return err
	}
	if err := validatePortableToolRecordReferenceV1(acquisition.Source.Reference); err != nil {
		return fmt.Errorf("source record: %w", err)
	}
	wantPrefix := releaseNamespace + "/revisions/" + entry.Provenance.Revision + "/sources/"
	sourceName, inRevision := strings.CutPrefix(acquisition.Source.Reference.ID, wantPrefix)
	if !inRevision || sourceName == "" {
		return fmt.Errorf("source record %q is outside selected release revision", acquisition.Source.Reference.ID)
	}
	if strings.Contains(sourceName, "/") {
		return fmt.Errorf("source record %q must use exactly one source name in the selected release revision", acquisition.Source.Reference.ID)
	}
	if err := validatePortableToolRecordEnvelopeV1(
		"portable tool acquisition source", PortableToolArtifactSourceRecordSchemaV1,
		acquisition.Source.Reference, acquisition.Source.Record,
	); err != nil {
		return err
	}
	if err := ValidatePortableToolCatalogRecordV1(acquisition.Source.Record); err != nil {
		return fmt.Errorf("source record: %w", err)
	}
	if portableToolObjectDigestV1(acquisition.Source.Record.Value, "sha256") != acquisition.Descriptor.SHA256 {
		return fmt.Errorf("source record does not authorize the selected artifact digest")
	}
	if err := validatePortableToolManifestSourceAuthorizationV1(manifest, acquisition, selectedArtifacts); err != nil {
		return err
	}
	mirrors, err := portableToolSourceStringArrayV1(acquisition.Source, "mirrors")
	if err != nil || len(mirrors) == 0 {
		return fmt.Errorf("source record mirrors must be a nonempty string array")
	}
	if err := providerstore.ValidateArtifactSource(providerstore.ArtifactSource{
		ID: acquisition.Source.Reference.ID, SHA256: acquisition.Descriptor.SHA256, Mirrors: mirrors,
	}, acquisition.Descriptor); err != nil {
		return fmt.Errorf("source record: %w", err)
	}
	historical, err := portableToolSourceStringArrayV1(acquisition.Source, "provenance")
	if err != nil {
		return err
	}
	if !portableToolStringsEqualV1(acquisition.Outcome.HistoricalLocators, historical) {
		return fmt.Errorf("historical locators do not match source-record provenance")
	}
	redirects, err := strconv.Atoi(acquisition.Outcome.RedirectHops)
	if err != nil || redirects < 0 || redirects > providerstore.CoreMaxArtifactRedirects || strconv.Itoa(redirects) != acquisition.Outcome.RedirectHops {
		return fmt.Errorf("redirect hops must be a canonical nonnegative decimal string no greater than %d", providerstore.CoreMaxArtifactRedirects)
	}
	artifactSize, err := strconv.ParseInt(acquisition.Descriptor.Size, 10, 64)
	if err != nil || artifactSize >= providerstore.CoreMaxArtifactBytesPerAttempt {
		return fmt.Errorf("acquisition artifact size must be representable and less than core per-attempt cap %d", providerstore.CoreMaxArtifactBytesPerAttempt)
	}
	switch acquisition.Outcome.Kind {
	case providerstore.AcquisitionOutcomeNetwork:
		if acquisition.Outcome.SuccessfulDeclaredLocator == "" || !portableToolContainsStringV1(mirrors, acquisition.Outcome.SuccessfulDeclaredLocator) {
			return fmt.Errorf("network acquisition must name a successful declared locator")
		}
	case providerstore.AcquisitionOutcomeCacheHit:
		if acquisition.Outcome.SuccessfulDeclaredLocator != "" || redirects != 0 {
			return fmt.Errorf("verified-cache acquisition must record that no locator was contacted")
		}
	default:
		return fmt.Errorf("acquisition outcome must be network or cache-hit")
	}
	return nil
}

func validatePortableToolManifestSourceAuthorizationV1(
	manifest PortableToolSelectedRecordV1,
	acquisition PortableToolArtifactAcquisitionLockV1,
	selectedArtifacts map[string]portableToolLockArtifactV1,
) error {
	values, err := portableToolObjectArrayV1(manifest.Record.Value, "artifact_sources")
	if err != nil {
		return fmt.Errorf("release manifest: %w", err)
	}
	matches := 0
	for index, raw := range values {
		mapping, ok := portableToolCanonicalMapV1(raw)
		if !ok {
			return fmt.Errorf("release manifest artifact source %d must be an object", index)
		}
		digest := portableToolObjectDigestV1(mapping, "artifact_sha256")
		artifact, artifactOK := portableToolObjectReferenceV1(mapping["artifact"])
		source, sourceOK := portableToolObjectReferenceV1(mapping["source"])
		if !artifactOK || !sourceOK || digest == "" {
			return fmt.Errorf("release manifest artifact source %d is incomplete", index)
		}
		if digest != acquisition.Descriptor.SHA256 || source != acquisition.Source.Reference {
			continue
		}
		selected, exists := selectedArtifacts[portableToolAcquisitionKeyV1(acquisition.Scope, acquisition.Tool, artifact)]
		if exists &&
			portableToolObjectDigestV1(selected.artifact.Record.Value, "sha256") == acquisition.Descriptor.SHA256 &&
			portableToolObjectStringV1(selected.artifact.Record.Value, "size") == acquisition.Descriptor.Size {
			matches++
		}
	}
	if matches != 1 {
		return fmt.Errorf("release manifest must authorize the selected content and source record once")
	}
	return nil
}

func portableToolAcquisitionKeyV1(scope, tool string, artifact PortableToolRecordReferenceV1) string {
	return scope + "\x00" + tool + "\x00" + artifact.ID + "\x00" + string(artifact.Digest)
}

func portableToolReleaseLockKeyV1(scope, tool string) string {
	return scope + "\x00" + tool
}

func clonePortableToolLockPlanV1(plan PortableToolProviderDAGV1) PortableToolProviderDAGV1 {
	result := plan
	result.ProviderPlan = cloneProviderPlanForPortableToolDAGV1(plan.ProviderPlan)
	result.PortableToolPlan = clonePortableToolPlanForPortableToolDAGV1(plan.PortableToolPlan)
	result.Domains = append([]PortableToolProviderDomainSetV1{}, plan.Domains...)
	result.Operations = append([]PortableToolProviderOperationV1{}, plan.Operations...)
	for index := range result.Operations {
		if plan.Operations[index].Record != nil {
			value := *plan.Operations[index].Record
			result.Operations[index].Record = &value
		}
		if plan.Operations[index].Environment != nil {
			value := *plan.Operations[index].Environment
			result.Operations[index].Environment = &value
		}
		if plan.Operations[index].Export != nil {
			value := *plan.Operations[index].Export
			result.Operations[index].Export = &value
		}
	}
	result.Dependencies = append([]PortableToolProviderDependencyV1{}, plan.Dependencies...)
	return result
}

func comparePortableToolAcquisitionLocksV1(left, right PortableToolArtifactAcquisitionLockV1) int {
	leftKey := portableToolAcquisitionKeyV1(left.Scope, left.Tool, left.Artifact)
	rightKey := portableToolAcquisitionKeyV1(right.Scope, right.Tool, right.Artifact)
	return strings.Compare(leftKey, rightKey)
}

func portableToolSourceStringArrayV1(source PortableToolSelectedRecordV1, field string) ([]string, error) {
	raw, exists := source.Record.Value[field]
	if !exists || raw == nil {
		return nil, fmt.Errorf("source record %s must use an explicit array", field)
	}
	values, ok := raw.([]any)
	if !ok {
		if stringsValue, ok := raw.([]string); ok {
			return append([]string{}, stringsValue...), nil
		}
		return nil, fmt.Errorf("source record %s must be a string array", field)
	}
	result := make([]string, len(values))
	for index, value := range values {
		text, ok := value.(string)
		if !ok || text == "" || strings.TrimSpace(text) != text {
			return nil, fmt.Errorf("source record %s must contain canonical nonempty strings", field)
		}
		result[index] = text
	}
	return result, nil
}

func portableToolObjectStringV1(value canonical.Object, field string) string {
	result, _ := value[field].(string)
	return result
}

func portableToolObjectDigestV1(value canonical.Object, field string) canonical.Digest {
	result, _ := value[field].(string)
	return canonical.Digest(result)
}

func portableToolObjectArrayV1(value canonical.Object, field string) ([]any, error) {
	raw, exists := value[field]
	if !exists || raw == nil {
		return nil, fmt.Errorf("%s must use an explicit array", field)
	}
	result, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("%s must use an array", field)
	}
	return result, nil
}

func portableToolCanonicalMapV1(value any) (canonical.Object, bool) {
	switch typed := value.(type) {
	case canonical.Object:
		return typed, true
	case map[string]any:
		return canonical.Object(typed), true
	default:
		return nil, false
	}
}

func portableToolObjectReferenceV1(value any) (PortableToolRecordReferenceV1, bool) {
	object, ok := portableToolCanonicalMapV1(value)
	if !ok {
		return PortableToolRecordReferenceV1{}, false
	}
	id, idOK := object["id"].(string)
	digest, digestOK := object["digest"].(string)
	result := PortableToolRecordReferenceV1{ID: id, Digest: canonical.Digest(digest)}
	if !idOK || !digestOK || validatePortableToolRecordReferenceV1(result) != nil {
		return PortableToolRecordReferenceV1{}, false
	}
	return result, true
}

func portableToolStringsEqualV1(left, right []string) bool {
	if left == nil || right == nil || len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func portableToolContainsStringV1(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
