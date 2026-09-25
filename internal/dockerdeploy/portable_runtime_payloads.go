package dockerdeploy

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"

	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/portabletool"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providerstore"
	"github.com/omry/reploy/internal/toolcatalog"
)

const portableRuntimePayloadArchiveV1 = "portable-runtime-payload-v1.tar"

// PortableRuntimePayloadsV1 is an offline, disposable build context. Nothing
// in it is accepted as browser support until the native-package and launch
// checks have run on the resulting image.
type PortableRuntimePayloadsV1 struct {
	Lock        providers.PortableToolLockV1
	Environment []providers.PortableToolEnvironmentVariableV1
	// authorityLock and authorityEnvironment are retained privately after
	// validation and staging. The exported views above are independent copies;
	// image preparation must never use caller-owned mutations as authority.
	authorityLock        providers.PortableToolLockV1
	authorityEnvironment []providers.PortableToolEnvironmentVariableV1
	workspace            string
	contextDir           string
	copies               []sourceBuilderCopyV1
	archives             []providerstore.ArchiveMaterializationResult
	// executablePaths is the selected Linux-mode authority for final image
	// realization. It is derived from the locked archive contracts rather than
	// from host filesystem permission bits, which are not portable to Windows.
	executablePaths []string
	// stagingInventory retains the host representation captured immediately
	// after extraction. It is used only to detect later staging mutation;
	// sealedInventory below carries the normalized Linux image contract.
	stagingInventory []portableRuntimeInventoryEntryV1
	// sealedInventory is captured after every selected archive has been
	// extracted and normalized. It is the private authority for the staging
	// tree and final-image checks; neither is allowed to derive authority from
	// a live, caller-mutable tree after this handoff.
	sealedInventory []portableRuntimeInventoryEntryV1
	archiveDigest   canonical.Digest
}

var acquirePortableRuntimePayloadV1 = providerstore.AcquireArtifact
var materializePortableRuntimeArchiveV1 = providerstore.MaterializeArchive
var embeddedPortableRuntimeLockRecordsV1 = toolcatalog.EmbeddedPortableToolLockRecordsV1
var openVerifiedPortableRuntimePayloadV1 = func(store providerstore.Store, descriptor providerstore.ArtifactDescriptor) (io.Closer, error) {
	return store.OpenVerifiedArtifact(descriptor)
}

func (payloads *PortableRuntimePayloadsV1) Cleanup() error {
	if payloads == nil || payloads.workspace == "" {
		return nil
	}
	if err := removeSourceBuilderPortableToolWorkspaceV1(payloads.workspace); err != nil {
		return err
	}
	payloads.workspace = ""
	return nil
}

func (payloads *PortableRuntimePayloadsV1) validateAuthorityV1() error {
	if !reflect.DeepEqual(payloads.Lock, payloads.authorityLock) {
		return fmt.Errorf("runtime payload public lock differs from validated authority")
	}
	if !slices.Equal(payloads.Environment, payloads.authorityEnvironment) {
		return fmt.Errorf("runtime payload public environment differs from validated authority")
	}
	return nil
}

// MaterializePortableRuntimePayloadsFreshV1 acquires only selected payloads.
// Other selected artifacts (for example eligible Python wheels) must arrive
// through their owning provider's verified acquisition handoff. The complete
// lock is built before any payload is extracted.
func MaterializePortableRuntimePayloadsFreshV1(
	ctx context.Context, store providerstore.Store, dag providers.PortableToolProviderDAGV1,
	closures []toolcatalog.SelectedClosureV1, other []providers.PortableToolArtifactAcquisitionInputV1,
) (*PortableRuntimePayloadsV1, error) {
	if ctx == nil {
		return nil, fmt.Errorf("runtime payload acquisition requires a context")
	}
	if err := providers.ValidatePortableToolProviderDAGV1(dag); err != nil {
		return nil, err
	}
	compiled, err := toolcatalog.CompilePortableToolPlanV1(closures)
	if err != nil {
		return nil, fmt.Errorf("compile selected runtime closures: %w", err)
	}
	want, err := providers.CanonicalPortableToolPlanBytesV1(dag.PortableToolPlan)
	if err != nil {
		return nil, err
	}
	got, err := providers.CanonicalPortableToolPlanBytesV1(compiled)
	if err != nil || !bytes.Equal(want, got) {
		return nil, fmt.Errorf("runtime payload closures do not match the selected provider plan")
	}
	records, err := embeddedPortableRuntimeLockRecordsV1(closures)
	if err != nil {
		return nil, fmt.Errorf("runtime payload lock records: %w", err)
	}
	if err := validatePortableRuntimeOtherAcquisitionsV1(store, dag, records, other); err != nil {
		return nil, err
	}
	inputs := append([]providers.PortableToolArtifactAcquisitionInputV1{}, other...)
	acquired := map[string]providerstore.AcquisitionResult{}
	for _, entry := range dag.PortableToolPlan.Tools {
		if entry.Runtime == nil || len(entry.Responsibilities.Payloads) == 0 {
			continue
		}
		manifest, err := portableToolPythonManifestForBindingV1(records.Releases, entry)
		if err != nil {
			return nil, err
		}
		for _, payload := range entry.Responsibilities.Payloads {
			artifact, err := portableToolPythonArtifactSourceForBindingV1(records.Artifacts, entry, payload.Reference)
			if err != nil {
				return nil, err
			}
			if err := providers.ValidatePortableToolArtifactSourceAuthorizationV1(entry, payload, artifact.Source, manifest, artifact.Descriptor); err != nil {
				return nil, fmt.Errorf("authorize payload %s: %w", payload.Reference.ID, err)
			}
			keyBytes, err := json.Marshal(struct {
				Descriptor providerstore.ArtifactDescriptor
				Source     providers.PortableToolSelectedRecordV1
				Mirrors    []string
			}{artifact.Descriptor, artifact.Source, artifact.Mirrors})
			if err != nil {
				return nil, err
			}
			key := string(keyBytes)
			outcome, found := acquired[key]
			if !found {
				outcome, err = acquirePortableRuntimePayloadV1(ctx, store, providerstore.AcquisitionRequest{
					Artifact: artifact.Descriptor,
					Source: providerstore.ArtifactSource{
						ID: artifact.Source.Reference.ID, SHA256: artifact.Descriptor.SHA256,
						Mirrors: artifact.Mirrors,
					},
					Policy: providerstore.DefaultAcquisitionPolicy(),
				})
				if err != nil {
					return nil, fmt.Errorf("acquire payload %s: %w", payload.Reference.ID, err)
				}
				if outcome.Artifact != artifact.Descriptor {
					return nil, fmt.Errorf("acquired payload %s differs from selected descriptor", payload.Reference.ID)
				}
				acquired[key] = outcome
			}
			inputs = append(inputs, providers.PortableToolArtifactAcquisitionInputV1{
				Scope: entry.Scope, Tool: entry.Provenance.Tool, Artifact: payload.Reference,
				Descriptor: outcome.Artifact, Source: artifact.Source, Provenance: outcome.Provenance,
			})
		}
	}
	lock, err := providers.BuildPortableToolLockV1(dag, records.Releases, inputs)
	if err != nil {
		return nil, fmt.Errorf("lock selected runtime payloads: %w", err)
	}
	return MaterializePortableRuntimePayloadsLockedV1(ctx, store, lock)
}

// The owning ecosystem provider must finish its eligibility and verified-byte
// handoff before browser acquisition starts. A caller cannot supply a payload
// acquisition as an "other" artifact to bypass the selected source mapping.
func validatePortableRuntimeOtherAcquisitionsV1(
	store providerstore.Store, dag providers.PortableToolProviderDAGV1,
	records toolcatalog.EmbeddedPortableToolLockRecordSetV1,
	other []providers.PortableToolArtifactAcquisitionInputV1,
) error {
	expected := map[string]struct{}{}
	for _, entry := range dag.PortableToolPlan.Tools {
		manifest, err := portableToolPythonManifestForBindingV1(records.Releases, entry)
		if err != nil {
			return err
		}
		for _, binding := range entry.Responsibilities.BindingArtifacts {
			artifact, err := portableToolPythonArtifactSourceForBindingV1(records.Artifacts, entry, binding.Reference)
			if err != nil {
				return err
			}
			key := entry.Scope + "\x00" + entry.Provenance.Tool + "\x00" + binding.Reference.ID
			expected[key] = struct{}{}
			matching := 0
			for _, input := range other {
				if input.Scope != entry.Scope || input.Tool != entry.Provenance.Tool || input.Artifact != binding.Reference {
					continue
				}
				matching++
				if input.Descriptor != artifact.Descriptor || !reflect.DeepEqual(input.Source, artifact.Source) {
					return fmt.Errorf("other acquisition %s differs from selected binding source", binding.Reference.ID)
				}
				if err := providers.ValidatePortableToolArtifactSourceAuthorizationV1(entry, binding, input.Source, manifest, input.Descriptor); err != nil {
					return fmt.Errorf("authorize binding %s: %w", binding.Reference.ID, err)
				}
				file, err := openVerifiedPortableRuntimePayloadV1(store, input.Descriptor)
				if err != nil {
					return fmt.Errorf("other acquisition %s is absent or changed: %w", binding.Reference.ID, err)
				}
				if err := file.Close(); err != nil {
					return err
				}
			}
			if matching != 1 {
				return fmt.Errorf("selected binding %s requires exactly one verified acquisition before payload acquisition", binding.Reference.ID)
			}
		}
	}
	if len(other) != len(expected) {
		return fmt.Errorf("other acquisitions include an unselected or duplicate artifact")
	}
	return nil
}

// MaterializePortableRuntimePayloadsLockedV1 never loads the embedded catalog
// or opens a network source. Every selected descriptor is reopened and hashed
// before extraction, and the archive primitive hashes it again at use.
func MaterializePortableRuntimePayloadsLockedV1(
	ctx context.Context, store providerstore.Store, lock providers.PortableToolLockV1,
) (result *PortableRuntimePayloadsV1, resultErr error) {
	if ctx == nil {
		return nil, fmt.Errorf("locked runtime payload materialization requires a context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := providers.ValidatePortableToolLockV1(lock); err != nil {
		return nil, fmt.Errorf("locked runtime payloads: %w", err)
	}
	type selectedPayload struct {
		payload    portabletool.PayloadRecordV1
		descriptor providerstore.ArtifactDescriptor
		root       string
	}
	authorityLock := providers.ClonePortableToolLockV1(lock)
	selected := []selectedPayload{}
	claims := map[string]selectedPayload{}
	environment := map[string]string{}
	for _, entry := range authorityLock.Plan.PortableToolPlan.Tools {
		if entry.Runtime == nil || len(entry.Responsibilities.Payloads) == 0 {
			continue
		}
		for _, variable := range entry.Runtime.Environment {
			if previous, found := environment[variable.Name]; found && previous != variable.Value {
				return nil, fmt.Errorf("conflicting runtime environment %s", variable.Name)
			}
			environment[variable.Name] = variable.Value
		}
		for _, record := range entry.Responsibilities.Payloads {
			encoded, err := json.Marshal(record.Record.Value)
			if err != nil {
				return nil, err
			}
			var payload portabletool.PayloadRecordV1
			if err := json.Unmarshal(encoded, &payload); err != nil {
				return nil, fmt.Errorf("decode selected payload %s: %w", record.Reference.ID, err)
			}
			var acquisition *providers.PortableToolArtifactAcquisitionLockV1
			for index := range authorityLock.Acquisitions {
				candidate := &authorityLock.Acquisitions[index]
				if candidate.Scope == entry.Scope && candidate.Tool == entry.Provenance.Tool && candidate.Artifact == record.Reference {
					acquisition = candidate
					break
				}
			}
			if acquisition == nil {
				return nil, fmt.Errorf("selected payload %s has no locked acquisition", record.Reference.ID)
			}
			destination := path.Join(entry.Runtime.InstallRoot, payload.InstallDirectory)
			for previous, claimed := range claims {
				if previous != destination && (strings.HasPrefix(destination, previous+"/") || strings.HasPrefix(previous, destination+"/")) {
					return nil, fmt.Errorf("runtime payload destinations %s and %s overlap", previous, destination)
				}
				if previous == destination && (claimed.descriptor != acquisition.Descriptor || !reflect.DeepEqual(claimed.payload, payload)) {
					return nil, fmt.Errorf("runtime payload destination %s has conflicting artifacts", destination)
				}
			}
			if _, duplicate := claims[destination]; duplicate {
				continue
			}
			claims[destination] = selectedPayload{payload: payload, descriptor: acquisition.Descriptor, root: entry.Runtime.InstallRoot}
			selected = append(selected, selectedPayload{payload: payload, descriptor: acquisition.Descriptor, root: entry.Runtime.InstallRoot})
		}
	}
	if len(selected) == 0 {
		return nil, fmt.Errorf("locked plan selects no runtime payloads")
	}
	for _, item := range selected {
		file, err := openVerifiedPortableRuntimePayloadV1(store, item.descriptor)
		if err != nil {
			return nil, fmt.Errorf("selected runtime payload %s is absent or changed: %w", item.payload.ID, err)
		}
		if err := file.Close(); err != nil {
			return nil, err
		}
	}
	workspace, err := store.NewWorkspace("runtime-payloads-*")
	if err != nil {
		return nil, err
	}
	authorityEnvironment := make([]providers.PortableToolEnvironmentVariableV1, 0, len(environment))
	for name, value := range environment {
		authorityEnvironment = append(authorityEnvironment, providers.PortableToolEnvironmentVariableV1{Name: name, Value: value})
	}
	sort.Slice(authorityEnvironment, func(i, j int) bool { return authorityEnvironment[i].Name < authorityEnvironment[j].Name })
	materialized := &PortableRuntimePayloadsV1{
		Lock:                 providers.ClonePortableToolLockV1(authorityLock),
		Environment:          append([]providers.PortableToolEnvironmentVariableV1(nil), authorityEnvironment...),
		authorityLock:        authorityLock,
		authorityEnvironment: append([]providers.PortableToolEnvironmentVariableV1(nil), authorityEnvironment...),
		workspace:            workspace,
		contextDir:           filepath.Join(workspace, "context"),
	}
	defer func() {
		if resultErr != nil {
			resultErr = errors.Join(resultErr, materialized.Cleanup())
		}
	}()
	for _, item := range selected {
		root := filepath.Join(materialized.contextDir, "rootfs", filepath.FromSlash(item.root))
		if err := os.MkdirAll(root, 0o700); err != nil {
			return nil, err
		}
		format, err := sourceBuilderArchiveFormatV1(item.payload.LogicalPath)
		if err != nil {
			return nil, err
		}
		policy, err := sourceBuilderSymbolicLinkPolicyV1(item.payload.SymbolicLinkPolicy)
		if err != nil {
			return nil, err
		}
		archiveResult, err := materializePortableRuntimeArchiveV1(ctx, store, providerstore.ArchiveMaterializationRequest{
			Artifact: item.descriptor, Format: format, DestinationRoot: root,
			InstallDirectory: item.payload.InstallDirectory, ArchiveRoot: item.payload.ArchiveRoot,
			ExpectedEntryCount: item.payload.Entries, ExpectedUnpackedSize: item.payload.UnpackedSize,
			ExecutablePaths: append([]string{}, item.payload.Executables...), SymbolicLinkPolicy: policy,
		})
		if err != nil {
			return nil, fmt.Errorf("materialize runtime payload %s: %w", item.payload.ID, err)
		}
		if archiveResult.FinalPath != filepath.Join(root, filepath.FromSlash(item.payload.InstallDirectory)) {
			return nil, fmt.Errorf("materialize runtime payload %s returned unexpected final path", item.payload.ID)
		}
		materialized.archives = append(materialized.archives, archiveResult)
		destination := path.Join(item.root, item.payload.InstallDirectory)
		materialized.copies = append(materialized.copies, sourceBuilderCopyV1{Source: path.Join("rootfs", destination), Destination: destination})
		for _, executable := range item.payload.Executables {
			imagePath, err := portableRuntimeExecutableDestinationV1(
				item.root, item.payload.InstallDirectory, item.payload.ArchiveRoot, executable,
			)
			if err != nil {
				return nil, fmt.Errorf("materialize runtime payload %s executable %s: %w", item.payload.ID, executable, err)
			}
			materialized.executablePaths = append(materialized.executablePaths, imagePath)
		}
	}
	sort.Slice(materialized.copies, func(i, j int) bool { return materialized.copies[i].Destination < materialized.copies[j].Destination })
	sort.Strings(materialized.executablePaths)
	for index := 1; index < len(materialized.executablePaths); index++ {
		if materialized.executablePaths[index-1] == materialized.executablePaths[index] {
			return nil, fmt.Errorf("runtime executable path %s repeats", materialized.executablePaths[index])
		}
	}
	stagingInventory, err := collectPortableRuntimeInventoryV1(materialized)
	if err != nil {
		return nil, fmt.Errorf("seal runtime payload inventory: %w", err)
	}
	materialized.stagingInventory = append([]portableRuntimeInventoryEntryV1(nil), stagingInventory...)
	sealedInventory, err := normalizePortableRuntimeExpectedInventoryV1(materialized, stagingInventory)
	if err != nil {
		return nil, fmt.Errorf("normalize runtime payload inventory: %w", err)
	}
	materialized.sealedInventory = sealedInventory
	archiveDigest, err := writePortableRuntimePayloadArchiveV1(ctx, materialized)
	if err != nil {
		return nil, fmt.Errorf("seal runtime payload archive: %w", err)
	}
	materialized.archiveDigest = archiveDigest
	return materialized, nil
}

// Build a deterministic Linux layer from the sealed inventory, not host mode
// bits. Docker ADD extracts this archive without running a command or relying
// on the configured user in the upstream image.
func writePortableRuntimePayloadArchiveV1(ctx context.Context, payloads *PortableRuntimePayloadsV1) (digest canonical.Digest, resultErr error) {
	archivePath := filepath.Join(payloads.contextDir, portableRuntimePayloadArchiveV1)
	file, err := os.OpenFile(archivePath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", err
	}
	defer func() { resultErr = errors.Join(resultErr, file.Close()) }()
	hash := sha256.New()
	writer := tar.NewWriter(io.MultiWriter(file, hash))
	defer func() {
		resultErr = errors.Join(resultErr, writer.Close())
		if resultErr == nil {
			digest = canonical.Digest(fmt.Sprintf("sha256:%x", hash.Sum(nil)))
		}
	}()
	for _, item := range payloads.sealedInventory {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		name := strings.TrimPrefix(item.path, "/")
		if name == "" || path.Clean(name) != name || strings.HasPrefix(name, "../") {
			return "", fmt.Errorf("runtime payload archive path %q is invalid", item.path)
		}
		header := &tar.Header{Name: name, Mode: int64(item.mode.Perm()), Uid: 0, Gid: 0, Format: tar.FormatPAX}
		switch item.kind {
		case providerstore.ArchiveEntryKindDirectory:
			header.Name += "/"
			header.Typeflag = tar.TypeDir
		case providerstore.ArchiveEntryKindRegular:
			header.Typeflag = tar.TypeReg
			header.Size = item.size
		default:
			return "", fmt.Errorf("runtime payload archive entry %s has unsupported kind %q", item.path, item.kind)
		}
		if err := writer.WriteHeader(header); err != nil {
			return "", err
		}
		if item.kind != providerstore.ArchiveEntryKindRegular {
			continue
		}
		stagedPath := filepath.Join(payloads.contextDir, "rootfs", filepath.FromSlash(name))
		staged, err := os.Open(stagedPath)
		if err != nil {
			return "", err
		}
		hash := sha256.New()
		written, copyErr := io.Copy(io.MultiWriter(writer, hash), staged)
		closeErr := staged.Close()
		if err := errors.Join(copyErr, closeErr); err != nil {
			return "", err
		}
		if written != item.size || canonical.Digest(fmt.Sprintf("sha256:%x", hash.Sum(nil))) != item.digest {
			return "", fmt.Errorf("runtime payload archive entry %s differs from sealed staging", item.path)
		}
	}
	return "", nil
}

func portableRuntimeExecutableDestinationV1(root, installDirectory, archiveRoot, executable string) (string, error) {
	if root == "" || !path.IsAbs(root) || path.Clean(root) != root {
		return "", fmt.Errorf("runtime install root %q is not an absolute clean path", root)
	}
	if installDirectory == "" || strings.ContainsAny(installDirectory, `/\\`) || installDirectory == "." || installDirectory == ".." {
		return "", fmt.Errorf("runtime install directory %q is invalid", installDirectory)
	}
	if executable == "" || path.IsAbs(executable) || path.Clean(executable) != executable || executable == "." {
		return "", fmt.Errorf("runtime executable path %q is not a clean relative path", executable)
	}
	relative := executable
	if archiveRoot != "." && archiveRoot == installDirectory {
		if executable == archiveRoot {
			return "", fmt.Errorf("runtime executable path %q names the archive root directory", executable)
		}
		prefix := archiveRoot + "/"
		if !strings.HasPrefix(executable, prefix) {
			return "", fmt.Errorf("runtime executable path %q is outside archive root %q", executable, archiveRoot)
		}
		relative = strings.TrimPrefix(executable, prefix)
	}
	if relative == "" || relative == "." || path.IsAbs(relative) || path.Clean(relative) != relative {
		return "", fmt.Errorf("runtime executable path %q has no file relative to install directory", executable)
	}
	return path.Join(root, installDirectory, relative), nil
}

// portableRuntimeDockerfileV1 extracts the sealed Linux layer without relying
// on binaries, shell, or the configured USER in the upstream image.
func portableRuntimeDockerfileV1(copies []sourceBuilderCopyV1, executablePaths []string) ([]byte, error) {
	if len(copies) == 0 {
		return nil, fmt.Errorf("runtime payload layer requires at least one materialized tree")
	}
	var output bytes.Buffer
	fmt.Fprintf(&output, "# syntax=%s\n", MaterializationDockerfileSyntax)
	output.WriteString("ARG REPLOY_BASE_IMAGE=scratch\n")
	output.WriteString("FROM ${REPLOY_BASE_IMAGE}\n")
	seenDestinations := map[string]struct{}{}
	for _, copy := range copies {
		if copy.Source == "" || path.IsAbs(copy.Source) || path.Clean(copy.Source) != copy.Source || strings.HasPrefix(copy.Source, "..") {
			return nil, fmt.Errorf("runtime payload copy source %q must be a clean context-relative path", copy.Source)
		}
		if !path.IsAbs(copy.Destination) || path.Clean(copy.Destination) != copy.Destination || copy.Destination == "/" {
			return nil, fmt.Errorf("runtime payload copy destination %q must be an absolute clean path", copy.Destination)
		}
		if _, exists := seenDestinations[copy.Destination]; exists {
			return nil, fmt.Errorf("runtime payload copy destination %q repeats", copy.Destination)
		}
		seenDestinations[copy.Destination] = struct{}{}
	}
	seenExecutables := map[string]struct{}{}
	for _, executable := range executablePaths {
		if executable == "" || !path.IsAbs(executable) || path.Clean(executable) != executable || executable == "/" {
			return nil, fmt.Errorf("runtime executable destination %q is not an absolute clean path", executable)
		}
		if _, exists := seenExecutables[executable]; exists {
			return nil, fmt.Errorf("runtime executable destination %s repeats", executable)
		}
		seenExecutables[executable] = struct{}{}
		found := false
		for _, copy := range copies {
			prefix := copy.Destination + "/"
			if !strings.HasPrefix(executable, prefix) {
				continue
			}
			relative := strings.TrimPrefix(executable, prefix)
			if relative == "" || relative == "." || path.IsAbs(relative) || path.Clean(relative) != relative {
				return nil, fmt.Errorf("runtime executable destination %s does not name a file below %s", executable, copy.Destination)
			}
			found = true
			break
		}
		if !found {
			return nil, fmt.Errorf("runtime executable destination %s is outside selected copies", executable)
		}
	}
	fmt.Fprintf(&output, "ADD --chown=0:0 [\"%s\",\"/\"]\n", portableRuntimePayloadArchiveV1)
	return output.Bytes(), nil
}

// Dockerfile produces only fixed ADD and ENV instructions from the validated
// lock. The caller must collision-check
// the upstream image and inspect and validate the result before accepting it.
func (payloads *PortableRuntimePayloadsV1) Dockerfile() ([]byte, error) {
	if payloads == nil || payloads.workspace == "" {
		return nil, fmt.Errorf("runtime payloads have been cleaned up")
	}
	if err := payloads.validateAuthorityV1(); err != nil {
		return nil, err
	}
	result, err := portableRuntimeDockerfileV1(payloads.copies, payloads.executablePaths)
	if err != nil {
		return nil, err
	}
	for _, variable := range payloads.authorityEnvironment {
		if !slices.ContainsFunc(payloads.authorityLock.Plan.PortableToolPlan.Tools, func(entry providers.PortableToolPlanEntryV1) bool {
			return entry.Runtime != nil && slices.Contains(entry.Runtime.Environment, variable)
		}) {
			return nil, fmt.Errorf("runtime environment %s is not selected", variable.Name)
		}
		value, err := quoteDockerfileWord(variable.Value)
		if err != nil {
			return nil, fmt.Errorf("quote runtime environment %s: %w", variable.Name, err)
		}
		result = append(result, []byte("ENV "+variable.Name+"="+value+"\n")...)
	}
	return result, nil
}
