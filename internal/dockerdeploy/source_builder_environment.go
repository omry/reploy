package dockerdeploy

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/omry/reploy/internal/buildprofile"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providerstore"
	"github.com/omry/reploy/internal/toolcatalog"
)

// SourceBuilderPortableToolSelectionV1 identifies one selected closure by the
// release provenance and closure identity that fix its built bytes. It is the
// only portable-tool contribution to source-build environment identity. The
// complete identity also binds the exact prepared builder image and
// interpreter, so reuse requires those inputs and the selection to remain
// unchanged.
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

const sourceBuilderExportsArchiveV1 = "reploy-source-builder-exports.tar"

// SourceBuilderPortableToolsV1 is the build-wide, already acquired and
// offline-materialized portable-tool closure set for every isolated source
// builder. Its lock is the persisted portable-tool portion of the build lock;
// its materialized tree is copied into each Python node's disposable builder
// image and removed with Cleanup after the build.
type SourceBuilderPortableToolsV1 struct {
	Plan           *SourceBuilderPortableToolPlanV1
	Lock           providers.PortableToolLockV1
	Selections     []SourceBuilderPortableToolSelectionV1
	Exports        []providers.PortableToolExportV1
	workspace      string
	contextDir     string
	copies         []sourceBuilderCopyV1
	exportsArchive string
}

// Cleanup removes the materialized host tree. Builder images built from it
// are independent Docker artifacts removed by their own environment cleanup.
func (tools *SourceBuilderPortableToolsV1) Cleanup() error {
	if tools == nil || tools.workspace == "" {
		return nil
	}
	if err := removeSourceBuilderPortableToolWorkspaceV1(tools.workspace); err != nil {
		return fmt.Errorf("remove source-builder portable tool workspace: %w", err)
	}
	tools.workspace = ""
	return nil
}

func removeSourceBuilderPortableToolWorkspaceV1(workspace string) error {
	root, err := os.OpenRoot(workspace)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("open workspace for cleanup: %w", err)
	}
	walkErr := fs.WalkDir(root.FS(), ".", func(relative string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := entry.Info()
		if err != nil {
			return fmt.Errorf("inspect workspace entry %s: %w", relative, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil
		}
		mode := os.FileMode(0o600)
		if info.IsDir() {
			mode = 0o700
		}
		if err := root.Chmod(filepath.FromSlash(relative), mode); err != nil {
			return fmt.Errorf("prepare workspace entry %s for cleanup: %w", relative, err)
		}
		return nil
	})
	closeErr := root.Close()
	if walkErr != nil || closeErr != nil {
		return errors.Join(walkErr, closeErr)
	}
	return os.RemoveAll(workspace)
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

	acquired, acquisitions, err := acquireSourceBuilderPortableToolArtifactsV1(ctx, store, records)
	if err != nil {
		return nil, err
	}
	lock, err := providers.BuildPortableToolLockV1(plan.DAG, records.Releases, acquisitions)
	if err != nil {
		return nil, fmt.Errorf("lock source-builder portable tools: %w", err)
	}
	tools.Lock = lock
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
			symbolicLinkPolicy, err := sourceBuilderSymbolicLinkPolicyV1(payload.Record.SymbolicLinkPolicy)
			if err != nil {
				return nil, fmt.Errorf("payload %s: %w", payload.Reference.ID, err)
			}
			_, err = materializeSourceBuilderArchiveV1(materializeCtx, store, providerstore.ArchiveMaterializationRequest{
				Artifact: descriptor, Format: format, DestinationRoot: destinationRoot,
				InstallDirectory: payload.Record.InstallDirectory, ArchiveRoot: payload.Record.ArchiveRoot,
				ExpectedEntryCount: payload.Record.Entries, ExpectedUnpackedSize: payload.Record.UnpackedSize,
				ExecutablePaths:    append([]string{}, payload.Record.Executables...),
				SymbolicLinkPolicy: symbolicLinkPolicy,
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
	tools.Exports, tools.exportsArchive, err = sourceBuilderExposeExportsV1(rootfs, tools.contextDir, exports)
	if err != nil {
		return nil, err
	}
	return tools, nil
}

// acquireSourceBuilderPortableToolArtifactsV1 acquires each selected payload
// or binding wheel once through the common verified store path. The embedded
// projection has already authorized all manifest and source mappings.
func acquireSourceBuilderPortableToolArtifactsV1(
	ctx context.Context,
	store providerstore.Store,
	records toolcatalog.EmbeddedPortableToolLockRecordSetV1,
) (map[providers.PortableToolRecordReferenceV1]providerstore.ArtifactDescriptor, []providers.PortableToolArtifactAcquisitionInputV1, error) {
	selected := make(map[providers.PortableToolRecordReferenceV1]toolcatalog.EmbeddedPortableToolArtifactSourceV1, len(records.Artifacts))
	for _, artifact := range records.Artifacts {
		if previous, exists := selected[artifact.Artifact]; exists {
			if previous.Descriptor != artifact.Descriptor || previous.Source.Reference != artifact.Source.Reference ||
				!slices.Equal(previous.Mirrors, artifact.Mirrors) {
				return nil, nil, fmt.Errorf("selected portable tool artifact %s has conflicting descriptors or sources", artifact.Artifact.ID)
			}
			continue
		}
		selected[artifact.Artifact] = artifact
	}
	acquired := make(map[providers.PortableToolRecordReferenceV1]providerstore.ArtifactDescriptor, len(records.Artifacts))
	acquisitions := make([]providers.PortableToolArtifactAcquisitionInputV1, 0, len(records.Artifacts))
	outcomes := make(map[providers.PortableToolRecordReferenceV1]providerstore.AcquisitionResult, len(selected))
	for _, artifact := range records.Artifacts {
		outcome, exists := outcomes[artifact.Artifact]
		if !exists {
			acquireCtx, endAcquire := buildprofile.Start(ctx, "Acquire portable tool artifact: "+artifact.Descriptor.LogicalPath)
			var err error
			outcome, err = acquireSourceBuilderArtifactV1(acquireCtx, store, providerstore.AcquisitionRequest{
				Artifact: artifact.Descriptor,
				Source:   providerstore.ArtifactSource{ID: artifact.Source.Reference.ID, SHA256: artifact.Descriptor.SHA256, Mirrors: artifact.Mirrors},
				Policy:   providerstore.DefaultAcquisitionPolicy(),
			})
			endAcquire(err)
			if err != nil {
				return nil, nil, fmt.Errorf("acquire portable tool artifact %s: %w", artifact.Artifact.ID, err)
			}
			if outcome.Artifact != artifact.Descriptor {
				return nil, nil, fmt.Errorf("acquired portable tool artifact %s does not match its selected descriptor", artifact.Artifact.ID)
			}
			outcomes[artifact.Artifact] = outcome
		}
		acquired[artifact.Artifact] = outcome.Artifact
		acquisitions = append(acquisitions, providers.PortableToolArtifactAcquisitionInputV1{
			Scope: artifact.Scope, Tool: artifact.Tool, Artifact: artifact.Artifact,
			Descriptor: outcome.Artifact, Source: artifact.Source, Provenance: outcome.Provenance,
		})
	}
	return acquired, acquisitions, nil
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

func sourceBuilderSymbolicLinkPolicyV1(policy string) (providerstore.ArchiveSymbolicLinkPolicy, error) {
	switch policy {
	case toolcatalog.PayloadSymbolicLinkPolicyRejectV1:
		return providerstore.ArchiveSymbolicLinkPolicyReject, nil
	case toolcatalog.PayloadSymbolicLinkPolicyMaterializeRegularTargetV1:
		return providerstore.ArchiveSymbolicLinkPolicyMaterializeRegularTarget, nil
	default:
		return "", fmt.Errorf("unsupported symbolic-link policy %q", policy)
	}
}

// sourceBuilderExposeExportsV1 encodes exactly the selected export names as
// Linux symbolic links in a deterministic tar layer after proving each target
// was materialized as a regular file. It never asks the host to create a
// symbolic link, so Windows privilege and path semantics cannot affect the
// resulting builder image. Executability is an archive contract validated by
// the reviewed materializer and then proved in that Linux image.
func sourceBuilderExposeExportsV1(rootfs, contextDir string, exports map[string]providers.PortableToolExportV1) ([]providers.PortableToolExportV1, string, error) {
	names := make([]string, 0, len(exports))
	for name := range exports {
		names = append(names, name)
	}
	sort.Strings(names)
	result := make([]providers.PortableToolExportV1, 0, len(names))
	if len(names) == 0 {
		return result, "", nil
	}
	for _, name := range names {
		export := exports[name]
		if name == "" || strings.ContainsAny(name, "/\x00") || name == "." || name == ".." {
			return nil, "", fmt.Errorf("source-builder export name %q is not a single path component", name)
		}
		if !path.IsAbs(export.Path) || path.Clean(export.Path) != export.Path {
			return nil, "", fmt.Errorf("source-builder export %q path %q must be absolute and clean", name, export.Path)
		}
		info, err := os.Lstat(filepath.Join(rootfs, filepath.FromSlash(export.Path)))
		if err != nil {
			return nil, "", fmt.Errorf("source-builder export %q target %s was not materialized: %w", name, export.Path, err)
		}
		if !info.Mode().IsRegular() {
			return nil, "", fmt.Errorf("source-builder export %q target %s is not a regular file", name, export.Path)
		}
		result = append(result, export)
	}
	archivePath := filepath.Join(contextDir, sourceBuilderExportsArchiveV1)
	archive, err := os.OpenFile(archivePath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, "", fmt.Errorf("create source-builder exports archive: %w", err)
	}
	writer := tar.NewWriter(archive)
	writeErr := writer.WriteHeader(&tar.Header{
		Name: strings.TrimPrefix(SourceBuilderExportsDirectoryV1, "/") + "/", Mode: 0o755,
		Typeflag: tar.TypeDir, ModTime: time.Unix(0, 0).UTC(), Format: tar.FormatUSTAR,
	})
	for index, export := range result {
		if writeErr != nil {
			break
		}
		writeErr = writer.WriteHeader(&tar.Header{
			Name:     path.Join(strings.TrimPrefix(SourceBuilderExportsDirectoryV1, "/"), names[index]),
			Linkname: export.Path, Mode: 0o777, Typeflag: tar.TypeSymlink,
			ModTime: time.Unix(0, 0).UTC(), Format: tar.FormatUSTAR,
		})
	}
	closeErr := errors.Join(writer.Close(), archive.Sync(), archive.Close())
	if err := errors.Join(writeErr, closeErr); err != nil {
		_ = os.Remove(archivePath)
		return nil, "", fmt.Errorf("write source-builder exports archive: %w", err)
	}
	return result, sourceBuilderExportsArchiveV1, nil
}

// sourceBuilderDockerfileV1 renders the command-free layer that copies the
// offline-materialized host tree and adds the generated Linux export-link tar
// into a disposable builder image. It changes no image configuration.
func sourceBuilderDockerfileV1(copies []sourceBuilderCopyV1, exportsArchive string) ([]byte, error) {
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
	if exportsArchive != "" {
		if exportsArchive != sourceBuilderExportsArchiveV1 {
			return nil, fmt.Errorf("source-builder exports archive %q is unsupported", exportsArchive)
		}
		operands, err := json.Marshal([]string{exportsArchive, "/"})
		if err != nil {
			return nil, err
		}
		fmt.Fprintf(&output, "ADD --chown=0:0 %s\n", operands)
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
	dockerfile, err := sourceBuilderDockerfileV1(tools.copies, tools.exportsArchive)
	if err != nil {
		return nil, err
	}
	options.Context = ctx
	// Design "Safe Materialization": selected destinations are collision-checked
	// before extraction. The COPY layer must not merge over bytes the prefix
	// image already holds at any destination.
	destinations := make([]string, 0, len(tools.copies)+1)
	for _, copy := range tools.copies {
		destinations = append(destinations, copy.Destination)
	}
	if tools.exportsArchive != "" {
		destinations = append(destinations, SourceBuilderExportsDirectoryV1)
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
		Recipes:    tools.Plan.Recipes,
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
	// Derive the image's selected validation cases from its validated lock at
	// the point of use. There is no independently mutable schedule retained by
	// either the materialized tools or the prepared environment.
	validationInput, err := PortableToolMaterializationValidationInputFromLockV1(inspected, tools.Lock)
	if err != nil {
		return fail(fmt.Errorf("schedule source-builder portable tools: %w", err))
	}
	validateCtx, endValidate := buildprofile.Start(ctx, "Validate source-builder portable tools")
	evidence, err := validateSourceBuilderMaterializationV1(validateCtx, store, validationInput)
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

// buildSourceBuilderLayer runs one Docker build of the command-free COPY plus
// generated export-link ADD layer with the same temporary base and output
// reference mechanics as provider materialization layers.
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
