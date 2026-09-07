package dockerdeploy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/omry/reploy/internal/buildprofile"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providerstore"
	"github.com/omry/reploy/internal/toolcatalog"
)

// SourceBuilderPortableToolSelectionV1 identifies one selected closure by the
// release provenance and closure identity that fix its built bytes. It is the
// only portable-tool contribution to source-build environment identity, so a
// retained sdist is reused across disposable builder images that carry the
// same selection and rebuilt when the selection changes.
type SourceBuilderPortableToolSelectionV1 struct {
	Scope                 string           `json:"scope"`
	Tool                  string           `json:"tool"`
	Version               string           `json:"version"`
	Revision              string           `json:"revision"`
	ManifestDigest        canonical.Digest `json:"manifest_digest"`
	SelectedClosureDigest canonical.Digest `json:"selected_closure_digest"`
}

type sourceBuilderCopyV1 struct {
	Source      string
	Destination string
}

// SourceBuilderPortableToolsV1 is the build-wide, already acquired and
// offline-materialized portable-tool closure set for every isolated source
// builder. Its lock is the persisted portable-tool portion of the build lock;
// its materialized tree is copied into each Python node's disposable builder
// image and removed with Cleanup after the build.
type SourceBuilderPortableToolsV1 struct {
	Plan       *SourceBuilderPortableToolPlanV1
	Lock       providers.PortableToolLockV1
	Selections []SourceBuilderPortableToolSelectionV1
	Exports    []providers.PortableToolExportV1
	Schedule   providers.PortableToolValidationScheduleV1
	workspace  string
	contextDir string
	copies     []sourceBuilderCopyV1
}

// Cleanup removes the materialized host tree. Builder images built from it
// are independent Docker artifacts removed by their own environment cleanup.
func (tools *SourceBuilderPortableToolsV1) Cleanup() error {
	if tools == nil || tools.workspace == "" {
		return nil
	}
	if err := os.RemoveAll(tools.workspace); err != nil {
		return fmt.Errorf("remove source-builder portable tool workspace: %w", err)
	}
	tools.workspace = ""
	return nil
}

var acquireSourceBuilderArtifactV1 = providerstore.AcquireArtifact
var materializeSourceBuilderArchiveV1 = providerstore.MaterializeArchive

// MaterializeSourceBuilderPortableToolsV1 acquires every selected artifact
// through the verified acquisition primitive while networking is permitted,
// locks the acquisition provenance, and only then materializes each verified
// archive offline into a host tree through the reviewed archive primitive.
// It exposes exactly the selected exports as named links and runs no tool.
func MaterializeSourceBuilderPortableToolsV1(
	ctx context.Context,
	store providerstore.Store,
	plan *SourceBuilderPortableToolPlanV1,
) (result *SourceBuilderPortableToolsV1, resultErr error) {
	if ctx == nil {
		return nil, fmt.Errorf("materialize source-builder portable tools requires a context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if plan == nil {
		return nil, fmt.Errorf("materialize source-builder portable tools requires a plan")
	}
	if err := providers.ValidatePortableToolProviderDAGV1(plan.DAG); err != nil {
		return nil, fmt.Errorf("source-builder portable tool plan: %w", err)
	}
	records, err := toolcatalog.EmbeddedPortableToolLockRecordsV1(plan.Closures)
	if err != nil {
		return nil, fmt.Errorf("source-builder portable tool lock records: %w", err)
	}
	workspace, err := store.NewWorkspace("source-builder-*")
	if err != nil {
		return nil, err
	}
	tools := &SourceBuilderPortableToolsV1{Plan: plan, workspace: workspace, contextDir: filepath.Join(workspace, "context")}
	defer func() {
		if resultErr != nil {
			resultErr = errors.Join(resultErr, tools.Cleanup())
		}
	}()
	rootfs := filepath.Join(tools.contextDir, "rootfs")
	if err := os.MkdirAll(rootfs, 0o700); err != nil {
		return nil, fmt.Errorf("create source-builder materialization root: %w", err)
	}

	acquired := make(map[providers.PortableToolRecordReferenceV1]providerstore.ArtifactDescriptor, len(records.Artifacts))
	acquisitions := make([]providers.PortableToolArtifactAcquisitionInputV1, 0, len(records.Artifacts))
	for _, artifact := range records.Artifacts {
		acquireCtx, endAcquire := buildprofile.Start(ctx, "Acquire portable tool artifact: "+artifact.Descriptor.LogicalPath)
		outcome, err := acquireSourceBuilderArtifactV1(acquireCtx, store, providerstore.AcquisitionRequest{
			Artifact: artifact.Descriptor,
			Source:   providerstore.ArtifactSource{ID: artifact.Source.Reference.ID, SHA256: artifact.Descriptor.SHA256, Mirrors: artifact.Mirrors},
			Policy:   providerstore.DefaultAcquisitionPolicy(),
		})
		endAcquire(err)
		if err != nil {
			return nil, fmt.Errorf("acquire portable tool artifact %s: %w", artifact.Artifact.ID, err)
		}
		if outcome.Artifact != artifact.Descriptor {
			return nil, fmt.Errorf("acquired portable tool artifact %s does not match its selected descriptor", artifact.Artifact.ID)
		}
		acquired[artifact.Artifact] = outcome.Artifact
		acquisitions = append(acquisitions, providers.PortableToolArtifactAcquisitionInputV1{
			Scope: artifact.Scope, Tool: artifact.Tool, Artifact: artifact.Artifact,
			Descriptor: outcome.Artifact, Source: artifact.Source, Provenance: outcome.Provenance,
		})
	}
	lock, err := providers.BuildPortableToolLockV1(plan.DAG, records.Releases, acquisitions)
	if err != nil {
		return nil, fmt.Errorf("lock source-builder portable tools: %w", err)
	}
	tools.Lock = lock
	schedule, err := providers.PortableToolValidationScheduleFromLockV1(lock)
	if err != nil {
		return nil, err
	}
	tools.Schedule = schedule

	exports := map[string]providers.PortableToolExportV1{}
	destinations := map[string]canonical.Digest{}
	for _, entry := range plan.Plan.Tools {
		closure, err := sourceBuilderClosureForEntryV1(plan.Closures, entry)
		if err != nil {
			return nil, err
		}
		installRoot, err := sourceBuilderInstallRootV1(entry.Exports, closure.Records.Payloads)
		if err != nil {
			return nil, fmt.Errorf("source-builder %s/%s: %w", entry.Scope, entry.Provenance.Tool, err)
		}
		for _, payload := range closure.Records.Payloads {
			descriptor, found := acquired[providers.PortableToolRecordReferenceV1{ID: payload.Reference.ID, Digest: payload.Reference.Digest}]
			if !found {
				return nil, fmt.Errorf("payload %s was not acquired before materialization", payload.Reference.ID)
			}
			format, err := sourceBuilderArchiveFormatV1(payload.Record.LogicalPath)
			if err != nil {
				return nil, fmt.Errorf("payload %s: %w", payload.Reference.ID, err)
			}
			destination := path.Join(installRoot, payload.Record.InstallDirectory)
			materialize, err := sourceBuilderClaimDestinationV1(destinations, destination, descriptor.SHA256)
			if err != nil {
				return nil, fmt.Errorf("payload %s: %w", payload.Reference.ID, err)
			}
			if !materialize {
				// Another source-builder scope selected the identical payload
				// for the same destination; composition rule 9 deduplicates it.
				continue
			}
			destinationRoot := filepath.Join(rootfs, filepath.FromSlash(installRoot))
			if err := os.MkdirAll(destinationRoot, 0o700); err != nil {
				return nil, fmt.Errorf("create source-builder install root %s: %w", installRoot, err)
			}
			materializeCtx, endMaterialize := buildprofile.Start(ctx, "Materialize portable tool payload: "+payload.Record.Name)
			_, err = materializeSourceBuilderArchiveV1(materializeCtx, store, providerstore.ArchiveMaterializationRequest{
				Artifact: descriptor, Format: format, DestinationRoot: destinationRoot,
				InstallDirectory: payload.Record.InstallDirectory, ArchiveRoot: payload.Record.ArchiveRoot,
				ExpectedEntryCount: payload.Record.Entries, ExpectedUnpackedSize: payload.Record.UnpackedSize,
				ExecutablePaths: append([]string{}, payload.Record.Executables...),
			})
			endMaterialize(err)
			if err != nil {
				return nil, fmt.Errorf("materialize payload %s: %w", payload.Reference.ID, err)
			}
			tools.copies = append(tools.copies, sourceBuilderCopyV1{
				Source: path.Join("rootfs", destination), Destination: destination,
			})
		}
		for _, export := range entry.Exports {
			if previous, exists := exports[export.Name]; exists {
				if previous.Path != export.Path {
					return nil, fmt.Errorf("source-builder export %q resolves to both %s and %s", export.Name, previous.Path, export.Path)
				}
				continue
			}
			exports[export.Name] = export
		}
		tools.Selections = append(tools.Selections, SourceBuilderPortableToolSelectionV1{
			Scope: entry.Scope, Tool: entry.Provenance.Tool, Version: entry.Provenance.Version,
			Revision: entry.Provenance.Revision, ManifestDigest: entry.Provenance.ManifestDigest,
			SelectedClosureDigest: entry.SelectedClosureDigest,
		})
	}
	tools.Exports, err = sourceBuilderExposeExportsV1(rootfs, exports)
	if err != nil {
		return nil, err
	}
	if len(tools.Exports) != 0 {
		tools.copies = append(tools.copies, sourceBuilderCopyV1{
			Source: path.Join("rootfs", SourceBuilderExportsDirectoryV1), Destination: SourceBuilderExportsDirectoryV1,
		})
	}
	return tools, nil
}

// sourceBuilderClaimDestinationV1 records which artifact owns one image
// destination. The same artifact selected again for that destination is a
// deduplicated contribution that must not be materialized twice; a different
// artifact for the same destination is a contribution conflict.
func sourceBuilderClaimDestinationV1(claims map[string]canonical.Digest, destination string, artifact canonical.Digest) (bool, error) {
	if !path.IsAbs(destination) || path.Clean(destination) != destination || destination == "/" {
		return false, fmt.Errorf("materialization destination %q must be an absolute clean path", destination)
	}
	if err := artifact.Validate(); err != nil {
		return false, fmt.Errorf("materialization destination %s artifact: %w", destination, err)
	}
	if previous, claimed := claims[destination]; claimed {
		if previous != artifact {
			return false, fmt.Errorf("destination %s is selected for both %s and %s", destination, previous, artifact)
		}
		return false, nil
	}
	claims[destination] = artifact
	return true, nil
}

func sourceBuilderClosureForEntryV1(closures []toolcatalog.SelectedClosureV1, entry providers.PortableToolPlanEntryV1) (*toolcatalog.SelectedClosureV1, error) {
	for index := range closures {
		closure := &closures[index]
		if closure.Scope == entry.Scope && closure.Provenance.Tool == entry.Provenance.Tool && closure.Identity == entry.SelectedClosureDigest {
			return closure, nil
		}
	}
	return nil, fmt.Errorf("plan entry %s/%s has no selected closure", entry.Scope, entry.Provenance.Tool)
}

// sourceBuilderInstallRootV1 derives the absolute directory beneath which the
// selected payloads install from the selected data itself: every contract
// export path must equal the install root joined with one payload's installed
// executable path, and every export must agree on one root.
func sourceBuilderInstallRootV1(exports []providers.PortableToolExportV1, payloads []toolcatalog.SelectedPayloadRecordV1) (string, error) {
	if len(exports) == 0 {
		return "", fmt.Errorf("selected closure exports nothing, so no install root can be derived")
	}
	root := ""
	for _, export := range exports {
		derived := ""
		for _, payload := range payloads {
			for _, executable := range payload.Record.Executables {
				relative := strings.TrimPrefix(executable, payload.Record.ArchiveRoot+"/")
				if payload.Record.ArchiveRoot == "" || payload.Record.ArchiveRoot == "." {
					relative = executable
				}
				suffix := "/" + path.Join(payload.Record.InstallDirectory, relative)
				if strings.HasSuffix(export.Path, suffix) {
					derived = strings.TrimSuffix(export.Path, suffix)
					break
				}
			}
			if derived != "" {
				break
			}
		}
		if derived == "" || !path.IsAbs(derived) || path.Clean(derived) != derived || derived == "/" {
			return "", fmt.Errorf("export %q at %s does not name a selected payload executable beneath one install root", export.Name, export.Path)
		}
		if root != "" && derived != root {
			return "", fmt.Errorf("exports derive conflicting install roots %s and %s", root, derived)
		}
		root = derived
	}
	return root, nil
}

func sourceBuilderArchiveFormatV1(logicalPath string) (providerstore.ArchiveFormat, error) {
	switch {
	case strings.HasSuffix(logicalPath, ".tar.gz"), strings.HasSuffix(logicalPath, ".tgz"):
		return providerstore.ArchiveFormatTarGz, nil
	case strings.HasSuffix(logicalPath, ".zip"):
		return providerstore.ArchiveFormatZip, nil
	default:
		return "", fmt.Errorf("artifact %q is not a supported portable archive", logicalPath)
	}
}

// sourceBuilderExposeExportsV1 links exactly the selected export names to
// their contract paths after proving each target was materialized as an
// executable regular file. Nothing else beneath the install root is exposed.
func sourceBuilderExposeExportsV1(rootfs string, exports map[string]providers.PortableToolExportV1) ([]providers.PortableToolExportV1, error) {
	names := make([]string, 0, len(exports))
	for name := range exports {
		names = append(names, name)
	}
	sort.Strings(names)
	result := make([]providers.PortableToolExportV1, 0, len(names))
	if len(names) == 0 {
		return result, nil
	}
	exportsDir := filepath.Join(rootfs, filepath.FromSlash(SourceBuilderExportsDirectoryV1))
	if err := os.MkdirAll(exportsDir, 0o755); err != nil {
		return nil, fmt.Errorf("create source-builder exports directory: %w", err)
	}
	for _, name := range names {
		export := exports[name]
		if name == "" || strings.ContainsAny(name, "/\x00") || name == "." || name == ".." {
			return nil, fmt.Errorf("source-builder export name %q is not a single path component", name)
		}
		if !path.IsAbs(export.Path) || path.Clean(export.Path) != export.Path {
			return nil, fmt.Errorf("source-builder export %q path %q must be absolute and clean", name, export.Path)
		}
		info, err := os.Lstat(filepath.Join(rootfs, filepath.FromSlash(export.Path)))
		if err != nil {
			return nil, fmt.Errorf("source-builder export %q target %s was not materialized: %w", name, export.Path, err)
		}
		if !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
			return nil, fmt.Errorf("source-builder export %q target %s is not an executable regular file", name, export.Path)
		}
		if err := os.Symlink(export.Path, filepath.Join(exportsDir, name)); err != nil {
			return nil, fmt.Errorf("expose source-builder export %q: %w", name, err)
		}
		result = append(result, export)
	}
	return result, nil
}

// sourceBuilderDockerfileV1 renders the COPY-only layer that places the
// offline-materialized host tree into a disposable builder image. It runs no
// command inside the image and changes no image configuration.
func sourceBuilderDockerfileV1(copies []sourceBuilderCopyV1) ([]byte, error) {
	if len(copies) == 0 {
		return nil, fmt.Errorf("source-builder layer requires at least one materialized tree")
	}
	var output bytes.Buffer
	fmt.Fprintf(&output, "# syntax=%s\n", MaterializationDockerfileSyntax)
	output.WriteString("ARG REPLOY_BASE_IMAGE=scratch\n")
	output.WriteString("FROM ${REPLOY_BASE_IMAGE}\n")
	seen := map[string]struct{}{}
	for _, copy := range copies {
		if copy.Source == "" || path.IsAbs(copy.Source) || path.Clean(copy.Source) != copy.Source || strings.HasPrefix(copy.Source, "..") {
			return nil, fmt.Errorf("source-builder copy source %q must be a clean context-relative path", copy.Source)
		}
		if !path.IsAbs(copy.Destination) || path.Clean(copy.Destination) != copy.Destination || copy.Destination == "/" {
			return nil, fmt.Errorf("source-builder copy destination %q must be an absolute clean path", copy.Destination)
		}
		if _, exists := seen[copy.Destination]; exists {
			return nil, fmt.Errorf("source-builder copy destination %q repeats", copy.Destination)
		}
		seen[copy.Destination] = struct{}{}
		operands, err := json.Marshal([]string{copy.Source, copy.Destination})
		if err != nil {
			return nil, err
		}
		if bytes.ContainsAny(operands, "\n\r") {
			return nil, fmt.Errorf("source-builder copy operands contain line breaks")
		}
		fmt.Fprintf(&output, "COPY --chown=0:0 %s\n", operands)
	}
	return output.Bytes(), nil
}

// SourceBuilderEnvironmentV1 is one Python node's prepared, inspected, and
// validated disposable source-builder image together with the exact exports
// and recipes it was prepared for.
type SourceBuilderEnvironmentV1 struct {
	Descriptor       deploy.ImageDescriptor
	Upstream         deploy.ImageDescriptor
	Image            InspectedImageCandidate
	Exports          []providers.PortableToolExportV1
	ExportsDirectory string
	Selections       []SourceBuilderPortableToolSelectionV1
	Recipes          map[string]SourceBuilderRecipeIdentityV1
	Schedule         providers.PortableToolValidationScheduleV1
	Evidence         []providers.ValidationEvidence
	candidate        BuiltImageCandidate
	removed          bool
}

var buildSourceBuilderLayerV1 = buildSourceBuilderLayer
var requireSourceBuilderDestinationsAbsentV1 = requireSourceBuilderDestinationsAbsent
var inspectSourceBuilderLayerV1 = InspectBuiltImageCandidate
var validateSourceBuilderMaterializationV1 = ValidatePortableToolMaterializationV1
var removeSourceBuilderLayerV1 = RemoveBuiltImageCandidate

// Cleanup removes the disposable builder image. It is idempotent.
func (environment *SourceBuilderEnvironmentV1) Cleanup(ctx context.Context) error {
	if environment == nil || environment.removed {
		return nil
	}
	if err := removeSourceBuilderLayerV1(ctx, environment.candidate); err != nil {
		return fmt.Errorf("remove source-builder image: %w", err)
	}
	environment.removed = true
	return nil
}

// PrepareSourceBuilderEnvironmentV1 builds one Python node's disposable
// builder image from the node's prefix image and the shared materialized
// tree, inspects it, and runs every scheduled build-scope validation profile
// against that exact image through the PTD-21.5 boundary before any consumer
// opens it. A failed probe removes the image and fails the build.
func PrepareSourceBuilderEnvironmentV1(
	ctx context.Context,
	store providerstore.Store,
	tools *SourceBuilderPortableToolsV1,
	upstream deploy.ImageDescriptor,
	options RunOptions,
) (*SourceBuilderEnvironmentV1, error) {
	if ctx == nil {
		return nil, fmt.Errorf("prepare source-builder environment requires a context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if tools == nil || tools.Plan == nil || tools.workspace == "" {
		return nil, fmt.Errorf("prepare source-builder environment requires materialized portable tools")
	}
	if err := upstream.Validate(); err != nil {
		return nil, fmt.Errorf("source-builder upstream descriptor: %w", err)
	}
	dockerfile, err := sourceBuilderDockerfileV1(tools.copies)
	if err != nil {
		return nil, err
	}
	options.Context = ctx
	// Design "Safe Materialization": selected destinations are collision-checked
	// before extraction. The COPY layer must not merge over bytes the prefix
	// image already holds at any destination.
	destinations := make([]string, 0, len(tools.copies))
	for _, copy := range tools.copies {
		destinations = append(destinations, copy.Destination)
	}
	collisionCtx, endCollision := buildprofile.Start(ctx, "Check source-builder destinations are absent")
	err = requireSourceBuilderDestinationsAbsentV1(collisionCtx, store, upstream, destinations)
	endCollision(err)
	if err != nil {
		return nil, fmt.Errorf("source-builder destination collision check: %w", err)
	}
	buildCtx, endBuild := buildprofile.Start(ctx, "Build source-builder image")
	candidate, err := buildSourceBuilderLayerV1(buildCtx, store, upstream, tools.contextDir, dockerfile, options)
	endBuild(err)
	if err != nil {
		return nil, err
	}
	environment := &SourceBuilderEnvironmentV1{
		Upstream: upstream, candidate: candidate,
		Exports: append([]providers.PortableToolExportV1{}, tools.Exports...), ExportsDirectory: SourceBuilderExportsDirectoryV1,
		Selections: append([]SourceBuilderPortableToolSelectionV1{}, tools.Selections...),
		Recipes:    tools.Plan.Recipes, Schedule: tools.Schedule,
	}
	fail := func(err error) (*SourceBuilderEnvironmentV1, error) {
		if cleanupErr := environment.Cleanup(context.WithoutCancel(ctx)); cleanupErr != nil {
			return nil, errors.Join(err, cleanupErr)
		}
		return nil, err
	}
	inspected, err := inspectSourceBuilderLayerV1(ctx, candidate, upstream.Platform)
	if err != nil {
		return fail(fmt.Errorf("inspect source-builder image: %w", err))
	}
	if err := ValidateInspectedImageCandidateIdentity(inspected); err != nil {
		return fail(fmt.Errorf("source-builder image: %w", err))
	}
	if inspected.Descriptor.Platform != upstream.Platform {
		return fail(fmt.Errorf("source-builder image platform %s does not match upstream %s", inspected.Descriptor.Platform.Canonical, upstream.Platform.Canonical))
	}
	environment.Descriptor = inspected.Descriptor
	environment.Image = inspected
	validateCtx, endValidate := buildprofile.Start(ctx, "Validate source-builder portable tools")
	evidence, err := validateSourceBuilderMaterializationV1(validateCtx, store, PortableToolMaterializationValidationInputV1{
		Image: inspected, Schedule: tools.Schedule,
	})
	endValidate(err)
	if err != nil {
		return fail(fmt.Errorf("validate source-builder portable tools: %w", err))
	}
	environment.Evidence = evidence
	return environment, nil
}

// requireSourceBuilderDestinationsAbsent proves through one networkless
// validation session on the upstream image that no copy destination already
// exists there, so the COPY layer cannot merge over pre-existing bytes.
func requireSourceBuilderDestinationsAbsent(
	ctx context.Context,
	store providerstore.Store,
	upstream deploy.ImageDescriptor,
	destinations []string,
) (resultErr error) {
	if len(destinations) == 0 {
		return fmt.Errorf("source-builder collision check requires at least one destination")
	}
	workspace, cleanupWorkspace, err := prepareSourceBuilderProbeWorkspaceV1(ctx, store, upstream.Platform)
	if err != nil {
		return err
	}
	defer func() {
		if providerHelperCleanupFailed(resultErr) {
			return
		}
		if err := cleanupWorkspace(); err != nil {
			resultErr = errors.Join(resultErr, err)
		}
	}()
	session, err := openSourceBuilderCollisionSessionV1(ctx, upstream, workspace)
	if err != nil {
		return err
	}
	defer func() {
		if err := session.Close(context.WithoutCancel(ctx)); err != nil {
			resultErr = errors.Join(resultErr, err)
		}
	}()
	return session.ValidatePathsAbsent(ctx, destinations)
}

var openSourceBuilderCollisionSessionV1 = OpenImageValidationSession

// buildSourceBuilderLayer runs one Docker build of the COPY-only layer with
// the same temporary base and output reference mechanics as provider
// materialization layers.
func buildSourceBuilderLayer(
	ctx context.Context,
	store providerstore.Store,
	upstream deploy.ImageDescriptor,
	contextDir string,
	dockerfile []byte,
	options RunOptions,
) (result BuiltImageCandidate, resultErr error) {
	image, err := realizedImageFromDescriptor(upstream)
	if err != nil {
		return BuiltImageCandidate{}, err
	}
	workspace, err := store.NewWorkspace("build-*")
	if err != nil {
		return BuiltImageCandidate{}, err
	}
	preserveWorkspace := false
	defer func() {
		if !preserveWorkspace {
			_ = os.RemoveAll(workspace)
		}
	}()
	baseReference, cleanupBaseReference, err := prepareTemporaryBuildBaseReference(
		ctx, store.Root(), workspace, image, runMaterializationBuildReferenceDocker,
	)
	if err != nil {
		return BuiltImageCandidate{}, err
	}
	defer func() {
		if cleanupErr := cleanupTemporaryBuildBaseReferenceAfterBuild(
			context.WithoutCancel(ctx), cleanupBaseReference, result, runMaterializationBuildReferenceDocker,
		); cleanupErr != nil {
			preserveWorkspace = true
			if resultErr != nil {
				resultErr = fmt.Errorf("%w; cleanup temporary build base reference: %v", resultErr, cleanupErr)
			} else {
				result = BuiltImageCandidate{}
				resultErr = fmt.Errorf("cleanup temporary build base reference: %w", cleanupErr)
			}
		}
	}()
	outputReference, err := prepareTemporaryBuildOutputReference(ctx, store.Root(), workspace, runMaterializationBuildReferenceDocker)
	if err != nil {
		return BuiltImageCandidate{}, err
	}
	defer func() {
		if resultErr == nil {
			return
		}
		if cleanupErr := removeTemporaryBuildReference(
			context.WithoutCancel(ctx), outputReference, "", runMaterializationBuildReferenceDocker,
		); cleanupErr != nil {
			preserveWorkspace = true
			resultErr = fmt.Errorf("%w; cleanup temporary build output reference: %v", resultErr, cleanupErr)
		}
	}()
	dockerfilePath := filepath.Join(workspace, "Dockerfile")
	if err := os.WriteFile(dockerfilePath, dockerfile, 0o600); err != nil {
		return BuiltImageCandidate{}, fmt.Errorf("write source-builder Dockerfile: %w", err)
	}
	iidPath := filepath.Join(workspace, "result.iid")
	command, err := MaterializationBuildCommand(MaterializationBuildPlan{
		BaseReference: baseReference, OutputReference: outputReference, Platform: upstream.Platform,
		DockerfilePath: dockerfilePath, ContextDir: contextDir, IIDFile: iidPath, NoCache: options.NoCache,
	})
	if err != nil {
		return BuiltImageCandidate{}, err
	}
	if err := runMaterializationBuildCommand(command, options); err != nil {
		return BuiltImageCandidate{}, fmt.Errorf("build source-builder image: %w", err)
	}
	content, err := os.ReadFile(iidPath)
	if err != nil {
		return BuiltImageCandidate{}, fmt.Errorf("read source-builder image ID: %w", err)
	}
	imageDigest := canonical.Digest(strings.TrimSpace(string(content)))
	if err := imageDigest.Validate(); err != nil {
		return BuiltImageCandidate{}, fmt.Errorf("source-builder image ID: %w", err)
	}
	preserveWorkspace = true
	return BuiltImageCandidate{ImageID: imageDigest, TemporaryReference: outputReference, Workspace: workspace}, nil
}
