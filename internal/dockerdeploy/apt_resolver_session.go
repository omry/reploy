package dockerdeploy

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"sort"
	"strings"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/probe"
	"github.com/omry/reploy/internal/providers"
	aptprovider "github.com/omry/reploy/internal/providers/apt"
	"github.com/omry/reploy/internal/providerstore"
)

const aptOSReleaseProbeScriptV1 = `set -eu
. /etc/os-release
if [ "${ID+x}" = x ]; then printf 'ID\000%s\000' "$ID"; fi
if [ "${ID_LIKE+x}" = x ]; then printf 'ID_LIKE\000%s\000' "$ID_LIKE"; fi
if [ "${VERSION_ID+x}" = x ]; then printf 'VERSION_ID\000%s\000' "$VERSION_ID"; fi
`

type APTBaseValidation struct {
	Profile     aptprovider.BaseProfileEvidenceV1
	Executables []providers.ValidatedExecutableInput
}

type APTResolverSession struct {
	descriptor           deploy.ImageDescriptor
	probe                PreparedProbeWorkspace
	resolver             PreparedAPTResolverWorkspace
	containerName        string
	stdout               io.Writer
	stderr               io.Writer
	observations         map[string]probe.ExecutableObservationV1
	base                 *APTBaseValidation
	refreshAttempted     bool
	refreshed            bool
	planAttempted        bool
	planned              bool
	planRoots            []string
	planRequest          []byte
	plan                 aptprovider.ResolvePlanV1
	baseStateAttempted   bool
	baseStateVerified    bool
	baseState            []aptprovider.PackageTuple
	downloadAttempted    bool
	downloaded           bool
	downloadRoots        []string
	inventoryAttempted   bool
	inventoried          bool
	archiveInventory     []APTArchiveInventoryEntry
	inspectionAttempted  bool
	inspected            bool
	bundlePackages       []aptprovider.BundlePackage
	publicationAttempted bool
	published            bool
	publicationStoreRoot string
	bundle               aptprovider.BundleV1
	manifestAttempted    bool
	manifested           bool
	manifestStoreRoot    string
	resolveResult        providers.ResolveResult
	manifestReference    providerstore.StoreObjectRef
	closed               bool
}

// PublishResolvedBundle validates and atomically publishes the common
// resolved-bundle manifest after every selected artifact is present.
func (session *APTResolverSession) PublishResolvedBundle(ctx context.Context, store providerstore.Store, node providers.NodeSpec) (providers.ResolveResult, providerstore.StoreObjectRef, error) {
	if session == nil || session.closed {
		return providers.ResolveResult{}, providerstore.StoreObjectRef{}, fmt.Errorf("APT resolver session is not open")
	}
	if ctx == nil {
		return providers.ResolveResult{}, providerstore.StoreObjectRef{}, fmt.Errorf("APT resolved-bundle publication context is required")
	}
	if err := ctx.Err(); err != nil {
		return providers.ResolveResult{}, providerstore.StoreObjectRef{}, err
	}
	if !session.published || session.publicationStoreRoot != store.Root() {
		return providers.ResolveResult{}, providerstore.StoreObjectRef{}, fmt.Errorf("APT resolved-bundle publication requires artifacts in the selected deployment store")
	}
	if err := providers.ValidateNodeSpec(node); err != nil || node.ID != "apt" || node.Provider != blueprint.ComponentTypeAPT {
		return providers.ResolveResult{}, providerstore.StoreObjectRef{}, fmt.Errorf("APT resolved-bundle node is invalid")
	}
	requestBytes, err := providers.CanonicalProviderRequestBytes(node.Request)
	if err != nil {
		return providers.ResolveResult{}, providerstore.StoreObjectRef{}, err
	}
	if !bytes.Equal(requestBytes, session.planRequest) {
		return providers.ResolveResult{}, providerstore.StoreObjectRef{}, fmt.Errorf("APT resolved-bundle request does not match the resolver session")
	}
	if session.manifestAttempted {
		if session.manifested && session.manifestStoreRoot == store.Root() {
			cloned, err := cloneAPTResolveResult(session.resolveResult)
			return cloned, session.manifestReference, err
		}
		if session.manifested {
			return providers.ResolveResult{}, providerstore.StoreObjectRef{}, fmt.Errorf("APT resolved bundle was already published to a different deployment store")
		}
		return providers.ResolveResult{}, providerstore.StoreObjectRef{}, fmt.Errorf("APT resolved-bundle publication already failed in this resolver session")
	}
	session.manifestAttempted = true
	session.manifestStoreRoot = store.Root()
	facts, err := aptprovider.CanonicalProfileFactsV1(session.base.Profile, session.base.Executables)
	if err != nil {
		return providers.ResolveResult{}, providerstore.StoreObjectRef{}, err
	}
	profile := providers.RequirementProfile{
		Schema: providers.RequirementProfileSchemaV1, Declaration: node.Requirements,
		SelectedExecutables: []providers.ExecutableEvidence{}, SelectedFiles: []providers.FileEvidence{},
		Platform: session.descriptor.Platform, Facts: facts,
	}
	profileDigest, err := providers.RequirementProfileDigest(profile, aptprovider.ValidateRequirementProfileV1)
	if err != nil {
		return providers.ResolveResult{}, providerstore.StoreObjectRef{}, err
	}
	payloadData, err := aptprovider.CanonicalBundleDataV1(session.bundle)
	if err != nil {
		return providers.ResolveResult{}, providerstore.StoreObjectRef{}, err
	}
	artifacts := make([]providerstore.ArtifactDescriptor, 0, len(session.bundle.BundlePackages)+2)
	for _, pkg := range session.bundle.BundlePackages {
		artifacts = append(artifacts, pkg.Artifact)
	}
	artifacts = append(artifacts, session.bundle.Script, session.bundle.StateManifest)
	sort.Slice(artifacts, func(left int, right int) bool { return artifacts[left].LogicalPath < artifacts[right].LogicalPath })
	upstream, err := realizedImageFromDescriptor(session.descriptor)
	if err != nil {
		return providers.ResolveResult{}, providerstore.StoreObjectRef{}, err
	}
	outputs, err := aptprovider.ResolvedOutputsV1(node.Request, node.ID, session.bundle)
	if err != nil {
		return providers.ResolveResult{}, providerstore.StoreObjectRef{}, err
	}
	resolvedBundle, err := providers.NewResolvedBundle(providers.ResolvedBundleIdentityV1{
		Schema: providers.ResolvedBundleSchemaV1, NodeID: node.ID, Provider: node.Provider,
		Request: node.Request, RequirementProfileDigest: profileDigest, RecipeVersion: aptprovider.RecipeVersion,
		Platform: session.descriptor.Platform, Upstream: upstream,
		Artifacts: artifacts, Outputs: outputs, ProviderPayload: payloadData,
	}, aptprovider.ValidateResolvedBundlePayloadV1)
	if err != nil {
		return providers.ResolveResult{}, providerstore.StoreObjectRef{}, err
	}
	evidence, err := providers.NewValidationEvidence(upstream.RootFSSubject, profileDigest)
	if err != nil {
		return providers.ResolveResult{}, providerstore.StoreObjectRef{}, err
	}
	result := providers.ResolveResult{Bundle: resolvedBundle, Profile: profile, Evidence: evidence}
	input := providers.ResolveInput{
		Node: node, Candidates: []providers.RequirementCandidatesV1{}, Platform: session.descriptor.Platform,
		Sources: []providers.ResolvedSourceInput{}, Upstream: upstream, ReusableArtifacts: []providerstore.StoreObjectRef{},
	}
	if err := providers.ValidateResolveResult(input, result, aptprovider.ValidateRequirementProfileV1, aptprovider.ValidateResolvedBundlePayloadV1); err != nil {
		return providers.ResolveResult{}, providerstore.StoreObjectRef{}, err
	}
	reference, err := providers.PublishResolvedBundleManifest(ctx, store, resolvedBundle, aptprovider.ValidateResolvedBundlePayloadV1)
	if err != nil {
		return providers.ResolveResult{}, providerstore.StoreObjectRef{}, err
	}
	stored, err := cloneAPTResolveResult(result)
	if err != nil {
		return providers.ResolveResult{}, providerstore.StoreObjectRef{}, err
	}
	session.resolveResult = stored
	session.manifestReference = reference
	session.manifested = true
	return result, reference, nil
}

// PublishBundleArtifacts publishes only selected, fully inspected archives
// with their expected descriptors, then constructs the canonical APT payload.
// It does not publish the resolved-bundle manifest.
func (session *APTResolverSession) PublishBundleArtifacts(ctx context.Context, store providerstore.Store) (aptprovider.BundleV1, error) {
	if session == nil || session.closed {
		return aptprovider.BundleV1{}, fmt.Errorf("APT resolver session is not open")
	}
	if ctx == nil {
		return aptprovider.BundleV1{}, fmt.Errorf("APT bundle artifact publication context is required")
	}
	if err := ctx.Err(); err != nil {
		return aptprovider.BundleV1{}, err
	}
	if !session.inspected || session.base == nil {
		return aptprovider.BundleV1{}, fmt.Errorf("APT bundle artifact publication requires successful archive inspection")
	}
	if store.Root() == "" {
		return aptprovider.BundleV1{}, fmt.Errorf("APT bundle artifact publication store is required")
	}
	if session.publicationAttempted {
		if session.published && session.publicationStoreRoot == store.Root() {
			return cloneAPTBundleV1(session.bundle), nil
		}
		if session.published {
			return aptprovider.BundleV1{}, fmt.Errorf("APT bundle artifacts were already published to a different deployment store")
		}
		return aptprovider.BundleV1{}, fmt.Errorf("APT bundle artifact publication already failed in this resolver session")
	}
	inventoryByPath := make(map[string]APTArchiveInventoryEntry, len(session.archiveInventory))
	for _, archive := range session.archiveInventory {
		inventoryByPath[archive.Artifact.LogicalPath] = archive
	}
	session.publicationAttempted = true
	session.publicationStoreRoot = store.Root()
	published := cloneAPTBundlePackages(session.bundlePackages)
	for index := range published {
		archive, found := inventoryByPath[published[index].Artifact.LogicalPath]
		if !found || archive.Artifact != published[index].Artifact {
			return aptprovider.BundleV1{}, fmt.Errorf("APT selected archive %q is missing from inspected inventory", published[index].Tuple.Name)
		}
		file, err := os.Open(archive.HostPath)
		if err != nil {
			return aptprovider.BundleV1{}, fmt.Errorf("open selected APT archive %q: %w", archive.Filename, err)
		}
		descriptor, publishErr := store.PublishExpected(ctx, archive.Artifact, file)
		closeErr := file.Close()
		if publishErr != nil {
			return aptprovider.BundleV1{}, fmt.Errorf("publish selected APT archive %q: %w", archive.Filename, publishErr)
		}
		if closeErr != nil {
			return aptprovider.BundleV1{}, fmt.Errorf("close selected APT archive %q: %w", archive.Filename, closeErr)
		}
		published[index].Artifact = descriptor
	}
	bundle, err := aptprovider.NewBundleV1(session.base.Profile.NativeArchitecture, session.plan, session.baseState, published)
	if err != nil {
		return aptprovider.BundleV1{}, err
	}
	if err := aptprovider.PublishMaterializationArtifactsV1(ctx, store, bundle); err != nil {
		return aptprovider.BundleV1{}, err
	}
	session.bundlePackages = published
	session.bundle = bundle
	session.published = true
	return cloneAPTBundleV1(bundle), nil
}

// InspectArchives binds exactly the selected add/upgrade closure to inspected
// .deb metadata and streamed file-list evidence. Unrelated unchanged seeds are
// ignored; any unrelated current-run output is rejected.
func (session *APTResolverSession) InspectArchives(ctx context.Context, exclusiveRoots []string) ([]aptprovider.BundlePackage, error) {
	if session == nil || session.closed {
		return nil, fmt.Errorf("APT resolver session is not open")
	}
	if ctx == nil {
		return nil, fmt.Errorf("APT archive inspection context is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !session.inventoried || !session.baseStateVerified || session.base == nil {
		return nil, fmt.Errorf("APT archive inspection requires inventory and verified base package state")
	}
	if err := aptprovider.ValidateArchiveExclusiveRootsV1(exclusiveRoots); err != nil {
		return nil, err
	}
	if session.inspectionAttempted {
		if session.inspected {
			return cloneAPTBundlePackages(session.bundlePackages), nil
		}
		return nil, fmt.Errorf("APT archive inspection already failed in this resolver session")
	}
	selected, err := aptprovider.SelectedBundlePackagesV1(session.plan)
	if err != nil {
		return nil, err
	}
	selectedByName := make(map[string]aptprovider.ResolvePlanPackageV1, len(selected))
	for _, pkg := range selected {
		selectedByName[pkg.Name] = pkg
	}
	session.inspectionAttempted = true
	matched := map[string]bool{}
	bundles := make([]aptprovider.BundlePackage, 0, len(selected))
	for _, archive := range session.archiveInventory {
		var selectedPlan aptprovider.ResolvePlanPackageV1
		inspectFileList := false
		tuple, fileListDigest, err := session.inspectArchive(ctx, archive, exclusiveRoots, func(tuple aptprovider.PackageTuple) (bool, error) {
			plan, found := selectedByName[tuple.Name]
			if found && tuple.Version == plan.SelectedVersion {
				if matched[tuple.Name] {
					return false, fmt.Errorf("APT selected package %q has multiple archives", tuple.Name)
				}
				selectedPlan = plan
				inspectFileList = true
				return true, nil
			}
			if !archive.UnchangedSeed {
				return false, fmt.Errorf("APT download produced archive %q outside the selected package closure", archive.Filename)
			}
			return false, nil
		})
		if err != nil {
			return nil, err
		}
		if !inspectFileList {
			continue
		}
		bundle, err := aptprovider.NewBundlePackageV1(selectedPlan, tuple, archive.Artifact, fileListDigest, session.baseState)
		if err != nil {
			return nil, err
		}
		matched[tuple.Name] = true
		bundles = append(bundles, bundle)
	}
	for _, pkg := range selected {
		if !matched[pkg.Name] {
			return nil, fmt.Errorf("APT selected package %q has no downloaded archive", pkg.Name)
		}
	}
	sort.Slice(bundles, func(left int, right int) bool {
		if bundles[left].Tuple.Name != bundles[right].Tuple.Name {
			return bundles[left].Tuple.Name < bundles[right].Tuple.Name
		}
		if bundles[left].Tuple.Architecture != bundles[right].Tuple.Architecture {
			return bundles[left].Tuple.Architecture < bundles[right].Tuple.Architecture
		}
		return bundles[left].Tuple.Version < bundles[right].Tuple.Version
	})
	session.bundlePackages = bundles
	session.inspected = true
	return cloneAPTBundlePackages(bundles), nil
}

func (session *APTResolverSession) inspectArchive(
	ctx context.Context,
	archive APTArchiveInventoryEntry,
	exclusiveRoots []string,
	selectFileList func(aptprovider.PackageTuple) (bool, error),
) (aptprovider.PackageTuple, canonical.Digest, error) {
	containerPath := aptprovider.ResolveArchivesDirectory + "/" + archive.Filename
	argv, err := aptprovider.ArchiveInspectionArgvV1(containerPath)
	if err != nil {
		return aptprovider.PackageTuple{}, "", err
	}
	commandContext, cancel := context.WithCancel(ctx)
	defer cancel()
	reader, writer := io.Pipe()
	commandDone := make(chan error, 1)
	go func() {
		commandErr := session.runProfileArgvTo(commandContext, "apt.resolve.archive-inspect", argv, writer, session.stderr)
		_ = writer.CloseWithError(commandErr)
		commandDone <- commandErr
	}()
	tuple, tarStream, parseErr := aptprovider.ReadArchiveInspectionHeaderV1(reader, session.base.Profile.NativeArchitecture)
	if parseErr != nil {
		cancel()
		_ = reader.Close()
		<-commandDone
		return aptprovider.PackageTuple{}, "", parseErr
	}
	inspect, decisionErr := selectFileList(tuple)
	if decisionErr != nil {
		cancel()
		_ = reader.Close()
		<-commandDone
		return aptprovider.PackageTuple{}, "", decisionErr
	}
	var digest canonical.Digest
	var consumeErr error
	if inspect {
		digest, consumeErr = aptprovider.InspectArchiveFileListV1(ctx, tarStream, exclusiveRoots)
		if consumeErr == nil {
			_, consumeErr = io.Copy(io.Discard, tarStream)
		}
	} else {
		_, consumeErr = io.Copy(io.Discard, tarStream)
	}
	if consumeErr != nil {
		cancel()
	}
	_ = reader.Close()
	commandErr := <-commandDone
	if consumeErr != nil {
		return aptprovider.PackageTuple{}, "", consumeErr
	}
	if commandErr != nil {
		return aptprovider.PackageTuple{}, "", commandErr
	}
	if err := providerstore.VerifyArtifactFile(archive.HostPath, archive.Artifact); err != nil {
		return aptprovider.PackageTuple{}, "", fmt.Errorf("verify inspected APT archive %q: %w", archive.Filename, err)
	}
	return tuple, digest, nil
}

// InventoryArchives records the exact post-download archive-cache contents.
// It does not decide bundle membership or publish any artifact.
func (session *APTResolverSession) InventoryArchives(ctx context.Context) ([]APTArchiveInventoryEntry, error) {
	if session == nil || session.closed {
		return nil, fmt.Errorf("APT resolver session is not open")
	}
	if ctx == nil {
		return nil, fmt.Errorf("APT archive inventory context is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !session.downloaded {
		return nil, fmt.Errorf("APT archive inventory requires a successful package download")
	}
	if session.inventoryAttempted {
		if session.inventoried {
			return append([]APTArchiveInventoryEntry{}, session.archiveInventory...), nil
		}
		return nil, fmt.Errorf("APT archive inventory already failed in this resolver session")
	}
	session.inventoryAttempted = true
	inventory, err := InventoryAPTResolverArchives(ctx, session.resolver)
	if err != nil {
		return nil, err
	}
	session.archiveInventory = inventory
	session.inventoried = true
	return append([]APTArchiveInventoryEntry{}, inventory...), nil
}

// ReadBasePackageState verifies the exact installed tuple for every package
// that the dependency plan retains from, or upgrades over, the immutable base.
func (session *APTResolverSession) ReadBasePackageState(ctx context.Context) ([]aptprovider.PackageTuple, error) {
	if session == nil || session.closed {
		return nil, fmt.Errorf("APT resolver session is not open")
	}
	if ctx == nil {
		return nil, fmt.Errorf("APT base package state context is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !session.planned || session.base == nil {
		return nil, fmt.Errorf("APT base package state requires a successful resolution plan")
	}
	if session.baseStateAttempted {
		if session.baseStateVerified {
			return append([]aptprovider.PackageTuple{}, session.baseState...), nil
		}
		return nil, fmt.Errorf("APT base package state already failed in this resolver session")
	}
	names, err := aptprovider.ResolveBasePackageNamesV1(session.plan)
	if err != nil {
		return nil, err
	}
	parser, err := aptprovider.NewBasePackageStateParserV1(session.base.Profile.NativeArchitecture, session.plan)
	if err != nil {
		return nil, err
	}
	session.baseStateAttempted = true
	if len(names) != 0 {
		argv := append(aptprovider.ResolveBaseStatePrefixArgvV1(), names...)
		if err := session.runProfileArgvTo(ctx, "apt.resolve.base-state", argv, parser, session.stderr); err != nil {
			return nil, err
		}
	}
	tuples, err := parser.Finish()
	if err != nil {
		return nil, fmt.Errorf("APT base package state: %w", err)
	}
	session.baseState = tuples
	session.baseStateVerified = true
	return append([]aptprovider.PackageTuple{}, tuples...), nil
}

// PlanPackages runs one read-only APT dependency pass against the freshly
// acquired private indexes. The complete marker stream must match the
// provider-owned capability grammar; no partial plan is accepted.
func (session *APTResolverSession) PlanPackages(ctx context.Context, request providers.CanonicalProviderRequest) (aptprovider.ResolvePlanV1, error) {
	if session == nil || session.closed {
		return aptprovider.ResolvePlanV1{}, fmt.Errorf("APT resolver session is not open")
	}
	if ctx == nil {
		return aptprovider.ResolvePlanV1{}, fmt.Errorf("APT package planning context is required")
	}
	if err := ctx.Err(); err != nil {
		return aptprovider.ResolvePlanV1{}, err
	}
	roots, err := aptprovider.ResolveRootOperandsV1(request)
	if err != nil {
		return aptprovider.ResolvePlanV1{}, fmt.Errorf("APT package planning roots: %w", err)
	}
	requestBytes, err := providers.CanonicalProviderRequestBytes(request)
	if err != nil {
		return aptprovider.ResolvePlanV1{}, err
	}
	if !session.refreshed {
		return aptprovider.ResolvePlanV1{}, fmt.Errorf("APT package planning requires a successful index refresh")
	}
	if session.planAttempted {
		if session.planned && slices.Equal(session.planRoots, roots) && bytes.Equal(session.planRequest, requestBytes) {
			return cloneAPTResolvePlan(session.plan), nil
		}
		if session.planned {
			return aptprovider.ResolvePlanV1{}, fmt.Errorf("APT package planning already completed for a different canonical request")
		}
		return aptprovider.ResolvePlanV1{}, fmt.Errorf("APT package planning already failed in this resolver session")
	}
	if session.base == nil {
		return aptprovider.ResolvePlanV1{}, fmt.Errorf("APT package planning requires successful base validation")
	}
	parser, err := aptprovider.NewResolvePlanMarkerParserV1(session.base.Profile.NativeArchitecture, roots)
	if err != nil {
		return aptprovider.ResolvePlanV1{}, err
	}
	session.planAttempted = true
	session.planRoots = append([]string{}, roots...)
	session.planRequest = append([]byte{}, requestBytes...)
	argv := append(aptprovider.ResolvePlanPrefixArgvV1(), roots...)
	if err := session.runProfileArgvTo(ctx, "apt.resolve.plan", argv, session.stdout, io.MultiWriter(parser, session.stderr)); err != nil {
		return aptprovider.ResolvePlanV1{}, err
	}
	plan, err := parser.Finish()
	if err != nil {
		return aptprovider.ResolvePlanV1{}, fmt.Errorf("APT package planning capability: %w", err)
	}
	session.plan = plan
	session.planned = true
	return cloneAPTResolvePlan(plan), nil
}

// DownloadPackages runs one unsplit download-only install transaction after
// the successful private-index refresh. It does not inspect or publish the
// resulting archives; those are separate acceptance steps.
func (session *APTResolverSession) DownloadPackages(ctx context.Context, request providers.CanonicalProviderRequest) error {
	if session == nil || session.closed {
		return fmt.Errorf("APT resolver session is not open")
	}
	if ctx == nil {
		return fmt.Errorf("APT package download context is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	roots, err := aptprovider.ResolveRootOperandsV1(request)
	if err != nil {
		return fmt.Errorf("APT package download roots: %w", err)
	}
	requestBytes, err := providers.CanonicalProviderRequestBytes(request)
	if err != nil {
		return err
	}
	if !session.planned {
		return fmt.Errorf("APT package download requires a successful resolution plan")
	}
	if !session.baseStateVerified {
		return fmt.Errorf("APT package download requires verified base package state")
	}
	if !slices.Equal(session.planRoots, roots) || !bytes.Equal(session.planRequest, requestBytes) {
		return fmt.Errorf("APT package download plan was created for a different canonical request")
	}
	if session.downloadAttempted {
		if session.downloaded && slices.Equal(session.downloadRoots, roots) {
			return nil
		}
		if session.downloaded {
			return fmt.Errorf("APT package download already completed for a different root set")
		}
		return fmt.Errorf("APT package download already failed in this resolver session")
	}
	if err := validateAPTResolverDownloadWorkspace(session.resolver); err != nil {
		return fmt.Errorf("APT package download workspace: %w", err)
	}
	session.downloadAttempted = true
	session.downloadRoots = append([]string{}, roots...)
	argv := append(aptprovider.ResolveDownloadPrefixArgvV1(), roots...)
	if err := session.runProfileArgvTo(ctx, "apt.resolve.download", argv, session.stdout, session.stderr); err != nil {
		return fmt.Errorf("APT download transaction with %d root packages: %w", len(roots), err)
	}
	session.downloaded = true
	return nil
}

// RefreshIndexes runs the one apt-get update permitted for this resolver miss.
// Successful repeated calls are idempotent; a failed refresh cannot be retried
// in the same session under weaker or changed conditions.
func (session *APTResolverSession) RefreshIndexes(ctx context.Context) error {
	if session == nil || session.closed {
		return fmt.Errorf("APT resolver session is not open")
	}
	if ctx == nil {
		return fmt.Errorf("APT index refresh context is required")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if session.base == nil {
		return fmt.Errorf("APT index refresh requires successful base validation")
	}
	if session.refreshAttempted {
		if session.refreshed {
			return nil
		}
		return fmt.Errorf("APT index refresh already failed in this resolver session")
	}
	if err := validatePreparedAPTResolverWorkspace(session.resolver); err != nil {
		return fmt.Errorf("APT index refresh workspace: %w", err)
	}
	session.refreshAttempted = true
	if err := session.runProfileArgvTo(ctx, "apt.resolve.update", aptprovider.ResolveUpdateArgvV1(), session.stdout, session.stderr); err != nil {
		return err
	}
	session.refreshed = true
	return nil
}

var runAPTResolverOpenCommand = runCommand
var runAPTResolverFollowupCommand = runCommandWithoutDockerPreflight

// OpenAPTResolverSession starts the one held container that will validate the
// exact prefix and, in later typed operations, resolve its APT transaction.
func OpenAPTResolverSession(
	ctx context.Context,
	descriptor deploy.ImageDescriptor,
	probeWorkspace PreparedProbeWorkspace,
	resolverWorkspace PreparedAPTResolverWorkspace,
	options RunOptions,
) (*APTResolverSession, error) {
	if ctx == nil {
		return nil, fmt.Errorf("APT resolver session context is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := descriptor.Validate(); err != nil {
		return nil, fmt.Errorf("APT resolver descriptor: %w", err)
	}
	if descriptor.Platform.OS != "linux" {
		return nil, fmt.Errorf("APT resolver requires a Linux image")
	}
	if err := validatePreparedProbeWorkspace(descriptor, probeWorkspace); err != nil {
		return nil, err
	}
	if err := validatePreparedAPTResolverWorkspace(resolverWorkspace); err != nil {
		return nil, err
	}
	probeMount, err := dockerMountArgument("type=bind", "source="+probeWorkspace.HostDir, "target="+probeWorkspace.ContainerDir, "readonly")
	if err != nil {
		return nil, err
	}
	resolverMount, err := dockerMountArgument("type=bind", "source="+resolverWorkspace.HostDir, "target="+resolverWorkspace.ContainerDir)
	if err != nil {
		return nil, err
	}
	containerName := aptResolverContainerName(resolverWorkspace.HostDir)
	spec := CommandSpec{Name: "docker", Args: []string{
		"create", "--name", containerName,
		"--platform", descriptor.Platform.Canonical, "--pull", "never",
		"--user", "0:0", "--workdir", "/", "--read-only",
		"--network", "default",
		"--mount", probeMount, "--mount", resolverMount,
		"--entrypoint", probeWorkspace.ContainerExecutable,
		descriptor.ImmutableReference, "hold",
	}}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := runAPTResolverOpenCommand(spec, RunOptions{
		Context: ctx, Stdout: &stdout, Stderr: &stderr,
		DockerPreflightTimeout: options.DockerPreflightTimeout,
	}); err != nil {
		return nil, aptResolverCommandError("create", descriptor.Platform.Canonical, descriptor.ConfigDigest, stderr.String(), err)
	}
	stderr.Reset()
	if err := runAPTResolverFollowupCommand(CommandSpec{Name: "docker", Args: []string{"start", containerName}}, RunOptions{Context: ctx, Stdout: &stdout, Stderr: &stderr}); err != nil {
		startErr := aptResolverCommandError("start", descriptor.Platform.Canonical, descriptor.ConfigDigest, stderr.String(), err)
		cleanupErr := removeAPTResolverContainer(context.WithoutCancel(ctx), containerName)
		return nil, errors.Join(startErr, cleanupErr)
	}
	return &APTResolverSession{
		descriptor: descriptor, probe: probeWorkspace, resolver: resolverWorkspace,
		containerName: containerName, stdout: aptResolverOutputWriter(options.Stdout), stderr: aptResolverOutputWriter(options.Stderr),
		observations: map[string]probe.ExecutableObservationV1{},
	}, nil
}

func aptResolverOutputWriter(writer io.Writer) io.Writer {
	if writer == nil {
		return io.Discard
	}
	return writer
}

// ProbeBaseProfile is the fixed first resolver operation. It observes all
// required executable paths together, then invokes only their fixed read-only
// identity and architecture interfaces through the validated clean launcher.
func (session *APTResolverSession) ProbeBaseProfile(ctx context.Context) (APTBaseValidation, error) {
	if session == nil || session.closed {
		return APTBaseValidation{}, fmt.Errorf("APT resolver session is not open")
	}
	if ctx == nil {
		return APTBaseValidation{}, fmt.Errorf("APT base probe context is required")
	}
	if err := ctx.Err(); err != nil {
		return APTBaseValidation{}, err
	}
	if session.base != nil {
		return cloneAPTBaseValidation(*session.base), nil
	}

	requiredTools := aptprovider.RequiredBaseToolsV1()
	inspections := make([]probe.ExecutableInspectionV1, 0, len(requiredTools))
	for _, tool := range requiredTools {
		inspections = append(inspections, probe.ExecutableInspectionV1{ID: tool.Name, InvocationPath: tool.Path})
	}
	request := probe.RequestV1{Schema: probe.RequestSchemaV1, Inspections: inspections}
	response, err := session.runProbe(ctx, request)
	if err != nil {
		return APTBaseValidation{}, fmt.Errorf("validate APT base executables: %w", err)
	}
	for _, observation := range response.Observations {
		session.observations[observation.ID] = observation
	}

	osReleaseOutput, err := session.runProfileCommand(ctx, "/bin/sh", "-c", aptOSReleaseProbeScriptV1)
	if err != nil {
		return APTBaseValidation{}, err
	}
	osRelease, err := parseAPTOSReleaseOutputV1(osReleaseOutput)
	if err != nil {
		return APTBaseValidation{}, err
	}
	versionCommands := map[string][]string{
		"apt_get":    {"/usr/bin/apt-get", "--version"},
		"dpkg":       {"/usr/bin/dpkg", "--version"},
		"dpkg_deb":   {"/usr/bin/dpkg-deb", "--version"},
		"dpkg_query": {"/usr/bin/dpkg-query", "--version"},
		"sha256sum":  {"/usr/bin/sha256sum", "--version"},
	}
	versions := map[string]string{}
	for _, tool := range requiredTools {
		command, exists := versionCommands[tool.Name]
		if !exists {
			continue
		}
		output, err := session.runProfileCommand(ctx, command[0], command[1:]...)
		if err != nil {
			return APTBaseValidation{}, err
		}
		version, err := firstAPTOutputLine(tool.Name+" version", output)
		if err != nil {
			return APTBaseValidation{}, err
		}
		versions[tool.Name] = version
	}
	nativeOutput, err := session.runProfileCommand(ctx, "/usr/bin/dpkg", "--print-architecture")
	if err != nil {
		return APTBaseValidation{}, err
	}
	native, err := singleAPTOutputLine("native architecture", nativeOutput)
	if err != nil {
		return APTBaseValidation{}, err
	}
	foreignOutput, err := session.runProfileCommand(ctx, "/usr/bin/dpkg", "--print-foreign-architectures")
	if err != nil {
		return APTBaseValidation{}, err
	}
	foreign, err := aptOutputLines("foreign architectures", foreignOutput)
	if err != nil {
		return APTBaseValidation{}, err
	}
	for index := range requiredTools {
		requiredTools[index].Version = versions[requiredTools[index].Name]
	}
	profile, err := aptprovider.NewBaseProfileEvidenceV1(session.descriptor.Platform, osRelease, requiredTools, native, foreign)
	if err != nil {
		return APTBaseValidation{}, err
	}
	executables, err := session.bindBaseExecutables(profile.Tools)
	if err != nil {
		return APTBaseValidation{}, err
	}
	result := APTBaseValidation{Profile: profile, Executables: executables}
	session.base = &result
	return cloneAPTBaseValidation(result), nil
}

func (session *APTResolverSession) runProbe(ctx context.Context, request probe.RequestV1) (probe.ResponseV1, error) {
	encoded, err := canonical.Marshal(request)
	if err != nil {
		return probe.ResponseV1{}, err
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	spec := CommandSpec{Name: "docker", Args: []string{
		"exec", "--interactive", "--user", "0:0", "--workdir", "/",
		session.containerName, session.probe.ContainerExecutable,
	}}
	if err := runAPTResolverFollowupCommand(spec, RunOptions{Context: ctx, Stdin: bytes.NewReader(encoded), Stdout: &stdout, Stderr: io.MultiWriter(&stderr, session.stderr)}); err != nil {
		return probe.ResponseV1{}, aptResolverCommandError("probe", session.descriptor.Platform.Canonical, session.descriptor.ConfigDigest, stderr.String(), err)
	}
	response, err := probe.DecodeResponseV1(request, stdout.Bytes())
	if err != nil {
		return probe.ResponseV1{}, fmt.Errorf("decode APT resolver probe response: %w", err)
	}
	return response, nil
}

func (session *APTResolverSession) runProfileCommand(ctx context.Context, executable string, arguments ...string) ([]byte, error) {
	argv := append([]string{executable}, arguments...)
	return session.runProfileArgv(ctx, "apt.resolve.inspect", argv)
}

func (session *APTResolverSession) runProfileArgv(ctx context.Context, phase string, argv []string) ([]byte, error) {
	var stdout bytes.Buffer
	if err := session.runProfileArgvTo(ctx, phase, argv, &stdout, session.stderr); err != nil {
		return nil, err
	}
	return stdout.Bytes(), nil
}

func (session *APTResolverSession) runProfileArgvTo(ctx context.Context, phase string, argv []string, stdout io.Writer, stderr io.Writer) error {
	if len(argv) == 0 {
		return fmt.Errorf("APT resolver phase %q has no executable", phase)
	}
	executable := argv[0]
	if _, found := session.observationForPath("/usr/bin/env"); !found {
		return fmt.Errorf("APT resolver clean-environment launcher was not probed in this container")
	}
	if _, found := session.observationForPath("/bin/sh"); !found {
		return fmt.Errorf("APT resolver carrier was not probed in this container")
	}
	observation, found := session.observationForPath(executable)
	if !found {
		return fmt.Errorf("APT resolver executable %s was not probed in this container", executable)
	}
	if observation.InvocationPath != executable {
		return fmt.Errorf("APT resolver executable evidence does not match %s", executable)
	}
	args := []string{
		"exec", "--user", "0:0", "--workdir", "/", session.containerName,
		"/usr/bin/env", "-i",
	}
	profile := aptprovider.ResolveChildEnvironmentV1()
	for _, variable := range profile.Variables {
		args = append(args, variable.Name+"="+variable.Value)
	}
	args = append(args,
		"/bin/sh", "-c", `exec </dev/null; umask "$1"; shift; exec "$@"`,
		profile.Name, profile.Umask, executable,
	)
	args = append(args, argv[1:]...)
	diagnosticTail := &aptDiagnosticTail{limit: commandOutputErrorLimit}
	stderr = io.MultiWriter(aptResolverOutputWriter(stderr), diagnosticTail)
	if err := runAPTResolverFollowupCommand(CommandSpec{Name: "docker", Args: args}, RunOptions{Context: ctx, Stdout: aptResolverOutputWriter(stdout), Stderr: stderr}); err != nil {
		return aptResolverCommandError(phase, session.descriptor.Platform.Canonical, session.descriptor.ConfigDigest, diagnosticTail.String(), err)
	}
	return nil
}

type aptDiagnosticTail struct {
	limit int
	data  []byte
}

func (tail *aptDiagnosticTail) Write(input []byte) (int, error) {
	length := len(input)
	if tail.limit <= 0 {
		return length, nil
	}
	if length >= tail.limit {
		tail.data = append(tail.data[:0], input[length-tail.limit:]...)
		return length, nil
	}
	if len(tail.data)+length > tail.limit {
		drop := len(tail.data) + length - tail.limit
		copy(tail.data, tail.data[drop:])
		tail.data = tail.data[:len(tail.data)-drop]
	}
	tail.data = append(tail.data, input...)
	return length, nil
}

func (tail *aptDiagnosticTail) String() string {
	return string(tail.data)
}

func (session *APTResolverSession) observationForPath(path string) (probe.ExecutableObservationV1, bool) {
	for _, observation := range session.observations {
		if observation.InvocationPath == path {
			return observation, true
		}
	}
	return probe.ExecutableObservationV1{}, false
}

func (session *APTResolverSession) bindBaseExecutables(tools []aptprovider.RequiredToolEvidenceV1) ([]providers.ValidatedExecutableInput, error) {
	result := make([]providers.ValidatedExecutableInput, 0, len(tools))
	for _, tool := range tools {
		observation, found := session.observations[tool.Name]
		if !found {
			return nil, fmt.Errorf("APT base tool %q was not probed in this container", tool.Name)
		}
		role := providers.ExecutableRoleProviderPrerequisite
		component := "apt"
		if tool.Name == "sh" {
			role, component = providers.ExecutableRoleCarrier, "backend"
		} else if tool.Name == "env" {
			role, component = providers.ExecutableRoleEnvironmentLauncher, "backend"
		}
		requirement := providers.ExecutableRequirement{
			ID: tool.Name, Command: tool.Name, Supplier: component,
			ValidationPolicy: providers.ValidationPolicyCompatible,
		}
		evidence, err := ExecutableEvidenceFromProbe(observation, ProbeExecutableBinding{
			Requirement: &requirement,
			Output:      providers.QualifiedOutput{Component: component, Name: tool.Name},
			Facts: providers.CanonicalProviderData{Schema: "apt-required-tool-v1", Value: canonical.Object{
				"interface": tool.Interface, "version": tool.Version,
			}},
		})
		if err != nil {
			return nil, err
		}
		input := providers.ValidatedExecutableInput{ID: tool.Name, Role: role, Policy: providers.ValidationPolicyCompatible, Evidence: evidence}
		if err := providers.ValidateValidatedExecutableInput(input); err != nil {
			return nil, err
		}
		result = append(result, input)
	}
	sort.Slice(result, func(left int, right int) bool { return result[left].ID < result[right].ID })
	return result, nil
}

func (session *APTResolverSession) Close(ctx context.Context) error {
	if session == nil || session.closed {
		return nil
	}
	if ctx == nil {
		return fmt.Errorf("close APT resolver session context is required")
	}
	if err := removeAPTResolverContainer(ctx, session.containerName); err != nil {
		return err
	}
	session.closed = true
	return nil
}

func parseAPTOSReleaseOutputV1(output []byte) (map[string]string, error) {
	parts := bytes.Split(output, []byte{0})
	if len(parts) < 3 || len(parts)%2 == 0 || len(parts[len(parts)-1]) != 0 {
		return nil, fmt.Errorf("APT OS release probe returned malformed output")
	}
	result := map[string]string{}
	for index := 0; index+1 < len(parts); index += 2 {
		name, value := string(parts[index]), string(parts[index+1])
		if name != "ID" && name != "ID_LIKE" && name != "VERSION_ID" {
			return nil, fmt.Errorf("APT OS release probe returned unexpected field %q", name)
		}
		if _, exists := result[name]; exists {
			return nil, fmt.Errorf("APT OS release probe repeated field %q", name)
		}
		result[name] = value
	}
	return result, nil
}

func firstAPTOutputLine(subject string, output []byte) (string, error) {
	newline := bytes.IndexByte(output, '\n')
	if newline <= 0 {
		return "", fmt.Errorf("APT %s probe returned no value", subject)
	}
	line := string(output[:newline])
	if strings.ContainsAny(line, "\x00\r") || strings.TrimSpace(line) != line {
		return "", fmt.Errorf("APT %s probe returned malformed output", subject)
	}
	return line, nil
}

func singleAPTOutputLine(subject string, output []byte) (string, error) {
	lines, err := aptOutputLines(subject, output)
	if err != nil {
		return "", err
	}
	if len(lines) != 1 {
		return "", fmt.Errorf("APT %s probe must return exactly one value", subject)
	}
	return lines[0], nil
}

func aptOutputLines(subject string, output []byte) ([]string, error) {
	text := strings.TrimSuffix(string(output), "\n")
	if text == "" {
		return []string{}, nil
	}
	if strings.ContainsAny(text, "\x00\r") || !strings.HasSuffix(string(output), "\n") {
		return nil, fmt.Errorf("APT %s probe returned malformed output", subject)
	}
	lines := strings.Split(text, "\n")
	for _, line := range lines {
		if line == "" || strings.TrimSpace(line) != line {
			return nil, fmt.Errorf("APT %s probe returned malformed output", subject)
		}
	}
	return lines, nil
}

func cloneAPTBaseValidation(input APTBaseValidation) APTBaseValidation {
	result := input
	result.Profile.OSRelease = append([]aptprovider.OSReleaseFieldV1{}, input.Profile.OSRelease...)
	result.Profile.Tools = append([]aptprovider.RequiredToolEvidenceV1{}, input.Profile.Tools...)
	result.Profile.ForeignArchitectures = append([]string{}, input.Profile.ForeignArchitectures...)
	result.Executables = make([]providers.ValidatedExecutableInput, len(input.Executables))
	for index, executable := range input.Executables {
		result.Executables[index] = executable
		result.Executables[index].Evidence.LinkChain = append([]providers.LinkEvidence{}, executable.Evidence.LinkChain...)
		result.Executables[index].Evidence.Access.Paths = append([]providers.AccessPathEvidence{}, executable.Evidence.Access.Paths...)
	}
	return result
}

func cloneAPTResolvePlan(input aptprovider.ResolvePlanV1) aptprovider.ResolvePlanV1 {
	result := input
	result.Packages = append([]aptprovider.ResolvePlanPackageV1{}, input.Packages...)
	return result
}

func cloneAPTBundlePackages(input []aptprovider.BundlePackage) []aptprovider.BundlePackage {
	result := append([]aptprovider.BundlePackage{}, input...)
	for index := range result {
		if input[index].BasePredecessor != nil {
			value := *input[index].BasePredecessor
			result[index].BasePredecessor = &value
		}
	}
	return result
}

func cloneAPTBundleV1(input aptprovider.BundleV1) aptprovider.BundleV1 {
	result := input
	result.BasePackages = append([]aptprovider.BasePackage{}, input.BasePackages...)
	result.BundlePackages = cloneAPTBundlePackages(input.BundlePackages)
	return result
}

func cloneAPTResolveResult(input providers.ResolveResult) (providers.ResolveResult, error) {
	encoded, err := canonical.Marshal(input)
	if err != nil {
		return providers.ResolveResult{}, fmt.Errorf("clone validated APT resolve result: %w", err)
	}
	var result providers.ResolveResult
	if err := json.Unmarshal(encoded, &result); err != nil {
		return providers.ResolveResult{}, fmt.Errorf("clone validated APT resolve result: %w", err)
	}
	return result, nil
}

func aptResolverContainerName(workspace string) string {
	digest := sha256.Sum256([]byte(workspace))
	return fmt.Sprintf("reploy-apt-resolve-%x", digest[:12])
}

func removeAPTResolverContainer(ctx context.Context, containerName string) error {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := runAPTResolverFollowupCommand(CommandSpec{Name: "docker", Args: []string{"rm", "--force", containerName}}, RunOptions{Context: ctx, Stdout: &stdout, Stderr: &stderr}); err != nil {
		return aptResolverCommandError("remove", "container", "", stderr.String(), err)
	}
	return nil
}

func aptResolverCommandError(operation string, platform string, baseDigest canonical.Digest, stderr string, err error) error {
	output := trimmedCommandOutput(stderr)
	var selectedPlatform *blueprint.Platform
	if parsed, parseErr := blueprint.ParsePlatform(platform); parseErr == nil {
		selectedPlatform = &parsed
	}
	if strings.Contains(strings.ToLower(output), "exec format error") {
		return providers.NewBuildErrorV1(providers.BuildErrorV1{
			Code: "platform.mismatch", Phase: "resolve", Platform: selectedPlatform, BaseDigest: baseDigest, NodeID: "apt",
			Correction: &providers.CorrectionV1{Kind: "enable-platform-emulation"}, CauseKind: operation,
		}, err)
	}
	if errors.Is(err, context.Canceled) {
		return providers.NewBuildErrorV1(providers.BuildErrorV1{
			Code: "cancelled", Phase: "resolve", Platform: selectedPlatform, BaseDigest: baseDigest, NodeID: "apt", CauseKind: operation,
		}, context.Canceled)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return providers.NewBuildErrorV1(providers.BuildErrorV1{
			Code: "cancelled", Phase: "resolve", Platform: selectedPlatform, BaseDigest: baseDigest, NodeID: "apt", CauseKind: operation,
		}, context.DeadlineExceeded)
	}
	code := "backend.failed"
	correction := "retry-after-backend-recovery"
	switch operation {
	case "apt.resolve.update":
		code = "apt.update_failed"
		correction = "select-updatable-base"
	case "apt.resolve.plan", "apt.resolve.base-state", "apt.resolve.download", "apt.resolve.archive-inspect":
		code = "apt.resolve_failed"
		correction = "fix-apt-request-or-base"
	case "apt.resolve.inspect":
		code = "apt.resolve_failed"
		correction = "select-compatible-base"
	}
	return providers.NewBuildErrorV1(providers.BuildErrorV1{
		Code: code, Phase: "resolve", Platform: selectedPlatform, BaseDigest: baseDigest, NodeID: "apt",
		Correction: &providers.CorrectionV1{Kind: correction}, CauseKind: operation,
	}, err)
}
