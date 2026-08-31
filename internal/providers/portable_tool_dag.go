package providers

import (
	"bytes"
	"fmt"
	"path"
	"reflect"
	"sort"
	"strings"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
)

const (
	// PortableToolProviderDAGSchemaV1 identifies the composite plan that binds
	// the existing provider DAG to provider-neutral portable-tool work.
	PortableToolProviderDAGSchemaV1 = "portable-tool-provider-dag-v1"

	PortableToolOperationBindingContractV1                = "binding-contract"
	PortableToolOperationBindingArtifactAcquisitionV1     = "binding-artifact-acquisition"
	PortableToolOperationBindingArtifactMaterializationV1 = "binding-artifact-materialization"
	PortableToolOperationPayloadAcquisitionV1             = "payload-acquisition"
	PortableToolOperationPayloadMaterializationV1         = "payload-materialization"
	PortableToolOperationNativePackageSetV1               = "native-package-set"
	PortableToolOperationRuntimeInstallRootV1             = "runtime-install-root"
	PortableToolOperationRuntimeEnvironmentV1             = "runtime-environment"
	PortableToolOperationExportV1                         = "export"
	PortableToolOperationCapabilityV1                     = "capability"
	PortableToolOperationAcquisitionBarrierV1             = "acquisition-barrier"
)

// PortableToolDomainAuthorityV1 names one stable authority and the generic
// provider node that owns it. Equal IDs deliberately express shared authority
// between scopes, but a shared ID cannot be assigned to different owners.
type PortableToolDomainAuthorityV1 struct {
	ID    string `json:"id"`
	Owner NodeID `json:"owner"`
}

// PortableToolProviderDomainSetV1 names the authority domains used by one
// selected resolution scope. Equal identities deliberately express shared
// authority between scopes; unequal identities isolate their responsibilities.
type PortableToolProviderDomainSetV1 struct {
	Scope          string                        `json:"scope"`
	PackageManager PortableToolDomainAuthorityV1 `json:"package_manager"`
	Binding        PortableToolDomainAuthorityV1 `json:"binding"`
	Filesystem     PortableToolDomainAuthorityV1 `json:"filesystem"`
	Environment    PortableToolDomainAuthorityV1 `json:"environment"`
	Exports        PortableToolDomainAuthorityV1 `json:"exports"`
	Capabilities   PortableToolDomainAuthorityV1 `json:"capabilities"`
}

// PortableToolProviderOperationV1 is one deterministic, provider-neutral
// responsibility. Record-backed work carries the selected record reference;
// runtime and export work carry their typed projection. Acquisition is kept
// distinct from materialization, and materialization always has Network set
// to NetworkPolicyNone.
type PortableToolProviderOperationV1 struct {
	ID          string                             `json:"id"`
	Scope       string                             `json:"scope"`
	Tool        string                             `json:"tool"`
	Kind        string                             `json:"kind"`
	Domain      string                             `json:"domain"`
	Owner       NodeID                             `json:"owner,omitempty"`
	Record      *PortableToolRecordReferenceV1     `json:"record,omitempty"`
	InstallRoot string                             `json:"install_root,omitempty"`
	Environment *PortableToolEnvironmentVariableV1 `json:"environment,omitempty"`
	Export      *PortableToolExportV1              `json:"export,omitempty"`
	Network     NetworkPolicy                      `json:"network,omitempty"`
}

// PortableToolProviderDependencyV1 is a structural edge between portable
// operations. Prerequisite must complete before Dependent may begin.
type PortableToolProviderDependencyV1 struct {
	Prerequisite string `json:"prerequisite"`
	Dependent    string `json:"dependent"`
}

// PortableToolProviderDAGV1 carries the existing provider plan unchanged in
// meaning, the compiled portable-tool plan, explicit authority domains, and
// the deterministic portable operation DAG.
type PortableToolProviderDAGV1 struct {
	Schema           string                             `json:"schema"`
	ProviderPlan     ProviderPlanV1                     `json:"provider_plan"`
	PortableToolPlan PortableToolPlanV1                 `json:"portable_tool_plan"`
	Domains          []PortableToolProviderDomainSetV1  `json:"domains"`
	Operations       []PortableToolProviderOperationV1  `json:"operations"`
	Dependencies     []PortableToolProviderDependencyV1 `json:"dependencies"`
}

type portableToolFilesystemClaimV1 struct {
	path  string
	value string
	kind  string
	owner string
}

// BuildPortableToolProviderDAGV1 composes a validated provider plan and a
// validated portable-tool plan. It does not execute, acquire, materialize, or
// persist any operation; it only builds and validates the structural graph.
func BuildPortableToolProviderDAGV1(
	providerPlan ProviderPlanV1,
	portableToolPlan PortableToolPlanV1,
	domains []PortableToolProviderDomainSetV1,
) (PortableToolProviderDAGV1, error) {
	if err := ValidateProviderPlanV1(providerPlan); err != nil {
		return PortableToolProviderDAGV1{}, fmt.Errorf("provider plan: %w", err)
	}
	if err := ValidatePortableToolPlanV1(portableToolPlan); err != nil {
		return PortableToolProviderDAGV1{}, fmt.Errorf("portable tool plan: %w", err)
	}
	orderedDomains, domainByScope, err := preparePortableToolProviderDomainsV1(providerPlan, portableToolPlan, domains)
	if err != nil {
		return PortableToolProviderDAGV1{}, err
	}
	operations, dependencies := expectedPortableToolProviderGraphV1(portableToolPlan, domainByScope)
	dag := PortableToolProviderDAGV1{
		Schema:           PortableToolProviderDAGSchemaV1,
		ProviderPlan:     cloneProviderPlanForPortableToolDAGV1(providerPlan),
		PortableToolPlan: clonePortableToolPlanForPortableToolDAGV1(portableToolPlan),
		Domains:          orderedDomains,
		Operations:       operations,
		Dependencies:     dependencies,
	}
	if err := ValidatePortableToolProviderDAGV1(dag); err != nil {
		return PortableToolProviderDAGV1{}, fmt.Errorf("validate portable tool provider DAG: %w", err)
	}
	return dag, nil
}

// ValidatePortableToolProviderDAGV1 validates the composite plan without
// normalizing caller-owned data. ProviderPlanV1 and PortableToolPlanV1 remain
// authoritative for their respective subtrees.
func ValidatePortableToolProviderDAGV1(dag PortableToolProviderDAGV1) error {
	if dag.Schema != PortableToolProviderDAGSchemaV1 {
		return fmt.Errorf("portable tool provider DAG schema must be %q", PortableToolProviderDAGSchemaV1)
	}
	if err := ValidateProviderPlanV1(dag.ProviderPlan); err != nil {
		return fmt.Errorf("provider plan: %w", err)
	}
	if err := ValidatePortableToolPlanV1(dag.PortableToolPlan); err != nil {
		return fmt.Errorf("portable tool plan: %w", err)
	}
	orderedDomains, domainByScope, err := preparePortableToolProviderDomainsV1(dag.ProviderPlan, dag.PortableToolPlan, dag.Domains)
	if err != nil {
		return err
	}
	if !portableToolProviderDomainsEqualV1(orderedDomains, dag.Domains) {
		return fmt.Errorf("portable tool provider domains must be unique and sorted by scope")
	}
	if dag.Operations == nil || dag.Dependencies == nil {
		return fmt.Errorf("portable tool provider operations and dependencies must use arrays")
	}
	expectedOperations, expectedDependencies := expectedPortableToolProviderGraphV1(dag.PortableToolPlan, domainByScope)
	if err := validatePortableToolProviderOperationsV1(dag.Operations, expectedOperations); err != nil {
		return err
	}
	if err := validatePortableToolProviderDependenciesV1(dag.Operations, dag.Dependencies, expectedDependencies); err != nil {
		return err
	}
	if err := validatePortableToolProviderSharedClaimsV1(dag.PortableToolPlan, dag.Operations); err != nil {
		return err
	}
	if _, err := canonical.Marshal(dag); err != nil {
		return fmt.Errorf("portable tool provider DAG canonical form: %w", err)
	}
	return nil
}

// CanonicalPortableToolProviderDAGBytesV1 validates the composite graph and
// returns its deterministic canonical-json-v1 representation.
func CanonicalPortableToolProviderDAGBytesV1(dag PortableToolProviderDAGV1) ([]byte, error) {
	if err := ValidatePortableToolProviderDAGV1(dag); err != nil {
		return nil, err
	}
	encoded, err := canonical.Marshal(dag)
	if err != nil {
		return nil, fmt.Errorf("portable tool provider DAG canonical form: %w", err)
	}
	return encoded, nil
}

func preparePortableToolProviderDomainsV1(
	providerPlan ProviderPlanV1,
	plan PortableToolPlanV1,
	domains []PortableToolProviderDomainSetV1,
) ([]PortableToolProviderDomainSetV1, map[string]PortableToolProviderDomainSetV1, error) {
	if domains == nil {
		return nil, nil, fmt.Errorf("portable tool provider domains must use an explicit array")
	}
	ordered := append([]PortableToolProviderDomainSetV1{}, domains...)
	sort.Slice(ordered, func(left int, right int) bool { return ordered[left].Scope < ordered[right].Scope })
	selectedScopes := make(map[string]struct{}, len(plan.Tools))
	for _, entry := range plan.Tools {
		selectedScopes[entry.Scope] = struct{}{}
	}
	if len(ordered) != len(selectedScopes) {
		return nil, nil, fmt.Errorf("portable tool provider domains must map every selected scope exactly once")
	}
	providerNodes := make(map[NodeID]struct{}, len(providerPlan.Nodes))
	for _, node := range providerPlan.Nodes {
		providerNodes[node.ID] = struct{}{}
	}
	result := make(map[string]PortableToolProviderDomainSetV1, len(ordered))
	ownersByID := make(map[string]NodeID)
	for index, domain := range ordered {
		if index > 0 && ordered[index-1].Scope >= domain.Scope {
			return nil, nil, fmt.Errorf("portable tool provider domains must be unique and sorted by scope")
		}
		if _, exists := selectedScopes[domain.Scope]; !exists {
			return nil, nil, fmt.Errorf("portable tool provider domain scope %q is not selected", domain.Scope)
		}
		if err := validatePortableToolOpaqueDomainV1("portable tool provider domain scope", domain.Scope); err != nil {
			return nil, nil, err
		}
		authorities := []struct {
			field     string
			authority PortableToolDomainAuthorityV1
		}{
			{field: "package manager", authority: domain.PackageManager},
			{field: "binding", authority: domain.Binding},
			{field: "filesystem", authority: domain.Filesystem},
			{field: "environment", authority: domain.Environment},
			{field: "exports", authority: domain.Exports},
			{field: "capabilities", authority: domain.Capabilities},
		}
		for _, item := range authorities {
			if err := validatePortableToolOpaqueDomainV1("portable tool provider "+item.field+" domain", item.authority.ID); err != nil {
				return nil, nil, err
			}
			if item.authority.Owner == "" {
				return nil, nil, fmt.Errorf("portable tool provider %s domain owner is required", item.field)
			}
			if _, exists := providerNodes[item.authority.Owner]; !exists {
				return nil, nil, fmt.Errorf("portable tool provider %s domain owner %q is unknown", item.field, item.authority.Owner)
			}
			if previous, exists := ownersByID[item.authority.ID]; exists && previous != item.authority.Owner {
				return nil, nil, fmt.Errorf("portable tool provider domain ID %q has conflicting owners %q and %q", item.authority.ID, previous, item.authority.Owner)
			}
			ownersByID[item.authority.ID] = item.authority.Owner
		}
		result[domain.Scope] = domain
	}
	return ordered, result, nil
}

func validatePortableToolOpaqueDomainV1(field string, value string) error {
	if value == "" || strings.TrimSpace(value) != value || containsPortableControl(value) {
		return fmt.Errorf("%s must be a nonempty stable text identity", field)
	}
	return nil
}

func portableToolProviderDomainsEqualV1(left, right []PortableToolProviderDomainSetV1) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func expectedPortableToolProviderGraphV1(
	plan PortableToolPlanV1,
	domainByScope map[string]PortableToolProviderDomainSetV1,
) ([]PortableToolProviderOperationV1, []PortableToolProviderDependencyV1) {
	operations := make([]PortableToolProviderOperationV1, 0)
	acquisitionIDs := make([]string, 0)
	materializationIDs := make([]string, 0)
	for _, entry := range plan.Tools {
		domain := domainByScope[entry.Scope]
		tool := entry.Provenance.Tool
		for _, selected := range entry.Responsibilities.BindingContracts {
			operations = append(operations, portableToolRecordOperationV1(entry, selected.Reference, PortableToolOperationBindingContractV1, domain.Binding))
		}
		for _, selected := range entry.Responsibilities.BindingArtifacts {
			acquire := portableToolRecordOperationV1(entry, selected.Reference, PortableToolOperationBindingArtifactAcquisitionV1, domain.Binding)
			materialize := portableToolRecordOperationV1(entry, selected.Reference, PortableToolOperationBindingArtifactMaterializationV1, domain.Filesystem)
			operations = append(operations, acquire, materialize)
			acquisitionIDs = append(acquisitionIDs, acquire.ID)
			materializationIDs = append(materializationIDs, materialize.ID)
		}
		for _, selected := range entry.Responsibilities.Payloads {
			acquire := portableToolRecordOperationV1(entry, selected.Reference, PortableToolOperationPayloadAcquisitionV1, domain.Filesystem)
			materialize := portableToolRecordOperationV1(entry, selected.Reference, PortableToolOperationPayloadMaterializationV1, domain.Filesystem)
			operations = append(operations, acquire, materialize)
			acquisitionIDs = append(acquisitionIDs, acquire.ID)
			materializationIDs = append(materializationIDs, materialize.ID)
		}
		for _, selected := range entry.Responsibilities.NativePackageSets {
			operations = append(operations, portableToolRecordOperationV1(entry, selected.Reference, PortableToolOperationNativePackageSetV1, domain.PackageManager))
		}
		if entry.Runtime != nil {
			operations = append(operations, PortableToolProviderOperationV1{
				ID:    portableToolOperationIDV1(entry.Scope, tool, PortableToolOperationRuntimeInstallRootV1, entry.Runtime.InstallRoot),
				Scope: entry.Scope, Tool: tool, Kind: PortableToolOperationRuntimeInstallRootV1,
				Domain: domain.Filesystem.ID, Owner: domain.Filesystem.Owner, InstallRoot: entry.Runtime.InstallRoot,
			})
			for _, variable := range entry.Runtime.Environment {
				value := variable
				operations = append(operations, PortableToolProviderOperationV1{
					ID:    portableToolOperationIDV1(entry.Scope, tool, PortableToolOperationRuntimeEnvironmentV1, variable.Name),
					Scope: entry.Scope, Tool: tool, Kind: PortableToolOperationRuntimeEnvironmentV1,
					Domain: domain.Environment.ID, Owner: domain.Environment.Owner, Environment: &value,
				})
			}
		}
		for _, exported := range entry.Exports {
			value := exported
			operations = append(operations, PortableToolProviderOperationV1{
				ID:    portableToolOperationIDV1(entry.Scope, tool, PortableToolOperationExportV1, exported.Name),
				Scope: entry.Scope, Tool: tool, Kind: PortableToolOperationExportV1,
				Domain: domain.Exports.ID, Owner: domain.Exports.Owner, Export: &value,
			})
			capability := exported
			operations = append(operations, PortableToolProviderOperationV1{
				ID:    portableToolOperationIDV1(entry.Scope, tool, PortableToolOperationCapabilityV1, exported.Name),
				Scope: entry.Scope, Tool: tool, Kind: PortableToolOperationCapabilityV1,
				Domain: domain.Capabilities.ID, Owner: domain.Capabilities.Owner, Export: &capability,
			})
		}
	}
	barrier := PortableToolProviderOperationV1{
		ID:   portableToolAcquisitionBarrierOperationIDV1,
		Kind: PortableToolOperationAcquisitionBarrierV1,
	}
	dependencies := make([]PortableToolProviderDependencyV1, 0, len(acquisitionIDs)+len(materializationIDs))
	if len(acquisitionIDs) > 0 || len(materializationIDs) > 0 {
		operations = append(operations, barrier)
		for _, acquisitionID := range acquisitionIDs {
			dependencies = append(dependencies, PortableToolProviderDependencyV1{Prerequisite: acquisitionID, Dependent: barrier.ID})
		}
		for _, materializationID := range materializationIDs {
			dependencies = append(dependencies, PortableToolProviderDependencyV1{Prerequisite: barrier.ID, Dependent: materializationID})
		}
	}
	sort.Slice(operations, func(left int, right int) bool { return operations[left].ID < operations[right].ID })
	sort.Slice(dependencies, func(left int, right int) bool {
		if dependencies[left].Prerequisite != dependencies[right].Prerequisite {
			return dependencies[left].Prerequisite < dependencies[right].Prerequisite
		}
		return dependencies[left].Dependent < dependencies[right].Dependent
	})
	return operations, dependencies
}

const portableToolAcquisitionBarrierOperationIDV1 = "portable-tool|acquisition-barrier"

func portableToolRecordOperationV1(
	entry PortableToolPlanEntryV1,
	reference PortableToolRecordReferenceV1,
	kind string,
	domain PortableToolDomainAuthorityV1,
) PortableToolProviderOperationV1 {
	network := NetworkPolicy("")
	if strings.HasSuffix(kind, "-materialization") {
		network = NetworkPolicyNone
	}
	referenceCopy := reference
	return PortableToolProviderOperationV1{
		ID:    portableToolOperationIDV1(entry.Scope, entry.Provenance.Tool, kind, reference.ID),
		Scope: entry.Scope, Tool: entry.Provenance.Tool, Kind: kind, Domain: domain.ID, Owner: domain.Owner,
		Record: &referenceCopy, Network: network,
	}
}

func portableToolOperationIDV1(scope, tool, kind, key string) string {
	return "portable-tool|" + scope + "|" + tool + "|" + kind + "|" + key
}

func validatePortableToolProviderOperationsV1(
	operations []PortableToolProviderOperationV1,
	expected []PortableToolProviderOperationV1,
) error {
	if len(operations) != len(expected) {
		return fmt.Errorf("portable tool provider operations must contain exactly the selected responsibilities")
	}
	seen := make(map[string]struct{}, len(operations))
	for index, operation := range operations {
		if operation.ID == "" || containsPortableControl(operation.ID) {
			return fmt.Errorf("portable tool provider operation %q must be nonempty canonical text", operation.ID)
		}
		if index > 0 && operations[index-1].ID >= operation.ID {
			return fmt.Errorf("portable tool provider operations must be unique and sorted by ID")
		}
		if _, exists := seen[operation.ID]; exists {
			return fmt.Errorf("portable tool provider operation %q is duplicated", operation.ID)
		}
		seen[operation.ID] = struct{}{}
		if operation.ID != expected[index].ID {
			return fmt.Errorf("portable tool provider operation %q is unknown or incorrectly ordered", operation.ID)
		}
		matches, err := portableToolCanonicalEqualV1(operation, expected[index])
		if err != nil {
			return err
		}
		if !matches {
			return fmt.Errorf("portable tool provider operation %q does not match its selected responsibility", operation.ID)
		}
		if err := validatePortableToolProviderOperationFieldsV1(operation); err != nil {
			return fmt.Errorf("portable tool provider operation %q: %w", operation.ID, err)
		}
	}
	return nil
}

func validatePortableToolProviderOperationFieldsV1(operation PortableToolProviderOperationV1) error {
	if operation.Kind == PortableToolOperationAcquisitionBarrierV1 {
		if operation.Owner != "" || operation.Scope != "" || operation.Tool != "" || operation.Domain != "" || operation.Record != nil || operation.InstallRoot != "" || operation.Environment != nil || operation.Export != nil || operation.Network != "" {
			return fmt.Errorf("acquisition barrier fields are inconsistent")
		}
		return nil
	}
	if operation.Scope == "" || operation.Tool == "" || operation.Kind == "" || operation.Domain == "" || operation.Owner == "" {
		return fmt.Errorf("scope, tool, kind, domain, and owner are required")
	}
	switch operation.Kind {
	case PortableToolOperationBindingContractV1, PortableToolOperationNativePackageSetV1:
		if operation.Record == nil || operation.InstallRoot != "" || operation.Environment != nil || operation.Export != nil || operation.Network != "" {
			return fmt.Errorf("record responsibility fields are inconsistent")
		}
	case PortableToolOperationBindingArtifactAcquisitionV1, PortableToolOperationPayloadAcquisitionV1:
		if operation.Record == nil || operation.InstallRoot != "" || operation.Environment != nil || operation.Export != nil || operation.Network != "" {
			return fmt.Errorf("acquisition fields are inconsistent")
		}
	case PortableToolOperationBindingArtifactMaterializationV1, PortableToolOperationPayloadMaterializationV1:
		if operation.Record == nil || operation.InstallRoot != "" || operation.Environment != nil || operation.Export != nil || operation.Network != NetworkPolicyNone {
			return fmt.Errorf("materialization must carry network policy %q and a record", NetworkPolicyNone)
		}
	case PortableToolOperationRuntimeInstallRootV1:
		if operation.Record != nil || operation.InstallRoot == "" || operation.Environment != nil || operation.Export != nil || operation.Network != "" {
			return fmt.Errorf("runtime install-root fields are inconsistent")
		}
	case PortableToolOperationRuntimeEnvironmentV1:
		if operation.Record != nil || operation.InstallRoot != "" || operation.Environment == nil || operation.Export != nil || operation.Network != "" {
			return fmt.Errorf("runtime environment fields are inconsistent")
		}
	case PortableToolOperationExportV1, PortableToolOperationCapabilityV1:
		if operation.Record != nil || operation.InstallRoot != "" || operation.Environment != nil || operation.Export == nil || operation.Network != "" {
			return fmt.Errorf("export or capability fields are inconsistent")
		}
	default:
		return fmt.Errorf("unknown operation kind %q", operation.Kind)
	}
	return nil
}

func validatePortableToolProviderDependenciesV1(
	operations []PortableToolProviderOperationV1,
	dependencies []PortableToolProviderDependencyV1,
	expected []PortableToolProviderDependencyV1,
) error {
	if len(dependencies) != len(expected) {
		return fmt.Errorf("portable tool provider dependencies must exactly match the canonical acquisition barrier")
	}
	operationSet := make(map[string]struct{}, len(operations))
	for _, operation := range operations {
		operationSet[operation.ID] = struct{}{}
	}
	seen := make(map[string]struct{}, len(dependencies))
	for index, dependency := range dependencies {
		if dependency.Prerequisite == "" || dependency.Dependent == "" {
			return fmt.Errorf("portable tool provider dependency endpoints are required")
		}
		if index > 0 && portableToolDependencyCompareV1(dependencies[index-1], dependency) >= 0 {
			return fmt.Errorf("portable tool provider dependencies must be unique and sorted")
		}
		if _, exists := operationSet[dependency.Prerequisite]; !exists {
			return fmt.Errorf("portable tool provider dependency references unknown prerequisite %q", dependency.Prerequisite)
		}
		if _, exists := operationSet[dependency.Dependent]; !exists {
			return fmt.Errorf("portable tool provider dependency references unknown dependent %q", dependency.Dependent)
		}
		if dependency.Prerequisite == dependency.Dependent {
			return fmt.Errorf("portable tool provider dependency is a self-edge")
		}
		key := dependency.Prerequisite + "\x00" + dependency.Dependent
		if _, exists := seen[key]; exists {
			return fmt.Errorf("portable tool provider dependency is duplicated")
		}
		seen[key] = struct{}{}
	}
	for _, dependency := range expected {
		key := dependency.Prerequisite + "\x00" + dependency.Dependent
		if _, exists := seen[key]; !exists {
			return fmt.Errorf("portable tool provider dependency from %q to %q is missing", dependency.Prerequisite, dependency.Dependent)
		}
		reverse := dependency.Dependent + "\x00" + dependency.Prerequisite
		if _, exists := seen[reverse]; exists {
			return fmt.Errorf("portable tool provider dependency from %q to %q is reversed", dependency.Prerequisite, dependency.Dependent)
		}
	}
	if err := rejectPortableToolProviderOperationCyclesV1(operations, dependencies); err != nil {
		return err
	}
	return nil
}

func portableToolDependencyCompareV1(left, right PortableToolProviderDependencyV1) int {
	if left.Prerequisite < right.Prerequisite {
		return -1
	}
	if left.Prerequisite > right.Prerequisite {
		return 1
	}
	return strings.Compare(left.Dependent, right.Dependent)
}

func rejectPortableToolProviderOperationCyclesV1(
	operations []PortableToolProviderOperationV1,
	dependencies []PortableToolProviderDependencyV1,
) error {
	adjacency := make(map[string][]string, len(operations))
	for _, dependency := range dependencies {
		adjacency[dependency.Prerequisite] = append(adjacency[dependency.Prerequisite], dependency.Dependent)
	}
	const (
		unseen = iota
		visiting
		done
	)
	state := make(map[string]int, len(operations))
	var visit func(string) error
	visit = func(operationID string) error {
		switch state[operationID] {
		case visiting:
			return fmt.Errorf("portable tool provider operation graph contains a cycle at %q", operationID)
		case done:
			return nil
		}
		state[operationID] = visiting
		for _, dependent := range adjacency[operationID] {
			if err := visit(dependent); err != nil {
				return err
			}
		}
		state[operationID] = done
		return nil
	}
	for _, operation := range operations {
		if err := visit(operation.ID); err != nil {
			return err
		}
	}
	return nil
}

func validatePortableToolProviderSharedClaimsV1(
	plan PortableToolPlanV1,
	operations []PortableToolProviderOperationV1,
) error {
	entries := make(map[string]PortableToolPlanEntryV1, len(plan.Tools))
	for _, entry := range plan.Tools {
		entries[entry.Scope+"\x00"+entry.Provenance.Tool] = entry
	}
	type claim struct {
		value string
		owner string
	}
	claims := make(map[string]claim)
	filesystemClaims := make(map[string][]portableToolFilesystemClaimV1)
	addClaim := func(key, value, owner string) error {
		if previous, exists := claims[key]; exists {
			if previous.value != value {
				return fmt.Errorf("portable tool provider shared-domain conflict on %q between %s and %s", strings.ReplaceAll(key, "\x00", "/"), previous.owner, owner)
			}
			return nil
		}
		claims[key] = claim{value: value, owner: owner}
		return nil
	}
	for _, operation := range operations {
		entry := entries[operation.Scope+"\x00"+operation.Tool]
		semanticClaims, err := portableToolProviderClaimsV1(operation, entry)
		if err != nil {
			return err
		}
		for _, semanticClaim := range semanticClaims {
			if err := addClaim(operation.Domain+"\x00"+semanticClaim.key, semanticClaim.value, operation.ID); err != nil {
				return err
			}
		}
		if operation.Kind == PortableToolOperationRuntimeInstallRootV1 {
			if err := addPortableToolFilesystemClaimV1(filesystemClaims, operation.Domain, operation.InstallRoot, operation.InstallRoot, "install-root", operation.ID); err != nil {
				return err
			}
		}
		if operation.Record != nil && (operation.Kind == PortableToolOperationBindingArtifactMaterializationV1 || operation.Kind == PortableToolOperationPayloadMaterializationV1) {
			selected, found := portableToolFindSelectedRecordV1(entry, operation.Kind, *operation.Record)
			if found {
				destination := portableToolFilesystemDestinationV1(selected)
				if operation.Kind == PortableToolOperationPayloadMaterializationV1 {
					destination = portableToolPayloadFilesystemDestinationV1(entry, selected)
				}
				kind := "binding-artifact"
				if operation.Kind == PortableToolOperationPayloadMaterializationV1 {
					kind = "payload"
				}
				if err := addPortableToolFilesystemClaimV1(filesystemClaims, operation.Domain, destination,
					string(selected.Reference.Digest), kind, operation.ID); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

type portableToolProviderSemanticClaimV1 struct {
	key   string
	value string
}

func portableToolProviderClaimsV1(
	operation PortableToolProviderOperationV1,
	entry PortableToolPlanEntryV1,
) ([]portableToolProviderSemanticClaimV1, error) {
	switch operation.Kind {
	case PortableToolOperationRuntimeEnvironmentV1:
		return []portableToolProviderSemanticClaimV1{{key: "environment:" + operation.Environment.Name, value: operation.Environment.Value}}, nil
	case PortableToolOperationExportV1:
		return []portableToolProviderSemanticClaimV1{{key: "export:" + operation.Export.Name, value: operation.Export.Path}}, nil
	case PortableToolOperationCapabilityV1:
		return []portableToolProviderSemanticClaimV1{{key: "capability:" + operation.Export.Name, value: operation.Export.Path}}, nil
	case PortableToolOperationNativePackageSetV1:
		if operation.Record == nil {
			return nil, nil
		}
		selected, found := portableToolFindSelectedRecordV1(entry, operation.Kind, *operation.Record)
		if !found {
			return nil, nil
		}
		return portableToolNativePackageClaimsV1(selected)
	case PortableToolOperationBindingArtifactAcquisitionV1, PortableToolOperationBindingArtifactMaterializationV1,
		PortableToolOperationPayloadAcquisitionV1, PortableToolOperationPayloadMaterializationV1,
		PortableToolOperationBindingContractV1:
		if operation.Record == nil {
			return nil, nil
		}
		selected, found := portableToolFindSelectedRecordV1(entry, operation.Kind, *operation.Record)
		if !found {
			return []portableToolProviderSemanticClaimV1{{key: "record:" + operation.Kind + ":" + operation.Record.ID, value: string(operation.Record.Digest)}}, nil
		}
		return []portableToolProviderSemanticClaimV1{{
			key:   "record:" + portableToolRecordResponsibilityKindV1(operation.Kind) + ":" + operation.Record.ID,
			value: string(selected.Reference.Digest) + "\x00" + portableToolFilesystemDestinationV1(selected),
		}}, nil
	default:
		return nil, nil
	}
}

func portableToolNativePackageClaimsV1(selected PortableToolSelectedRecordV1) ([]portableToolProviderSemanticClaimV1, error) {
	managerValue, managerPresent := selected.Record.Value["manager"]
	requirements, requirementsPresent, err := portableToolStringListFieldV1(selected.Record.Value, "requirements")
	if err != nil {
		return nil, fmt.Errorf("native package requirements: %w", err)
	}
	repositories, repositoriesPresent, err := portableToolStringListFieldV1(selected.Record.Value, "repositories")
	if err != nil {
		return nil, fmt.Errorf("native package repositories: %w", err)
	}
	if !managerPresent {
		if requirementsPresent || repositoriesPresent {
			return nil, fmt.Errorf("native package manager is required when package semantics are present")
		}
		return nil, nil
	}
	manager, ok := managerValue.(string)
	if !ok || manager == "" {
		return nil, fmt.Errorf("native package manager must be a nonempty string")
	}
	if !requirementsPresent && !repositoriesPresent {
		// Provider-neutral hand-built fixtures may carry only the record identity
		// and manager. They have no semantic package claims to compare; compiled
		// v1 records carry explicit arrays and therefore take the path below.
		return nil, nil
	}
	claims := make([]portableToolProviderSemanticClaimV1, 0, len(requirements)+len(repositories))
	for _, requirement := range requirements {
		parsed, err := blueprint.ParseAPTPackageRequest(requirement)
		if err != nil {
			return nil, fmt.Errorf("native package requirement %q: %w", requirement, err)
		}
		claims = append(claims, portableToolProviderSemanticClaimV1{
			key: "package/" + manager + "/" + parsed.Name, value: requirement,
		})
	}
	for _, repository := range repositories {
		claims = append(claims, portableToolProviderSemanticClaimV1{
			key: "repository/" + manager + "/" + repository, value: repository,
		})
	}
	sort.Slice(claims, func(left, right int) bool {
		if claims[left].key != claims[right].key {
			return claims[left].key < claims[right].key
		}
		return claims[left].value < claims[right].value
	})
	return claims, nil
}

func portableToolStringListFieldV1(value canonical.Object, field string) ([]string, bool, error) {
	raw, exists := value[field]
	if !exists {
		return nil, false, nil
	}
	var values []string
	switch typed := raw.(type) {
	case []string:
		values = append([]string{}, typed...)
	case []any:
		values = make([]string, len(typed))
		for index, item := range typed {
			stringValue, ok := item.(string)
			if !ok {
				return nil, true, fmt.Errorf("%s entry %d must be a string", field, index)
			}
			values[index] = stringValue
		}
	default:
		return nil, true, fmt.Errorf("%s must be a string array", field)
	}
	return values, true, nil
}

func addPortableToolFilesystemClaimV1(
	claims map[string][]portableToolFilesystemClaimV1,
	domain string,
	claimedPath string,
	value string,
	kind string,
	owner string,
) error {
	// This adapter is kept separate from semantic claims so path ownership can
	// use Linux component boundaries rather than treating /opt/a and /opt/ab as
	// aliases. Payload destinations are resolved before this check; binding
	// artifact destinations retain their record-local identity semantics.
	if claimedPath == "" {
		return nil
	}
	for _, previous := range claims[domain] {
		if !portableToolLinuxPathsOverlapV1(previous.path, claimedPath) {
			continue
		}
		if previous.path == claimedPath && previous.value == value {
			return nil
		}
		if (previous.kind == "install-root" && kind == "payload") || (previous.kind == "payload" && kind == "install-root") {
			continue
		}
		return fmt.Errorf("portable tool provider shared filesystem-domain conflict on %q between %s and %s", claimedPath, previous.owner, owner)
	}
	claims[domain] = append(claims[domain], portableToolFilesystemClaimV1{path: claimedPath, value: value, kind: kind, owner: owner})
	return nil
}

func portableToolLinuxPathsOverlapV1(left, right string) bool {
	left = path.Clean(left)
	right = path.Clean(right)
	if left == right {
		return true
	}
	if !path.IsAbs(left) || !path.IsAbs(right) {
		return false
	}
	return strings.HasPrefix(left, right+"/") || strings.HasPrefix(right, left+"/")
}

func portableToolRecordResponsibilityKindV1(kind string) string {
	switch kind {
	case PortableToolOperationBindingArtifactAcquisitionV1, PortableToolOperationBindingArtifactMaterializationV1:
		return "binding-artifact"
	case PortableToolOperationPayloadAcquisitionV1, PortableToolOperationPayloadMaterializationV1:
		return "payload"
	default:
		return kind
	}
}

func portableToolFilesystemDestinationV1(record PortableToolSelectedRecordV1) string {
	for _, field := range []string{"install_directory", "logical_path", "filename"} {
		if value, ok := record.Record.Value[field].(string); ok && value != "" {
			return value
		}
	}
	return record.Reference.ID
}

func portableToolPayloadFilesystemDestinationV1(entry PortableToolPlanEntryV1, record PortableToolSelectedRecordV1) string {
	destination, ok := record.Record.Value["install_directory"].(string)
	if !ok || destination == "" {
		// Provider-neutral hand-built fixtures may omit typed payload fields;
		// compiled payload records always carry install_directory.
		destination = record.Reference.ID
	}
	if entry.Runtime != nil && !path.IsAbs(destination) {
		return path.Join(entry.Runtime.InstallRoot, destination)
	}
	return destination
}

func portableToolFindSelectedRecordV1(
	entry PortableToolPlanEntryV1,
	kind string,
	reference PortableToolRecordReferenceV1,
) (PortableToolSelectedRecordV1, bool) {
	var records []PortableToolSelectedRecordV1
	switch kind {
	case PortableToolOperationBindingContractV1:
		records = entry.Responsibilities.BindingContracts
	case PortableToolOperationBindingArtifactAcquisitionV1, PortableToolOperationBindingArtifactMaterializationV1:
		records = entry.Responsibilities.BindingArtifacts
	case PortableToolOperationPayloadAcquisitionV1, PortableToolOperationPayloadMaterializationV1:
		records = entry.Responsibilities.Payloads
	case PortableToolOperationNativePackageSetV1:
		records = entry.Responsibilities.NativePackageSets
	}
	for _, selected := range records {
		if selected.Reference == reference {
			return selected, true
		}
	}
	return PortableToolSelectedRecordV1{}, false
}

func portableToolCanonicalEqualV1(left, right any) (bool, error) {
	leftBytes, err := canonical.Marshal(left)
	if err != nil {
		return false, fmt.Errorf("portable tool canonical operation: %w", err)
	}
	rightBytes, err := canonical.Marshal(right)
	if err != nil {
		return false, fmt.Errorf("portable tool canonical operation: %w", err)
	}
	return bytes.Equal(leftBytes, rightBytes), nil
}

func cloneProviderPlanForPortableToolDAGV1(plan ProviderPlanV1) ProviderPlanV1 {
	result := plan
	result.Nodes = make([]NodeSpec, len(plan.Nodes))
	for index, node := range plan.Nodes {
		result.Nodes[index] = node
		result.Nodes[index].Components = append([]string{}, node.Components...)
		result.Nodes[index].Request.Value = clonePortableToolCanonicalObjectV1(node.Request.Value)
		result.Nodes[index].OutputDeclarations = append([]OutputDeclaration{}, node.OutputDeclarations...)
		for outputIndex, output := range node.OutputDeclarations {
			result.Nodes[index].OutputDeclarations[outputIndex].Provenance.Value = clonePortableToolCanonicalObjectV1(output.Provenance.Value)
		}
		result.Nodes[index].Requirements.Executables = append([]ExecutableRequirement{}, node.Requirements.Executables...)
		result.Nodes[index].Requirements.Files = append([]FileRequirement{}, node.Requirements.Files...)
		result.Nodes[index].Requirements.ProviderData.Value = clonePortableToolCanonicalObjectV1(node.Requirements.ProviderData.Value)
	}
	result.Edges = append([]ProviderEdgeV1{}, plan.Edges...)
	return result
}

func clonePortableToolPlanForPortableToolDAGV1(plan PortableToolPlanV1) PortableToolPlanV1 {
	result := plan
	result.Tools = make([]PortableToolPlanEntryV1, len(plan.Tools))
	for index, entry := range plan.Tools {
		result.Tools[index] = entry
		result.Tools[index].Exports = append([]PortableToolExportV1{}, entry.Exports...)
		result.Tools[index].ValidationProfiles = append([]PortableToolValidationProfileV1{}, entry.ValidationProfiles...)
		for profileIndex, profile := range entry.ValidationProfiles {
			result.Tools[index].ValidationProfiles[profileIndex].Record.Value = clonePortableToolCanonicalObjectV1(profile.Record.Value)
		}
		if entry.Runtime != nil {
			runtime := *entry.Runtime
			runtime.Environment = append([]PortableToolEnvironmentVariableV1{}, entry.Runtime.Environment...)
			result.Tools[index].Runtime = &runtime
		}
		result.Tools[index].Responsibilities.BindingContracts = clonePortableToolSelectedRecordsV1(entry.Responsibilities.BindingContracts)
		result.Tools[index].Responsibilities.BindingArtifacts = clonePortableToolSelectedRecordsV1(entry.Responsibilities.BindingArtifacts)
		result.Tools[index].Responsibilities.Payloads = clonePortableToolSelectedRecordsV1(entry.Responsibilities.Payloads)
		result.Tools[index].Responsibilities.NativePackageSets = clonePortableToolSelectedRecordsV1(entry.Responsibilities.NativePackageSets)
	}
	return result
}

func clonePortableToolSelectedRecordsV1(records []PortableToolSelectedRecordV1) []PortableToolSelectedRecordV1 {
	result := append([]PortableToolSelectedRecordV1{}, records...)
	for index, selected := range records {
		result[index].Record.Value = clonePortableToolCanonicalObjectV1(selected.Record.Value)
	}
	return result
}

func clonePortableToolCanonicalObjectV1(value canonical.Object) canonical.Object {
	if value == nil {
		return nil
	}
	cloned := clonePortableToolCanonicalValueV1(reflect.ValueOf(value))
	if !cloned.IsValid() || cloned.IsNil() {
		return nil
	}
	return cloned.Interface().(canonical.Object)
}

func clonePortableToolCanonicalValueV1(value reflect.Value) reflect.Value {
	if !value.IsValid() {
		return value
	}
	switch value.Kind() {
	case reflect.Interface:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		cloned := clonePortableToolCanonicalValueV1(value.Elem())
		wrapped := reflect.New(value.Type()).Elem()
		wrapped.Set(cloned)
		return wrapped
	case reflect.Map:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		cloned := reflect.MakeMapWithSize(value.Type(), value.Len())
		iter := value.MapRange()
		for iter.Next() {
			cloned.SetMapIndex(iter.Key(), clonePortableToolCanonicalValueV1(iter.Value()))
		}
		return cloned
	case reflect.Slice:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		cloned := reflect.MakeSlice(value.Type(), value.Len(), value.Len())
		for index := 0; index < value.Len(); index++ {
			cloned.Index(index).Set(clonePortableToolCanonicalValueV1(value.Index(index)))
		}
		return cloned
	case reflect.Pointer:
		if value.IsNil() {
			return reflect.Zero(value.Type())
		}
		cloned := reflect.New(value.Type().Elem())
		cloned.Elem().Set(clonePortableToolCanonicalValueV1(value.Elem()))
		return cloned
	default:
		return value
	}
}
