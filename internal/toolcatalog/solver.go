package toolcatalog

import (
	"bytes"
	"fmt"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	pythonprovider "github.com/omry/reploy/internal/providers/python"
)

const (
	maxJointAssignmentStatesV1  = 1024
	operationSnapshotIdentityV1 = "portable-tool-operation-snapshot-v1"
)

// ProviderDomainSetV1 names the provider and destination authority domains
// visible from one canonical resolution scope. Equal names mean that two
// scopes genuinely share that authority; unequal names keep their constraints
// isolated.
type ProviderDomainSetV1 struct {
	Scope          string `json:"scope"`
	PackageManager string `json:"package_manager"`
	Filesystem     string `json:"filesystem"`
	Environment    string `json:"environment"`
	Exports        string `json:"exports"`
	Capabilities   string `json:"capabilities"`
}

// ReleaseCandidateSetV1 is the PTD-09 output for one canonical requirement
// group. PTD-10 orders these sets independently of request input order.
type ReleaseCandidateSetV1 struct {
	Group      CanonicalRequirementGroupV1
	Candidates []ReleaseCandidateV1
}

// ResolutionOperationInputsV1 carries the complete canonical inputs that must
// remain fixed after joint solving. The selected candidate constraints are
// added by the solver before the immutable snapshot is sealed.
type ResolutionOperationInputsV1 struct {
	Blueprint canonical.Envelope `json:"blueprint"`
	Reploy    canonical.Envelope `json:"reploy"`
	Platform  canonical.Envelope `json:"platform"`
	Catalog   canonical.Envelope `json:"catalog"`
}

// ImmutableOperationSnapshotV1 is the complete canonical operation payload.
// A string owns its bytes, so acquisition can carry it unchanged without
// aliasing maps supplied by the caller.
type ImmutableOperationSnapshotV1 struct {
	CanonicalJSON string           `json:"canonical_json"`
	Digest        canonical.Digest `json:"digest"`
}

type ReleaseProvenanceV1 struct {
	Tool           string           `json:"tool"`
	Version        string           `json:"version"`
	Revision       string           `json:"revision"`
	ManifestDigest canonical.Digest `json:"manifest_digest"`
}

// SelectedContractProjectionV1 contains only behavior selected from a release
// contract. Bindings is nil when no binding was selected, as required by the
// selected-closure identity schema.
type SelectedContractProjectionV1 struct {
	Context    string              `json:"context"`
	Bindings   []string            `json:"bindings"`
	Selections map[string][]string `json:"selections"`
	Runtime    *RecordRuntimeV1    `json:"runtime"`
	Exports    []ToolExportV1      `json:"exports"`
}

type SelectedTargetBindingV1 struct {
	Name        string              `json:"name"`
	Contract    RecordReferenceV1   `json:"contract"`
	Artifacts   []RecordReferenceV1 `json:"artifacts"`
	PackageSets []RecordReferenceV1 `json:"package_sets"`
	Exports     []ToolExportV1      `json:"exports"`
}

type SelectedTargetSelectionV1 struct {
	Dimension   string              `json:"dimension"`
	Value       string              `json:"value"`
	Payloads    []RecordReferenceV1 `json:"payloads"`
	PackageSets []RecordReferenceV1 `json:"package_sets"`
	Exports     []ToolExportV1      `json:"exports"`
}

type SelectedTargetProjectionV1 struct {
	Identity    TargetIdentityV1            `json:"identity"`
	PackageSets []RecordReferenceV1         `json:"package_sets"`
	Bindings    []SelectedTargetBindingV1   `json:"bindings"`
	Payloads    []RecordReferenceV1         `json:"payloads"`
	Selections  []SelectedTargetSelectionV1 `json:"selections"`
	Exports     []ToolExportV1              `json:"exports"`
}

type SelectedBindingContractRecordV1 struct {
	Reference RecordReferenceV1 `json:"reference"`
	Record    BindingContractV1 `json:"record"`
}

type SelectedBindingArtifactRecordV1 struct {
	Reference RecordReferenceV1       `json:"reference"`
	Record    BindingArtifactRecordV1 `json:"record"`
}

type SelectedPayloadRecordV1 struct {
	Reference RecordReferenceV1 `json:"reference"`
	Record    PayloadRecordV1   `json:"record"`
}

type SelectedPackageSetRecordV1 struct {
	Reference RecordReferenceV1  `json:"reference"`
	Record    NativePackageSetV1 `json:"record"`
}

type SelectedClosureRecordsV1 struct {
	BindingContracts []SelectedBindingContractRecordV1 `json:"binding_contracts"`
	BindingArtifacts []SelectedBindingArtifactRecordV1 `json:"binding_artifacts"`
	Payloads         []SelectedPayloadRecordV1         `json:"payloads"`
	PackageSets      []SelectedPackageSetRecordV1      `json:"package_sets"`
}

// SelectedClosureV1 owns every selected record and every validation record it
// returns. Validation records authorize support but remain outside Identity.
type SelectedClosureV1 struct {
	Scope      string                       `json:"scope"`
	Provenance ReleaseProvenanceV1          `json:"provenance"`
	Contract   SelectedContractProjectionV1 `json:"contract"`
	Target     SelectedTargetProjectionV1   `json:"target"`
	Records    SelectedClosureRecordsV1     `json:"records"`
	Fixture    IntegrationFixtureRecordV1   `json:"fixture"`
	Profiles   []ValidationProfileRecordV1  `json:"profiles"`
	Identity   canonical.Digest             `json:"identity"`
}

type JointResolutionV1 struct {
	Closures      []SelectedClosureV1          `json:"closures"`
	Snapshot      ImmutableOperationSnapshotV1 `json:"snapshot"`
	VisitedStates string                       `json:"visited_states"`
}

type orderedCandidateSetV1 struct {
	group      CanonicalRequirementGroupV1
	candidates []ReleaseCandidateV1
	groupBytes []byte
	domains    ProviderDomainSetV1
}

// ResolveSelectedClosuresV1 jointly solves PTD-09 candidate sets. Candidate
// enumeration and canonical group construction deliberately remain outside
// this method.
func (catalog *CatalogV1) ResolveSelectedClosuresV1(sets []ReleaseCandidateSetV1,
	domains []ProviderDomainSetV1, operation ResolutionOperationInputsV1) (JointResolutionV1, error) {
	ordered, err := catalog.prepareCandidateSetsV1(sets, domains)
	if err != nil {
		return JointResolutionV1{}, err
	}
	if err := validateOperationInputsV1(operation); err != nil {
		return JointResolutionV1{}, err
	}
	chosen, visited, err := catalog.solveCandidateSetsV1(ordered, maxJointAssignmentStatesV1)
	if err != nil {
		return JointResolutionV1{}, err
	}
	closures := make([]SelectedClosureV1, 0, len(chosen))
	for index, candidate := range chosen {
		closure, err := catalog.finalizeSelectedClosureV1(ordered[index].group, candidate)
		if err != nil {
			return JointResolutionV1{}, fmt.Errorf("finalize %s/%s: %w",
				ordered[index].group.Scope, ordered[index].group.Tool, err)
		}
		closures = append(closures, closure)
	}
	snapshot, err := buildOperationSnapshotV1(operation, ordered, chosen, closures)
	if err != nil {
		return JointResolutionV1{}, err
	}
	return JointResolutionV1{
		Closures: closures, Snapshot: snapshot, VisitedStates: strconv.Itoa(visited),
	}, nil
}

func validateOperationInputsV1(inputs ResolutionOperationInputsV1) error {
	for _, item := range []struct {
		name  string
		value canonical.Envelope
	}{
		{name: "blueprint", value: inputs.Blueprint},
		{name: "Reploy", value: inputs.Reploy},
		{name: "platform", value: inputs.Platform},
		{name: "catalog", value: inputs.Catalog},
	} {
		if item.value.Schema == "" || item.value.Value == nil {
			return fmt.Errorf("operation %s snapshot input must be complete", item.name)
		}
		if _, err := canonical.Marshal(item.value); err != nil {
			return fmt.Errorf("operation %s snapshot input: %w", item.name, err)
		}
	}
	return nil
}

func (catalog *CatalogV1) prepareCandidateSetsV1(sets []ReleaseCandidateSetV1,
	domains []ProviderDomainSetV1) ([]orderedCandidateSetV1, error) {
	if len(sets) == 0 {
		return nil, fmt.Errorf("joint tool resolution requires at least one candidate set")
	}
	domainsByScope := make(map[string]ProviderDomainSetV1, len(domains))
	for _, item := range domains {
		if item.Scope == "" || item.PackageManager == "" || item.Filesystem == "" ||
			item.Environment == "" || item.Exports == "" || item.Capabilities == "" {
			return nil, fmt.Errorf("provider domains for scope %q must name every authority domain", item.Scope)
		}
		if _, duplicate := domainsByScope[item.Scope]; duplicate {
			return nil, fmt.Errorf("provider domains repeat scope %q", item.Scope)
		}
		domainsByScope[item.Scope] = item
	}
	ordered := make([]orderedCandidateSetV1, 0, len(sets))
	seenGroups := make(map[string]struct{}, len(sets))
	for _, set := range sets {
		if err := validateCanonicalRequirementGroupV1(set.Group); err != nil {
			return nil, fmt.Errorf("candidate group %q/%q: %w", set.Group.Scope, set.Group.Tool, err)
		}
		groupKey := set.Group.Scope + "\x00" + set.Group.Tool
		if _, duplicate := seenGroups[groupKey]; duplicate {
			return nil, fmt.Errorf("candidate sets repeat canonical group %s/%s", set.Group.Scope, set.Group.Tool)
		}
		seenGroups[groupKey] = struct{}{}
		domainSet, exists := domainsByScope[set.Group.Scope]
		if !exists {
			return nil, fmt.Errorf("candidate group %s/%s has no provider-domain mapping",
				set.Group.Scope, set.Group.Tool)
		}
		if len(set.Candidates) == 0 {
			return nil, fmt.Errorf("candidate group %s/%s has no surviving candidate",
				set.Group.Scope, set.Group.Tool)
		}
		toolKey, exists := catalog.tools[set.Group.Tool]
		if !exists {
			return nil, fmt.Errorf("portable tool %q is not defined", set.Group.Tool)
		}
		tool, ok := catalog.records[toolKey].Value.(*ToolRecordV1)
		if !ok {
			return nil, fmt.Errorf("portable tool %q does not resolve to a tool record", set.Group.Tool)
		}
		candidates := append([]ReleaseCandidateV1{}, set.Candidates...)
		for _, candidate := range candidates {
			if candidate.Scope != set.Group.Scope || candidate.Manifest.Tool != set.Group.Tool {
				return nil, fmt.Errorf("candidate %q does not belong to canonical group %s/%s",
					candidate.Manifest.ID, set.Group.Scope, set.Group.Tool)
			}
		}
		sort.SliceStable(candidates, func(left int, right int) bool {
			versionOrder := compareToolVersionsV1(tool.VersionScheme,
				candidates[left].Manifest.Version, candidates[right].Manifest.Version)
			if versionOrder != 0 {
				return versionOrder > 0
			}
			revisionOrder := compareCanonicalDecimalV1(
				candidates[left].Manifest.Revision, candidates[right].Manifest.Revision)
			if revisionOrder != 0 {
				return revisionOrder > 0
			}
			return candidates[left].Manifest.ID < candidates[right].Manifest.ID
		})
		for index := 1; index < len(candidates); index++ {
			if candidates[index-1].Manifest.Version == candidates[index].Manifest.Version &&
				candidates[index-1].Manifest.Revision == candidates[index].Manifest.Revision {
				return nil, fmt.Errorf("candidate group %s/%s repeats release coordinate %s~%s",
					set.Group.Scope, set.Group.Tool, candidates[index].Manifest.Version,
					candidates[index].Manifest.Revision)
			}
		}
		groupBytes, err := canonical.Marshal(set.Group)
		if err != nil {
			return nil, fmt.Errorf("canonical candidate group %s/%s: %w", set.Group.Scope, set.Group.Tool, err)
		}
		ordered = append(ordered, orderedCandidateSetV1{
			group: set.Group, candidates: candidates, groupBytes: groupBytes, domains: domainSet,
		})
	}
	sort.SliceStable(ordered, func(left int, right int) bool {
		if ordered[left].group.Scope != ordered[right].group.Scope {
			return ordered[left].group.Scope < ordered[right].group.Scope
		}
		if ordered[left].group.Tool != ordered[right].group.Tool {
			return ordered[left].group.Tool < ordered[right].group.Tool
		}
		return bytes.Compare(ordered[left].groupBytes, ordered[right].groupBytes) < 0
	})
	return ordered, nil
}

func compareCanonicalDecimalV1(left string, right string) int {
	if len(left) != len(right) {
		if len(left) < len(right) {
			return -1
		}
		return 1
	}
	return strings.Compare(left, right)
}

func (catalog *CatalogV1) solveCandidateSetsV1(sets []orderedCandidateSetV1,
	limit int) ([]ReleaseCandidateV1, int, error) {
	if limit < 1 {
		return nil, 0, fmt.Errorf("joint assignment state cap must be positive")
	}
	selected := make([]ReleaseCandidateV1, 0, len(sets))
	visited := 0
	lastConflict := ""
	var search func(int) (bool, error)
	search = func(index int) (bool, error) {
		if index == len(sets) {
			return true, nil
		}
		for _, candidate := range sets[index].candidates {
			visited++
			if visited > limit {
				return false, fmt.Errorf("joint assignment visited-state cap %d exceeded before a complete assignment", limit)
			}
			selected = append(selected, candidate)
			conflict, err := catalog.assignmentConflictV1(sets[:index+1], selected)
			if err != nil {
				return false, err
			}
			if conflict == "" {
				complete, err := search(index + 1)
				if err != nil {
					return false, err
				}
				if complete {
					return true, nil
				}
			} else {
				lastConflict = conflict
			}
			selected = selected[:len(selected)-1]
		}
		return false, nil
	}
	complete, err := search(0)
	if err != nil {
		return nil, visited, err
	}
	if !complete {
		groups := make([]string, 0, len(sets))
		for _, set := range sets {
			groups = append(groups, set.group.Scope+"/"+set.group.Tool)
		}
		if lastConflict == "" {
			lastConflict = "every candidate combination was incompatible"
		}
		return nil, visited, fmt.Errorf("no complete assignment for incompatible requirements %s: %s",
			strings.Join(groups, ", "), lastConflict)
	}
	return append([]ReleaseCandidateV1{}, selected...), visited, nil
}

type semanticClaimV1 struct {
	owner string
	value string
}

type ownedPathClaimV1 struct {
	owner string
	path  string
	value string
}

type assignmentClaimsV1 struct {
	semantic map[string]semanticClaimV1
	paths    map[string][]ownedPathClaimV1
}

func (catalog *CatalogV1) assignmentConflictV1(sets []orderedCandidateSetV1,
	candidates []ReleaseCandidateV1) (string, error) {
	claims := assignmentClaimsV1{
		semantic: make(map[string]semanticClaimV1), paths: make(map[string][]ownedPathClaimV1),
	}
	for index, candidate := range candidates {
		owner := sets[index].group.Scope + "/" + sets[index].group.Tool + "@" +
			candidate.Manifest.Version + "~" + candidate.Manifest.Revision
		if conflict, err := catalog.addCandidateClaimsV1(&claims, sets[index].domains, owner, candidate); err != nil {
			return "", err
		} else if conflict != "" {
			return conflict, nil
		}
	}
	return "", nil
}

func addSemanticClaimV1(claims *assignmentClaimsV1, kind string, domain string,
	key string, value string, owner string) string {
	claimKey := kind + "\x00" + domain + "\x00" + key
	previous, exists := claims.semantic[claimKey]
	if exists && previous.value != value {
		return fmt.Sprintf("%s conflict in domain %q on %q between %s and %s",
			kind, domain, key, previous.owner, owner)
	}
	if !exists {
		claims.semantic[claimKey] = semanticClaimV1{owner: owner, value: value}
	}
	return ""
}

func addOwnedPathClaimV1(claims *assignmentClaimsV1, domain string, claimedPath string,
	value string, owner string) string {
	cleaned := path.Clean(claimedPath)
	duplicate := false
	for _, previous := range claims.paths[domain] {
		if !pathsOverlapV1(previous.path, cleaned) {
			continue
		}
		if previous.path == cleaned && previous.value == value {
			duplicate = true
			continue
		}
		return fmt.Sprintf("filesystem conflict in domain %q between %s path %q and %s path %q",
			domain, previous.owner, previous.path, owner, cleaned)
	}
	if duplicate {
		return ""
	}
	claims.paths[domain] = append(claims.paths[domain], ownedPathClaimV1{
		owner: owner, path: cleaned, value: value,
	})
	return ""
}

func pathsOverlapV1(left string, right string) bool {
	if left == right {
		return true
	}
	return strings.HasPrefix(left, right+"/") || strings.HasPrefix(right, left+"/")
}

func (catalog *CatalogV1) addCandidateClaimsV1(claims *assignmentClaimsV1,
	domains ProviderDomainSetV1, owner string, candidate ReleaseCandidateV1) (string, error) {
	if candidate.Contract.Runtime != nil {
		if candidate.Contract.Runtime.InstallRoot != "" {
			if conflict := addOwnedPathClaimV1(claims, domains.Filesystem,
				candidate.Contract.Runtime.InstallRoot, candidate.Contract.Runtime.InstallRoot,
				owner); conflict != "" {
				return conflict, nil
			}
		}
		for _, variable := range candidate.Contract.Runtime.Environment {
			if conflict := addSemanticClaimV1(claims, "environment", domains.Environment,
				variable.Name, variable.Value, owner); conflict != "" {
				return conflict, nil
			}
		}
	}
	for _, exported := range candidate.Exports {
		if conflict := addSemanticClaimV1(claims, "export", domains.Exports,
			exported.Name, exported.Path, owner); conflict != "" {
			return conflict, nil
		}
		if conflict := addSemanticClaimV1(claims, "capability", domains.Capabilities,
			exported.Name, exported.Path, owner); conflict != "" {
			return conflict, nil
		}
	}
	for _, reference := range candidate.Contributions {
		record, err := catalog.exactRecordV1(reference)
		if err != nil {
			return "", err
		}
		value := string(reference.Digest)
		switch selected := record.Value.(type) {
		case *NativePackageSetV1:
			for _, requirement := range selected.Requirements {
				packageName := requirement
				if selected.Manager == "apt" {
					parsed, err := blueprint.ParseAPTPackageRequest(requirement)
					if err != nil {
						return "", err
					}
					packageName = parsed.Name
				}
				if conflict := addSemanticClaimV1(claims, "package requirement",
					domains.PackageManager, selected.Manager+"/"+packageName, requirement, owner); conflict != "" {
					return conflict, nil
				}
			}
			for _, repository := range selected.Repositories {
				if conflict := addSemanticClaimV1(claims, "package repository requirement",
					domains.PackageManager, selected.Manager+"/"+repository, repository, owner); conflict != "" {
					return conflict, nil
				}
			}
		case *PayloadRecordV1:
			if conflict := addSemanticClaimV1(claims, "artifact logical path", domains.Filesystem,
				selected.LogicalPath, value, owner); conflict != "" {
				return conflict, nil
			}
			if conflict := addOwnedPathClaimV1(claims, domains.Filesystem,
				selected.InstallDirectory, value, owner); conflict != "" {
				return conflict, nil
			}
		case *BindingArtifactRecordV1:
			if conflict := addSemanticClaimV1(claims, "artifact logical path", domains.Filesystem,
				selected.Filename, value, owner); conflict != "" {
				return conflict, nil
			}
		case *BindingContractV1:
			// Binding requirements are ecosystem-provider constraints. The
			// package-manager domain is the shared provider authority available
			// in the current record model.
			for _, requirement := range selected.Requirements {
				distribution, err := pythonprovider.PackageRootDistributionNameV1(requirement)
				if err != nil {
					return "", err
				}
				if conflict := addSemanticClaimV1(claims, "binding requirement",
					domains.PackageManager, selected.Name+"/"+distribution, requirement, owner); conflict != "" {
					return conflict, nil
				}
			}
		}
	}
	return "", nil
}

func (catalog *CatalogV1) exactRecordV1(reference RecordReferenceV1) (loadedRecordV1, error) {
	record, exists := catalog.records[recordKeyV1{ID: reference.ID, Digest: reference.Digest}]
	if !exists || record.ID != reference.ID || record.Digest != reference.Digest {
		return loadedRecordV1{}, fmt.Errorf("reference %q does not resolve to its exact record", reference.ID)
	}
	return record, nil
}

func (catalog *CatalogV1) finalizeSelectedClosureV1(group CanonicalRequirementGroupV1,
	candidate ReleaseCandidateV1) (SelectedClosureV1, error) {
	contract := SelectedContractProjectionV1{
		Context: group.Context, Selections: cloneSelectionMapV1(candidate.Selections),
		Runtime: cloneRuntimeV1(candidate.Contract.Runtime),
		Exports: append([]ToolExportV1{}, candidate.Contract.Exports...),
	}
	if len(candidate.Bindings) != 0 {
		contract.Bindings = append([]string{}, candidate.Bindings...)
	}
	if contract.Selections == nil {
		contract.Selections = map[string][]string{}
	}
	target := selectedTargetProjectionV1(candidate)
	records, err := catalog.selectedClosureRecordsV1(candidate.Contributions)
	if err != nil {
		return SelectedClosureV1{}, err
	}
	identityInput := struct {
		Tool     string                       `json:"tool"`
		Version  string                       `json:"version"`
		Contract SelectedContractProjectionV1 `json:"contract"`
		Target   SelectedTargetProjectionV1   `json:"target"`
		Records  []RecordReferenceV1          `json:"records"`
	}{
		Tool: group.Tool, Version: candidate.Manifest.Version, Contract: contract,
		Target: target, Records: canonicalReferenceUnionV1(candidate.Contributions),
	}
	identity, err := canonical.Sum("portable-tool-selected-closure", SelectedClosureIdentityV1, identityInput)
	if err != nil {
		return SelectedClosureV1{}, fmt.Errorf("selected-closure identity: %w", err)
	}
	manifestDigest, err := canonical.Sum("portable-tool-record", portableToolRecordIdentityV1, candidate.Manifest)
	if err != nil {
		return SelectedClosureV1{}, fmt.Errorf("release provenance: %w", err)
	}
	profiles := make([]ValidationProfileRecordV1, 0, len(candidate.Profiles))
	for index := range candidate.Profiles {
		profiles = append(profiles, cloneValidationProfileV1(&candidate.Profiles[index]))
	}
	return SelectedClosureV1{
		Scope: group.Scope,
		Provenance: ReleaseProvenanceV1{
			Tool: group.Tool, Version: candidate.Manifest.Version, Revision: candidate.Manifest.Revision,
			ManifestDigest: manifestDigest,
		},
		Contract: contract, Target: target, Records: records,
		Fixture: cloneIntegrationFixtureV1(&candidate.Fixture), Profiles: profiles, Identity: identity,
	}, nil
}

func selectedTargetProjectionV1(candidate ReleaseCandidateV1) SelectedTargetProjectionV1 {
	result := SelectedTargetProjectionV1{
		Identity:    candidate.Target.Target,
		PackageSets: append([]RecordReferenceV1{}, candidate.Target.PackageSets...),
		Bindings:    make([]SelectedTargetBindingV1, 0, len(candidate.Bindings)),
		Payloads:    append([]RecordReferenceV1{}, candidate.Target.Payloads...),
		Selections:  []SelectedTargetSelectionV1{},
		Exports:     append([]ToolExportV1{}, candidate.Target.Exports...),
	}
	for _, name := range candidate.Bindings {
		binding, found := targetBindingEntryV1(&candidate.Target, name)
		if !found {
			continue
		}
		result.Bindings = append(result.Bindings, SelectedTargetBindingV1{
			Name: name, Contract: binding.Contract,
			Artifacts:   append([]RecordReferenceV1{}, binding.Artifacts...),
			PackageSets: append([]RecordReferenceV1{}, binding.PackageSets...),
			Exports:     append([]ToolExportV1{}, binding.Exports...),
		})
	}
	dimensions := make([]string, 0, len(candidate.Selections))
	for dimension := range candidate.Selections {
		dimensions = append(dimensions, dimension)
	}
	sort.Strings(dimensions)
	for _, dimension := range dimensions {
		for _, value := range candidate.Selections[dimension] {
			for _, selected := range candidate.Target.Selections {
				if selected.Dimension != dimension || selected.Value != value {
					continue
				}
				result.Selections = append(result.Selections, SelectedTargetSelectionV1{
					Dimension: dimension, Value: value,
					Payloads:    append([]RecordReferenceV1{}, selected.Payloads...),
					PackageSets: append([]RecordReferenceV1{}, selected.PackageSets...),
					Exports:     append([]ToolExportV1{}, selected.Exports...),
				})
				break
			}
		}
	}
	return result
}

func (catalog *CatalogV1) selectedClosureRecordsV1(references []RecordReferenceV1) (SelectedClosureRecordsV1, error) {
	result := SelectedClosureRecordsV1{
		BindingContracts: []SelectedBindingContractRecordV1{},
		BindingArtifacts: []SelectedBindingArtifactRecordV1{},
		Payloads:         []SelectedPayloadRecordV1{}, PackageSets: []SelectedPackageSetRecordV1{},
	}
	for _, reference := range canonicalReferenceUnionV1(references) {
		record, err := catalog.exactRecordV1(reference)
		if err != nil {
			return SelectedClosureRecordsV1{}, err
		}
		switch value := record.Value.(type) {
		case *BindingContractV1:
			result.BindingContracts = append(result.BindingContracts, SelectedBindingContractRecordV1{
				Reference: reference, Record: cloneBindingContractV1(value),
			})
		case *BindingArtifactRecordV1:
			result.BindingArtifacts = append(result.BindingArtifacts, SelectedBindingArtifactRecordV1{
				Reference: reference, Record: cloneBindingArtifactV1(value),
			})
		case *PayloadRecordV1:
			result.Payloads = append(result.Payloads, SelectedPayloadRecordV1{
				Reference: reference, Record: clonePayloadRecordV1(value),
			})
		case *NativePackageSetV1:
			result.PackageSets = append(result.PackageSets, SelectedPackageSetRecordV1{
				Reference: reference, Record: cloneNativePackageSetV1(value),
			})
		default:
			return SelectedClosureRecordsV1{}, fmt.Errorf("selected contribution %q has unsupported record type %T",
				reference.ID, record.Value)
		}
	}
	return result, nil
}

func buildOperationSnapshotV1(inputs ResolutionOperationInputsV1, sets []orderedCandidateSetV1,
	candidates []ReleaseCandidateV1, closures []SelectedClosureV1) (ImmutableOperationSnapshotV1, error) {
	type selectedConstraintsV1 struct {
		Group      CanonicalRequirementGroupV1  `json:"group"`
		Domains    ProviderDomainSetV1          `json:"domains"`
		Provenance ReleaseProvenanceV1          `json:"provenance"`
		Contract   SelectedContractProjectionV1 `json:"contract"`
		Target     SelectedTargetProjectionV1   `json:"target"`
		Records    SelectedClosureRecordsV1     `json:"records"`
	}
	selected := make([]selectedConstraintsV1, 0, len(candidates))
	for index := range candidates {
		selected = append(selected, selectedConstraintsV1{
			Group: sets[index].group, Domains: sets[index].domains, Provenance: closures[index].Provenance,
			Contract: closures[index].Contract, Target: closures[index].Target,
			Records: closures[index].Records,
		})
	}
	payload := struct {
		Inputs      ResolutionOperationInputsV1 `json:"inputs"`
		Constraints []selectedConstraintsV1     `json:"constraints"`
	}{Inputs: inputs, Constraints: selected}
	encoded, err := canonical.Marshal(payload)
	if err != nil {
		return ImmutableOperationSnapshotV1{}, fmt.Errorf("operation snapshot: %w", err)
	}
	digest, err := canonical.Sum("portable-tool-operation-snapshot", operationSnapshotIdentityV1, payload)
	if err != nil {
		return ImmutableOperationSnapshotV1{}, fmt.Errorf("operation snapshot identity: %w", err)
	}
	return ImmutableOperationSnapshotV1{CanonicalJSON: string(encoded), Digest: digest}, nil
}
