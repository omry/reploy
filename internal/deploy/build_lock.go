package deploy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providerstore"
)

const BuildLockSchemaV1 = "lock-v1"

type BuildLockV1 struct {
	Schema                string                       `json:"schema"`
	BlueprintDigest       canonical.Digest             `json:"blueprint_digest"`
	Overlay               RequestOverlayV1             `json:"overlay"`
	ResolvedRequestDigest canonical.Digest             `json:"resolved_request_digest"`
	Platform              blueprint.Platform           `json:"platform"`
	Base                  ImageDescriptor              `json:"base"`
	Graph                 ProviderGraphLockV1          `json:"graph"`
	Nodes                 []NodeLockV1                 `json:"nodes"`
	RuntimePolicy         RuntimePolicyV1              `json:"runtime_policy"`
	ValidationRecord      providerstore.StoreObjectRef `json:"validation_record"`
	FinalImage            providers.RealizedImageV1    `json:"final_image"`
}

type ProviderGraphLockV1 struct {
	Nodes []providers.NodeID         `json:"nodes"`
	Edges []providers.ProviderEdgeV1 `json:"edges"`
}

type NodeLockV1 struct {
	NodeID               providers.NodeID                        `json:"node_id"`
	Provider             blueprint.ComponentType                 `json:"provider"`
	PlanDigest           canonical.Digest                        `json:"plan_digest"`
	ResolverCacheKey     canonical.Digest                        `json:"resolver_cache_key"`
	RequirementProfile   providers.RequirementProfile            `json:"requirement_profile"`
	ValidationEvidence   providers.ValidationEvidence            `json:"validation_evidence"`
	BundleManifest       providerstore.StoreObjectRef            `json:"bundle_manifest"`
	TransactionDigest    canonical.Digest                        `json:"transaction_digest"`
	Upstream             providers.RealizedImageV1               `json:"upstream"`
	Result               providers.RealizedImageV1               `json:"result"`
	GeneratedExecutables []providers.RealizedGeneratedExecutable `json:"generated_executables"`
	Outputs              []providers.RealizedOutput              `json:"outputs"`
}

func BuildLockDigestV1(lock BuildLockV1, validateProfileOwner providers.RequirementProfileOwnerValidator) (canonical.Digest, error) {
	if err := ValidateBuildLockV1(lock, validateProfileOwner); err != nil {
		return "", err
	}
	return canonical.Sum("build-lock", BuildLockSchemaV1, lock)
}

func EncodeBuildLockV1(lock BuildLockV1, validateProfileOwner providers.RequirementProfileOwnerValidator) ([]byte, error) {
	if _, err := BuildLockDigestV1(lock, validateProfileOwner); err != nil {
		return nil, fmt.Errorf("encode build lock: %w", err)
	}
	return canonical.Marshal(lock)
}

func DecodeBuildLockV1(content []byte, validateProfileOwner providers.RequirementProfileOwnerValidator) (BuildLockV1, error) {
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	var lock BuildLockV1
	if err := decoder.Decode(&lock); err != nil {
		return BuildLockV1{}, fmt.Errorf("decode build lock: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return BuildLockV1{}, fmt.Errorf("build lock contains trailing JSON")
		}
		return BuildLockV1{}, fmt.Errorf("decode build lock trailer: %w", err)
	}
	if _, err := BuildLockDigestV1(lock, validateProfileOwner); err != nil {
		return BuildLockV1{}, fmt.Errorf("validate build lock: %w", err)
	}
	canonicalContent, err := canonical.Marshal(lock)
	if err != nil {
		return BuildLockV1{}, err
	}
	if !bytes.Equal(content, canonicalContent) {
		return BuildLockV1{}, fmt.Errorf("build lock is not canonical JSON")
	}
	return lock, nil
}

func ValidateBuildLockV1(lock BuildLockV1, validateProfileOwner providers.RequirementProfileOwnerValidator) error {
	if lock.Schema != BuildLockSchemaV1 {
		return fmt.Errorf("build lock schema must be %q", BuildLockSchemaV1)
	}
	if err := lock.BlueprintDigest.Validate(); err != nil {
		return fmt.Errorf("build lock blueprint digest: %w", err)
	}
	if _, err := RequestOverlayDigestV1(lock.Overlay); err != nil {
		return fmt.Errorf("build lock overlay: %w", err)
	}
	if err := lock.ResolvedRequestDigest.Validate(); err != nil {
		return fmt.Errorf("build lock resolved request digest: %w", err)
	}
	if err := lock.Platform.Validate(); err != nil {
		return fmt.Errorf("build lock platform: %w", err)
	}
	if err := lock.Base.Validate(); err != nil {
		return fmt.Errorf("build lock base: %w", err)
	}
	if lock.Base.Platform != lock.Platform {
		return fmt.Errorf("build lock base platform does not match selected platform")
	}
	graphNodes, err := validateProviderGraphLock(lock.Graph)
	if err != nil {
		return err
	}
	if lock.Nodes == nil {
		return fmt.Errorf("build lock nodes must use an array")
	}
	lockedNodes := make(map[providers.NodeID]bool, len(lock.Nodes))
	for index, node := range lock.Nodes {
		if index > 0 && lock.Nodes[index-1].NodeID >= node.NodeID {
			return fmt.Errorf("build lock nodes must be unique and sorted by node ID")
		}
		if !graphNodes[node.NodeID] || node.NodeID == "base" {
			return fmt.Errorf("build lock node %q is not a non-base graph node", node.NodeID)
		}
		if err := validateNodeLock(node, lock.Platform, validateProfileOwner); err != nil {
			return fmt.Errorf("build lock node %q: %w", node.NodeID, err)
		}
		lockedNodes[node.NodeID] = true
	}
	for node := range graphNodes {
		if node != "base" && !lockedNodes[node] {
			return fmt.Errorf("build lock is missing node %q", node)
		}
	}
	if err := validateBuildLockImageLineage(lock); err != nil {
		return err
	}
	if err := ValidateRuntimePolicyV1(lock.RuntimePolicy); err != nil {
		return fmt.Errorf("build lock runtime policy: %w", err)
	}
	if err := lock.ValidationRecord.Validate(); err != nil {
		return fmt.Errorf("build lock validation record: %w", err)
	}
	if lock.ValidationRecord.Kind != providerstore.ValidationRecordKind {
		return fmt.Errorf("build lock validation record must reference a validation-record")
	}
	if err := lock.FinalImage.Validate(); err != nil {
		return fmt.Errorf("build lock final image: %w", err)
	}
	return nil
}

func validateBuildLockImageLineage(lock BuildLockV1) error {
	order, err := providers.StableProviderNodeOrder(lock.Graph.Nodes, lock.Graph.Edges)
	if err != nil {
		return fmt.Errorf("build lock graph order: %w", err)
	}
	baseRootFS, err := RootFSSubject(lock.Base.RootFSDiffIDs)
	if err != nil {
		return fmt.Errorf("build lock base root filesystem: %w", err)
	}
	baseDigest := lock.Base.ManifestDigest
	if baseDigest == "" {
		baseDigest = lock.Base.ConfigDigest
	}
	current := providers.RealizedImageV1{
		Digest: baseDigest, ConfigDigest: lock.Base.ConfigDigest, RootFSSubject: baseRootFS,
	}
	if err := current.Validate(); err != nil {
		return fmt.Errorf("build lock base realized image: %w", err)
	}
	nodes := make(map[providers.NodeID]NodeLockV1, len(lock.Nodes))
	for _, node := range lock.Nodes {
		nodes[node.NodeID] = node
	}
	for _, nodeID := range order {
		if nodeID == "base" {
			continue
		}
		node := nodes[nodeID]
		if node.Upstream != current {
			return fmt.Errorf(
				"build lock node %q upstream image does not match the preceding graph prefix: got digest=%s config=%s rootfs=%s, want digest=%s config=%s rootfs=%s",
				nodeID,
				node.Upstream.Digest, node.Upstream.ConfigDigest, node.Upstream.RootFSSubject,
				current.Digest, current.ConfigDigest, current.RootFSSubject,
			)
		}
		current = node.Result
	}
	if lock.FinalImage.RootFSSubject != current.RootFSSubject {
		return fmt.Errorf(
			"build lock final image root filesystem %s does not match the final graph prefix %s",
			lock.FinalImage.RootFSSubject, current.RootFSSubject,
		)
	}
	return nil
}

func validateProviderGraphLock(graph ProviderGraphLockV1) (map[providers.NodeID]bool, error) {
	if graph.Nodes == nil || graph.Edges == nil {
		return nil, fmt.Errorf("build lock graph nodes and edges must use arrays")
	}
	nodes := make(map[providers.NodeID]bool, len(graph.Nodes))
	baseCount := 0
	for index, node := range graph.Nodes {
		if err := validateLockedGraphNodeID(node); err != nil {
			return nil, err
		}
		if index > 0 && graph.Nodes[index-1] >= node {
			return nil, fmt.Errorf("build lock graph nodes must be unique and sorted")
		}
		if node == "base" {
			baseCount++
		}
		nodes[node] = true
	}
	if baseCount != 1 {
		return nil, fmt.Errorf("build lock graph must contain exactly one base node")
	}
	for index, edge := range graph.Edges {
		if index > 0 && compareLockedEdges(graph.Edges[index-1], edge) >= 0 {
			return nil, fmt.Errorf("build lock graph edges must be unique and sorted")
		}
		if !nodes[edge.Supplier] || !nodes[edge.Consumer] || edge.Supplier == edge.Consumer {
			return nil, fmt.Errorf("build lock graph edge %q -> %q is invalid", edge.Supplier, edge.Consumer)
		}
		if edge.Consumer == "base" || blueprint.ValidateProviderIdentifier("build lock graph requirement", edge.RequirementID) != nil {
			return nil, fmt.Errorf("build lock graph edge requirement is invalid")
		}
		if err := validateQualifiedOutput(edge.Output); err != nil {
			return nil, err
		}
	}
	if err := rejectLockedGraphCycles(graph.Nodes, graph.Edges); err != nil {
		return nil, err
	}
	return nodes, nil
}

func validateNodeLock(node NodeLockV1, platform blueprint.Platform, validateProfileOwner providers.RequirementProfileOwnerValidator) error {
	switch node.Provider {
	case blueprint.ComponentTypeAPT:
		if node.NodeID != "apt" {
			return fmt.Errorf("APT node ID must be apt")
		}
	case blueprint.ComponentTypePython:
		component, ok := strings.CutPrefix(string(node.NodeID), "python/")
		if !ok || blueprint.ValidateProviderIdentifier("Python node component", component) != nil {
			return fmt.Errorf("Python node ID must use python/<component>")
		}
	default:
		return fmt.Errorf("node provider %q is unsupported", node.Provider)
	}
	for _, item := range []struct {
		field  string
		digest canonical.Digest
	}{
		{field: "plan", digest: node.PlanDigest},
		{field: "resolver cache key", digest: node.ResolverCacheKey},
		{field: "transaction", digest: node.TransactionDigest},
	} {
		if err := item.digest.Validate(); err != nil {
			return fmt.Errorf("%s digest: %w", item.field, err)
		}
	}
	profileDigest, err := providers.RequirementProfileDigest(node.RequirementProfile, validateProfileOwner)
	if err != nil {
		return fmt.Errorf("requirement profile: %w", err)
	}
	if node.RequirementProfile.Platform != platform {
		return fmt.Errorf("requirement profile platform does not match build lock")
	}
	if err := node.Upstream.Validate(); err != nil {
		return fmt.Errorf("upstream image: %w", err)
	}
	if err := node.ValidationEvidence.Validate(); err != nil {
		return err
	}
	if node.ValidationEvidence.SubjectRootFS != node.Upstream.RootFSSubject {
		return fmt.Errorf("validation evidence does not identify the upstream root filesystem")
	}
	if node.ValidationEvidence.ProfileDigest != profileDigest {
		return fmt.Errorf("validation evidence does not identify the requirement profile")
	}
	if err := node.BundleManifest.Validate(); err != nil {
		return err
	}
	if node.BundleManifest.Kind != providerstore.BundleManifestKind {
		return fmt.Errorf("bundle manifest must reference a bundle-manifest")
	}
	if err := node.Result.Validate(); err != nil {
		return fmt.Errorf("result image: %w", err)
	}
	if node.GeneratedExecutables == nil || node.Outputs == nil {
		return fmt.Errorf("generated executables and outputs must use arrays")
	}
	for index, generated := range node.GeneratedExecutables {
		if index > 0 && node.GeneratedExecutables[index-1].Declaration.ID >= generated.Declaration.ID {
			return fmt.Errorf("generated executables must be unique and sorted")
		}
		if err := providers.ValidateRealizedGeneratedExecutable(generated); err != nil {
			return err
		}
	}
	for index, output := range node.Outputs {
		if output.SupplierNode != node.NodeID {
			return fmt.Errorf("output %s.%s has a different supplier node", output.SupplierComponent, output.Name)
		}
		if index > 0 && compareLockedOutputs(node.Outputs[index-1], output) >= 0 {
			return fmt.Errorf("outputs must be unique and sorted")
		}
		if err := providers.ValidateRealizedOutput(output); err != nil {
			return err
		}
	}
	return nil
}

func validateLockedGraphNodeID(node providers.NodeID) error {
	if node == "base" || node == "apt" {
		return nil
	}
	component, ok := strings.CutPrefix(string(node), "python/")
	if !ok || blueprint.ValidateProviderIdentifier("Python graph node component", component) != nil {
		return fmt.Errorf("build lock graph node ID %q is unsupported", node)
	}
	return nil
}

func rejectLockedGraphCycles(nodes []providers.NodeID, edges []providers.ProviderEdgeV1) error {
	adjacency := make(map[providers.NodeID][]providers.NodeID, len(nodes))
	for _, edge := range edges {
		adjacency[edge.Supplier] = append(adjacency[edge.Supplier], edge.Consumer)
	}
	const (
		unvisited = iota
		visiting
		visited
	)
	state := make(map[providers.NodeID]int, len(nodes))
	var visit func(providers.NodeID) error
	visit = func(node providers.NodeID) error {
		switch state[node] {
		case visiting:
			return fmt.Errorf("build lock graph contains a provider cycle at %q", node)
		case visited:
			return nil
		}
		state[node] = visiting
		for _, consumer := range adjacency[node] {
			if err := visit(consumer); err != nil {
				return err
			}
		}
		state[node] = visited
		return nil
	}
	for _, node := range nodes {
		if err := visit(node); err != nil {
			return err
		}
	}
	return nil
}

func validateQualifiedOutput(output providers.QualifiedOutput) error {
	if err := blueprint.ValidateProviderIdentifier("qualified output component", output.Component); err != nil {
		return err
	}
	return blueprint.ValidateProviderIdentifier("qualified output name", output.Name)
}

func compareLockedEdges(left providers.ProviderEdgeV1, right providers.ProviderEdgeV1) int {
	if left.Supplier != right.Supplier {
		return strings.Compare(string(left.Supplier), string(right.Supplier))
	}
	if left.Consumer != right.Consumer {
		return strings.Compare(string(left.Consumer), string(right.Consumer))
	}
	if left.RequirementID != right.RequirementID {
		return strings.Compare(left.RequirementID, right.RequirementID)
	}
	if left.Output.Component != right.Output.Component {
		return strings.Compare(left.Output.Component, right.Output.Component)
	}
	return strings.Compare(left.Output.Name, right.Output.Name)
}

func compareLockedOutputs(left providers.RealizedOutput, right providers.RealizedOutput) int {
	if left.SupplierComponent != right.SupplierComponent {
		return strings.Compare(left.SupplierComponent, right.SupplierComponent)
	}
	return strings.Compare(left.Name, right.Name)
}
