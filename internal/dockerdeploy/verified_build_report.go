package dockerdeploy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"

	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/deploy"
)

// VerifiedBuildReportMaxBytes includes the terminating newline.
const VerifiedBuildReportMaxBytes = 1 << 20

// VerifiedBuildReportV1 is a closed, public audit snapshot. It deliberately
// excludes source records, image configuration and host paths.
type VerifiedBuildReportV1 struct {
	Schema        string                   `json:"schema"`
	SchemaVersion int                      `json:"schema_version"`
	Environment   string                   `json:"environment"`
	Platform      verifiedReportPlatform   `json:"platform"`
	Build         verifiedReportBuild      `json:"build"`
	Inputs        verifiedReportInputs     `json:"inputs"`
	Base          verifiedReportBase       `json:"base"`
	Image         verifiedReportImage      `json:"image"`
	Artifacts     verifiedReportArtifacts  `json:"artifacts"`
	Validation    verifiedReportValidation `json:"validation"`
	Runtime       verifiedReportRuntime    `json:"runtime"`
	Checks        verifiedReportChecks     `json:"checks"`
}
type verifiedReportPlatform struct {
	OS           string `json:"os"`
	Architecture string `json:"architecture"`
	Variant      string `json:"variant"`
}
type verifiedReportBuild struct {
	LockDigest     canonical.Digest `json:"lock_digest"`
	ImageReference string           `json:"image_reference"`
	LocalImageID   canonical.Digest `json:"local_image_id"`
}
type verifiedReportInputs struct {
	BlueprintDigest       canonical.Digest `json:"blueprint_digest"`
	ResolvedRequestDigest canonical.Digest `json:"resolved_request_digest"`
}
type verifiedReportBase struct {
	ImmutableReference string             `json:"immutable_reference"`
	LocalImageID       canonical.Digest   `json:"local_image_id"`
	ManifestDigest     *canonical.Digest  `json:"manifest_digest"`
	RootFSDiffIDs      []canonical.Digest `json:"rootfs_diff_ids"`
}
type verifiedReportImage struct {
	RootFSDiffIDs []canonical.Digest `json:"rootfs_diff_ids"`
}
type verifiedReportBundle struct {
	NodeID      string           `json:"node_id"`
	LogicalPath string           `json:"logical_path"`
	Kind        string           `json:"kind"`
	Size        string           `json:"size"`
	SHA256      canonical.Digest `json:"sha256"`
}
type verifiedReportAcquisition struct {
	Scope          string           `json:"scope"`
	Tool           string           `json:"tool"`
	ArtifactID     string           `json:"artifact_id"`
	ArtifactDigest canonical.Digest `json:"artifact_digest"`
	LogicalPath    string           `json:"logical_path"`
	Kind           string           `json:"kind"`
	Size           string           `json:"size"`
	SHA256         canonical.Digest `json:"sha256"`
}
type verifiedReportArtifacts struct {
	ProviderBundles      []verifiedReportBundle      `json:"provider_bundles"`
	PortableAcquisitions []verifiedReportAcquisition `json:"portable_acquisitions"`
}
type verifiedReportValidation struct {
	Schema              string           `json:"schema"`
	RecordDigest        canonical.Digest `json:"record_digest"`
	RuntimePolicySchema string           `json:"runtime_policy_schema"`
	RuntimePolicyDigest canonical.Digest `json:"runtime_policy_digest"`
}
type verifiedReportRuntime struct {
	Account         deploy.ApplicationLocalAccountV1    `json:"account"`
	StartupVerifier deploy.ApplicationStartupVerifierV1 `json:"startup_verifier"`
	PlannedSandbox  verifiedReportSandbox               `json:"planned_sandbox"`
}
type verifiedReportSandbox struct {
	UID               string                `json:"uid"`
	GID               string                `json:"gid"`
	SupplementaryGIDs []string              `json:"supplementary_gids"`
	Network           verifiedReportNetwork `json:"network"`
	ReadOnlyRoot      bool                  `json:"read_only_root"`
	TemporaryHome     string                `json:"temporary_home"`
	Kernel            verifiedReportKernel  `json:"kernel"`
}
type verifiedReportNetwork struct {
	Public    string `json:"public"`
	Local     string `json:"local"`
	Ambiguous string `json:"ambiguous"`
}
type verifiedReportKernel struct {
	DropAllCapabilities bool     `json:"drop_all_capabilities"`
	NoNewPrivileges     bool     `json:"no_new_privileges"`
	Privileged          bool     `json:"privileged"`
	SeccompProfile      string   `json:"seccomp_profile"`
	HostNamespaces      []string `json:"host_namespaces"`
	HostDevices         []string `json:"host_devices"`
}
type verifiedReportChecks struct {
	ProviderStoreObjects int `json:"provider_store_objects"`
	Images               int `json:"images"`
	Commands             int `json:"commands"`
}

type verifiedBuildReportCollector struct {
	artifacts verifiedReportArtifacts
	// A lower bound on serialized variable data caps added collection before
	// append. Escaping can grow bounded input by at most six times; the final
	// bounded encoding enforces the exact public limit, including the newline.
	used int
}

func newVerifiedBuildReportCollector() *verifiedBuildReportCollector {
	return &verifiedBuildReportCollector{artifacts: verifiedReportArtifacts{
		ProviderBundles: []verifiedReportBundle{}, PortableAcquisitions: []verifiedReportAcquisition{},
	}}
}
func reportSizeError() error {
	return fmt.Errorf("verified-build JSON exceeds %d bytes", VerifiedBuildReportMaxBytes)
}
func (c *verifiedBuildReportCollector) strings(values ...string) error {
	for _, value := range values {
		if len(value) > VerifiedBuildReportMaxBytes-c.used-2 {
			return reportSizeError()
		}
		c.used += len(value) + 2
	}
	return nil
}
func (c *verifiedBuildReportCollector) collect(a deploy.BuildLockClosureArtifact) error {
	d := a.Descriptor
	if a.Artifact.ID == "" {
		if err := c.strings(string(a.NodeID), d.LogicalPath, d.Kind, d.Size, string(d.SHA256)); err != nil {
			return err
		}
		c.artifacts.ProviderBundles = append(c.artifacts.ProviderBundles, verifiedReportBundle{
			string(a.NodeID), d.LogicalPath, d.Kind, d.Size, d.SHA256,
		})
	} else {
		if err := c.strings(a.Scope, a.Tool, a.Artifact.ID, string(a.Artifact.Digest), d.LogicalPath, d.Kind, d.Size, string(d.SHA256)); err != nil {
			return err
		}
		c.artifacts.PortableAcquisitions = append(c.artifacts.PortableAcquisitions, verifiedReportAcquisition{
			a.Scope, a.Tool, a.Artifact.ID, a.Artifact.Digest, d.LogicalPath, d.Kind, d.Size, d.SHA256,
		})
	}
	return nil
}
func compareReportBundle(a, b verifiedReportBundle) int {
	return slices.Compare([]string{a.NodeID, a.LogicalPath}, []string{b.NodeID, b.LogicalPath})
}
func compareReportAcquisition(a, b verifiedReportAcquisition) int {
	return slices.Compare([]string{a.Scope, a.Tool, a.ArtifactID, string(a.ArtifactDigest)}, []string{b.Scope, b.Tool, b.ArtifactID, string(b.ArtifactDigest)})
}
func (c *verifiedBuildReportCollector) finish(current CurrentBuild, runtime CurrentRuntimePlanV1, final InspectedImageCandidate, validation deploy.PrefixValidationV1, checks CurrentBuildVerificationResultV1) (*VerifiedBuildReportV1, error) {
	lock := current.Lock
	policy, err := deploy.RuntimePolicyDigestV1(lock.RuntimePolicy)
	if err != nil {
		return nil, err
	}
	sandbox := runtime.Docker.Sandbox
	account, helper := lock.RuntimeLayer.Account, lock.RuntimeLayer.Verifier
	if err := c.strings(runtime.Document.Environment.ID, lock.Platform.OS, lock.Platform.Architecture, lock.Platform.Variant,
		string(current.Generation.BuildLockDigest), current.Generation.Reference, string(final.Image.ConfigDigest),
		string(lock.BlueprintDigest), string(lock.ResolvedRequestDigest), lock.Base.ImmutableReference, string(lock.Base.ConfigDigest), string(lock.Base.ManifestDigest),
		validation.Schema, string(lock.ValidationRecord.Digest), lock.RuntimePolicy.Schema, string(policy), account.Schema, account.Name, account.UID, account.GID, account.Home,
		helper.Schema, helper.RecipeVersion, helper.Path, string(helper.Artifact), helper.Size,
		string(sandbox.Network.Public), string(sandbox.Network.Local), string(sandbox.Network.Ambiguous), sandbox.TemporaryHome, sandbox.Kernel.SeccompProfile); err != nil {
		return nil, err
	}
	baseIDs, err := c.digests(lock.Base.RootFSDiffIDs)
	if err != nil {
		return nil, err
	}
	finalIDs, err := c.digests(final.Descriptor.RootFSDiffIDs)
	if err != nil {
		return nil, err
	}
	namespaces, err := c.list(sandbox.Kernel.HostNamespaces)
	if err != nil {
		return nil, err
	}
	devices, err := c.list(sandbox.Kernel.HostDevices)
	if err != nil {
		return nil, err
	}
	uid, gid := strconv.FormatUint(uint64(sandbox.RuntimeUser.UID), 10), strconv.FormatUint(uint64(sandbox.RuntimeUser.GID), 10)
	if err := c.strings(uid, gid); err != nil {
		return nil, err
	}
	// Bound collection before copying or sorting a potentially large group list.
	groups := []uint32{}
	seenGroups := map[uint32]bool{}
	for _, group := range sandbox.RuntimeUser.SupplementaryGIDs {
		if seenGroups[group] {
			continue
		}
		seenGroups[group] = true
		if err := c.strings(strconv.FormatUint(uint64(group), 10)); err != nil {
			return nil, err
		}
		groups = append(groups, group)
	}
	slices.Sort(groups)
	groupNames := []string{}
	for _, group := range slices.Compact(groups) {
		groupNames = append(groupNames, strconv.FormatUint(uint64(group), 10))
	}
	slices.SortFunc(c.artifacts.ProviderBundles, compareReportBundle)
	for i := 1; i < len(c.artifacts.ProviderBundles); i++ {
		if compareReportBundle(c.artifacts.ProviderBundles[i-1], c.artifacts.ProviderBundles[i]) == 0 {
			return nil, fmt.Errorf("duplicate verified provider artifact ownership")
		}
	}
	slices.SortFunc(c.artifacts.PortableAcquisitions, compareReportAcquisition)
	for i := 1; i < len(c.artifacts.PortableAcquisitions); i++ {
		if compareReportAcquisition(c.artifacts.PortableAcquisitions[i-1], c.artifacts.PortableAcquisitions[i]) == 0 {
			return nil, fmt.Errorf("duplicate verified portable artifact ownership")
		}
	}
	var manifest *canonical.Digest
	if lock.Base.ManifestDigest != "" {
		value := lock.Base.ManifestDigest
		manifest = &value
	}
	return &VerifiedBuildReportV1{
		Schema: "reploy:verified-build", SchemaVersion: 1, Environment: runtime.Document.Environment.ID,
		Platform: verifiedReportPlatform{lock.Platform.OS, lock.Platform.Architecture, lock.Platform.Variant},
		Build:    verifiedReportBuild{current.Generation.BuildLockDigest, current.Generation.Reference, final.Image.ConfigDigest},
		Inputs:   verifiedReportInputs{lock.BlueprintDigest, lock.ResolvedRequestDigest},
		Base:     verifiedReportBase{lock.Base.ImmutableReference, lock.Base.ConfigDigest, manifest, baseIDs},
		Image:    verifiedReportImage{finalIDs}, Artifacts: c.artifacts,
		Validation: verifiedReportValidation{validation.Schema, lock.ValidationRecord.Digest, lock.RuntimePolicy.Schema, policy},
		Runtime: verifiedReportRuntime{account, helper, verifiedReportSandbox{
			uid, gid, groupNames, verifiedReportNetwork{string(sandbox.Network.Public), string(sandbox.Network.Local), string(sandbox.Network.Ambiguous)}, sandbox.ReadOnlyRoot, sandbox.TemporaryHome,
			verifiedReportKernel{sandbox.Kernel.DropAllCapabilities, sandbox.Kernel.NoNewPrivileges, sandbox.Kernel.Privileged, sandbox.Kernel.SeccompProfile, namespaces, devices},
		}}, Checks: verifiedReportChecks{checks.StoreObjects, checks.Images, checks.Commands},
	}, nil
}
func (c *verifiedBuildReportCollector) list(values []string) ([]string, error) {
	result := []string{}
	for _, value := range values {
		if err := c.strings(value); err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	return result, nil
}
func (c *verifiedBuildReportCollector) digests(values []canonical.Digest) ([]canonical.Digest, error) {
	result := []canonical.Digest{}
	for _, value := range values {
		if err := c.strings(string(value)); err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	return result, nil
}

type verifiedReportBuffer struct{ bytes.Buffer }

func (b *verifiedReportBuffer) Write(p []byte) (int, error) {
	if len(p) > VerifiedBuildReportMaxBytes-b.Len() {
		return 0, reportSizeError()
	}
	return b.Buffer.Write(p)
}
func encodeVerifiedBuildReport(report *VerifiedBuildReportV1) ([]byte, error) {
	if report == nil {
		return nil, fmt.Errorf("verified-build JSON report is missing")
	}
	var output verifiedReportBuffer
	if err := json.NewEncoder(&output).Encode(report); err != nil {
		return nil, fmt.Errorf("encode verified-build JSON: %w", err)
	}
	return output.Bytes(), nil
}
