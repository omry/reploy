package dockerdeploy

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/probe"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providerstore"
)

// PortableRuntimePayloadImageV1 is a disposable, inspected payload layer.
// It is not browser-support evidence: native dependencies and non-root launch
// remain mandatory downstream checks.
type PortableRuntimePayloadImageV1 struct {
	Image     InspectedImageCandidate
	candidate BuiltImageCandidate
	removed   bool
}

var buildPortableRuntimePayloadLayerV1 = buildSourceBuilderLayerV1
var inspectPortableRuntimePayloadLayerV1 = InspectBuiltImageCandidate
var removePortableRuntimePayloadLayerV1 = RemoveBuiltImageCandidate
var requirePortableRuntimeDestinationsAbsentV1 = requirePortableRuntimeDestinationsAbsent
var preparePortableRuntimeProbeWorkspaceV1 = PrepareProbeWorkspace
var openPortableRuntimeProbeSessionV1 = OpenImageValidationSession

const portableRuntimeDestinationAncestorSymlinkCheckV1 = `for candidate in "$@"; do while [ "$candidate" != "/" ]; do test ! -L "$candidate" || exit 1; candidate="${candidate%/*}"; [ -n "$candidate" ] || candidate=/; done; done`

func (image *PortableRuntimePayloadImageV1) Cleanup(ctx context.Context) error {
	if image == nil || image.removed {
		return nil
	}
	if err := removePortableRuntimePayloadLayerV1(ctx, image.candidate); err != nil {
		return err
	}
	image.removed = true
	return nil
}

// PreparePortableRuntimePayloadLayerV1 checks upstream destinations before
// COPY, builds only from verified offline staging, and compares the exact
// selected destination inventory in the resulting image with staged entries.
// It returns no capability or support evidence.
func PreparePortableRuntimePayloadLayerV1(
	ctx context.Context, store providerstore.Store, payloads *PortableRuntimePayloadsV1,
	upstream deploy.ImageDescriptor, options RunOptions,
) (*PortableRuntimePayloadImageV1, error) {
	if ctx == nil {
		return nil, fmt.Errorf("prepare runtime payload layer requires a context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if payloads == nil || payloads.workspace == "" || len(payloads.copies) == 0 {
		return nil, fmt.Errorf("prepare runtime payload layer requires staged payloads")
	}
	if err := payloads.validateAuthorityV1(); err != nil {
		return nil, err
	}
	if len(payloads.sealedInventory) == 0 {
		return nil, fmt.Errorf("runtime payloads have no sealed staging inventory")
	}
	if err := verifyPortableRuntimeStagingV1(payloads, payloads.sealedInventory); err != nil {
		return nil, err
	}
	if err := upstream.Validate(); err != nil {
		return nil, err
	}
	for _, entry := range payloads.authorityLock.Plan.PortableToolPlan.Tools {
		for _, selected := range entry.Responsibilities.Payloads {
			platform, ok := selected.Record.Value["platform"].(string)
			if !ok || platform != upstream.Platform.Canonical {
				return nil, fmt.Errorf("runtime payload %s platform differs from upstream %s", selected.Reference.ID, upstream.Platform.Canonical)
			}
		}
	}
	dockerfile, err := payloads.Dockerfile()
	if err != nil {
		return nil, err
	}
	destinations := make([]string, len(payloads.copies))
	for index, copy := range payloads.copies {
		destinations[index] = copy.Destination
	}
	if err := requirePortableRuntimeDestinationsAbsentV1(ctx, store, upstream, destinations); err != nil {
		return nil, fmt.Errorf("runtime payload destination collision: %w", err)
	}
	expected := append([]portableRuntimeInventoryEntryV1(nil), payloads.sealedInventory...)
	options.Context = ctx
	candidate, err := buildPortableRuntimePayloadLayerV1(ctx, store, upstream, payloads.contextDir, dockerfile, options)
	if err != nil {
		return nil, err
	}
	result := &PortableRuntimePayloadImageV1{candidate: candidate}
	fail := func(err error) (*PortableRuntimePayloadImageV1, error) {
		return nil, errors.Join(err, result.Cleanup(context.WithoutCancel(ctx)))
	}
	inspected, err := inspectPortableRuntimePayloadLayerV1(ctx, candidate, upstream.Platform)
	if err != nil {
		return fail(err)
	}
	if err := ValidateInspectedImageCandidateIdentity(inspected); err != nil {
		return fail(err)
	}
	if inspected.Descriptor.Platform != upstream.Platform {
		return fail(fmt.Errorf("runtime payload image platform differs from upstream"))
	}
	if err := requirePortableRuntimeImageEnvironmentV1(inspected, payloads.authorityEnvironment); err != nil {
		return fail(err)
	}
	if err := verifyPortableRuntimeInventoryV1(ctx, store, inspected, expected); err != nil {
		return fail(err)
	}
	result.Image = inspected
	return result, nil
}

// requirePortableRuntimeDestinationsAbsent performs the candidate-specific
// preflight needed by ordinary COPY destinations. In addition to preserving
// the exact-destination absence check, it rejects a symlink anywhere in the
// upstream destination ancestor chain: Docker COPY can follow such a link in
// the previous image state even when the selected child destination itself is
// absent.
func requirePortableRuntimeDestinationsAbsent(
	ctx context.Context,
	store providerstore.Store,
	upstream deploy.ImageDescriptor,
	destinations []string,
) (resultErr error) {
	if len(destinations) == 0 {
		return fmt.Errorf("runtime payload collision check requires at least one destination")
	}
	workspace, cleanupWorkspace, err := preparePortableRuntimeProbeWorkspaceV1(ctx, store, upstream.Platform)
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
	session, err := openPortableRuntimeProbeSessionV1(ctx, upstream, workspace)
	if err != nil {
		return err
	}
	defer func() {
		if err := session.Close(context.WithoutCancel(ctx)); err != nil {
			resultErr = errors.Join(resultErr, err)
		}
	}()
	if err := session.ValidatePathsAbsent(ctx, destinations); err != nil {
		return err
	}
	if err := validatePortableRuntimeDestinationAncestorsV1(ctx, session, destinations); err != nil {
		return err
	}
	return nil
}

func validatePortableRuntimeDestinationAncestorsV1(
	ctx context.Context, session *ImageValidationSession, destinations []string,
) error {
	if session == nil || session.closed {
		return fmt.Errorf("image validation session is not open")
	}
	if ctx == nil {
		return fmt.Errorf("runtime payload destination ancestor context is required")
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("validate runtime payload destination ancestors: %w", err)
	}
	for _, destination := range destinations {
		if err := validateMountOptionPath("path", destination, true); err != nil {
			return fmt.Errorf("runtime payload destination ancestor check: %w", err)
		}
	}
	args := []string{
		"exec", "--user", "0:0", "--workdir", "/", session.containerName,
		"/bin/sh", "-c", portableRuntimeDestinationAncestorSymlinkCheckV1, "reploy-validation",
	}
	args = append(args, destinations...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if err := session.runDockerCommand(CommandSpec{Name: "docker", Args: args}, RunOptions{
		Context: ctx, Stdout: &stdout, Stderr: &stderr,
	}); err != nil {
		return fmt.Errorf("runtime payload destination ancestor is a symlink: %w", imageValidationCommandError("destination ancestor check", session.descriptor.Platform.Canonical, stderr.String(), err))
	}
	return nil
}

// verifyPortableRuntimeStagingV1 binds the live build context to the
// post-extraction authority captured by MaterializePortableRuntimePayloadsLockedV1.
// In particular, do not derive expected from the live tree here: a caller can
// retain the disposable context after materialization and mutate it before
// image preparation.
func verifyPortableRuntimeStagingV1(
	payloads *PortableRuntimePayloadsV1, expected []portableRuntimeInventoryEntryV1,
) error {
	observed, err := collectPortableRuntimeInventoryV1(payloads)
	if err != nil {
		return fmt.Errorf("verify live runtime staging: %w", err)
	}
	observedByPath := make(map[string]portableRuntimeInventoryEntryV1, len(observed))
	for _, item := range observed {
		if _, found := observedByPath[item.path]; found {
			return fmt.Errorf("live runtime staging inventory repeats %s", item.path)
		}
		observedByPath[item.path] = item
	}
	// Compare the raw host representation with the immediately post-extraction
	// baseline first. This detects staging mutation even on Windows, where an
	// executable may be reported as 0444 although the sealed Linux contract is
	// 0555. The final-image contract is checked separately below.
	baseline := payloads.stagingInventory
	if len(baseline) == 0 {
		if err := comparePortableRuntimeInventoryContentV1(observedByPath, expected); err != nil {
			return fmt.Errorf("live runtime staging differs from sealed inventory: %w", err)
		}
		if err := comparePortableRuntimeHostModesV1(observedByPath, expected); err != nil {
			return fmt.Errorf("live runtime staging modes differ from sealed inventory: %w", err)
		}
	} else {
		if err := comparePortableRuntimeInventoryV1(observedByPath, baseline); err != nil {
			return fmt.Errorf("live runtime staging differs from sealed inventory: %w", err)
		}
	}
	if err := comparePortableRuntimeInventoryContentV1(observedByPath, expected); err != nil {
		return fmt.Errorf("live runtime staging differs from normalized image contract: %w", err)
	}
	for _, expectedItem := range expected {
		observedItem := observedByPath[expectedItem.path]
		if observedItem.uid != expectedItem.uid || observedItem.gid != expectedItem.gid {
			return fmt.Errorf("live runtime staging ownership for %s differs from sealed inventory", expectedItem.path)
		}
	}
	return nil
}

func comparePortableRuntimeHostModesV1(
	observed map[string]portableRuntimeInventoryEntryV1, expected []portableRuntimeInventoryEntryV1,
) error {
	for _, expectedItem := range expected {
		observedItem, found := observed[expectedItem.path]
		if !found {
			return fmt.Errorf("runtime payload inventory is missing %s", expectedItem.path)
		}
		if observedItem.mode == expectedItem.mode {
			continue
		}
		// Windows represents both archive regular-file classes as read-only
		// 0444 on the staging host. The sealed Linux contract still requires
		// selected executables to be 0555, and the image Dockerfile realizes
		// that mode explicitly.
		if expectedItem.kind == providerstore.ArchiveEntryKindRegular && expectedItem.mode == 0o555 && observedItem.mode == 0o444 {
			continue
		}
		return fmt.Errorf("runtime inventory mode for %s differs from sealed contract", expectedItem.path)
	}
	return nil
}

func comparePortableRuntimeInventoryContentV1(
	observed map[string]portableRuntimeInventoryEntryV1, expected []portableRuntimeInventoryEntryV1,
) error {
	want := make(map[string]portableRuntimeInventoryEntryV1, len(expected))
	for _, item := range expected {
		if item.path == "" || !path.IsAbs(item.path) || path.Clean(item.path) != item.path {
			return fmt.Errorf("runtime inventory path %q is not an absolute clean path", item.path)
		}
		if _, found := want[item.path]; found {
			return fmt.Errorf("runtime inventory path %s repeats", item.path)
		}
		want[item.path] = item
	}
	if len(observed) != len(want) {
		for imagePath := range want {
			if _, found := observed[imagePath]; !found {
				return fmt.Errorf("runtime payload inventory is missing %s", imagePath)
			}
		}
		return fmt.Errorf("runtime payload inventory has unexpected entries")
	}
	for imagePath, expectedItem := range want {
		observedItem, found := observed[imagePath]
		if !found {
			return fmt.Errorf("runtime payload inventory is missing %s", imagePath)
		}
		if observedItem.kind != expectedItem.kind || observedItem.size != expectedItem.size || observedItem.digest != expectedItem.digest {
			return fmt.Errorf("runtime payload inventory entry %s differs from normalized image contract", imagePath)
		}
	}
	return nil
}

func requirePortableRuntimeImageEnvironmentV1(image InspectedImageCandidate, selected []providers.PortableToolEnvironmentVariableV1) error {
	observed := make(map[string]string, len(image.Config.Environment))
	for _, variable := range image.Config.Environment {
		observed[variable.Name] = variable.Value
	}
	for _, variable := range selected {
		if value, found := observed[variable.Name]; !found || value != variable.Value {
			return fmt.Errorf("runtime payload image environment %s differs from selected contract", variable.Name)
		}
	}
	return nil
}

type portableRuntimeFileEvidenceV1 struct {
	path   string
	digest canonical.Digest
	size   int64
	mode   os.FileMode
}

type portableRuntimeInventoryEntryV1 struct {
	path   string
	kind   string
	uid    int64
	gid    int64
	mode   os.FileMode
	size   int64
	digest canonical.Digest
}

const portableRuntimeSpecialModeBitsV1 = os.ModeSetuid | os.ModeSetgid | os.ModeSticky | os.FileMode(0o7000)

func normalizePortableRuntimeModeV1(mode os.FileMode) (os.FileMode, error) {
	if mode&portableRuntimeSpecialModeBitsV1 != 0 {
		return 0, fmt.Errorf("runtime payload mode contains unsupported special bits")
	}
	return mode.Perm(), nil
}

func normalizePortableRuntimeExpectedInventoryV1(
	payloads *PortableRuntimePayloadsV1, observed []portableRuntimeInventoryEntryV1,
) ([]portableRuntimeInventoryEntryV1, error) {
	if payloads == nil {
		return nil, fmt.Errorf("runtime payload inventory authority requires staged payloads")
	}
	executables := make(map[string]struct{}, len(payloads.executablePaths))
	for _, executable := range payloads.executablePaths {
		if executable == "" || !path.IsAbs(executable) || path.Clean(executable) != executable {
			return nil, fmt.Errorf("runtime executable authority path %q is not an absolute clean path", executable)
		}
		if _, found := executables[executable]; found {
			return nil, fmt.Errorf("runtime executable authority path %s repeats", executable)
		}
		executables[executable] = struct{}{}
	}
	result := make([]portableRuntimeInventoryEntryV1, len(observed))
	foundExecutables := make(map[string]struct{}, len(executables))
	for index, item := range observed {
		result[index] = item
		switch item.kind {
		case providerstore.ArchiveEntryKindDirectory:
			result[index].mode = 0o555
		case providerstore.ArchiveEntryKindRegular:
			result[index].mode = 0o444
			if _, executable := executables[item.path]; executable {
				result[index].mode = 0o555
				foundExecutables[item.path] = struct{}{}
			}
		default:
			return nil, fmt.Errorf("runtime payload inventory has unsupported kind %q at %s", item.kind, item.path)
		}
	}
	for executable := range executables {
		if _, found := foundExecutables[executable]; !found {
			return nil, fmt.Errorf("runtime executable authority path %s is missing from staging", executable)
		}
	}
	return result, nil
}

// A docker cp tar member can add headers, padding, and extended path metadata
// beyond the accepted file bytes. The selected staging inventory bounds both
// content and member count; this allowance bounds transport framing only.
const portableRuntimeImageTarFramingBytesPerEntryV1 = int64(16 << 10)

type portableRuntimeImageInventoryLimitV1 struct {
	entries int
	bytes   int64
}

func portableRuntimeImageInventoryLimitForRootV1(
	want map[string]portableRuntimeInventoryEntryV1, root string,
) (portableRuntimeImageInventoryLimitV1, error) {
	limit := portableRuntimeImageInventoryLimitV1{bytes: 1024} // End-of-archive blocks.
	for imagePath, item := range want {
		if imagePath != root && !strings.HasPrefix(imagePath, root+"/") {
			continue
		}
		limit.entries++
		if limit.bytes > math.MaxInt64-portableRuntimeImageTarFramingBytesPerEntryV1 {
			return portableRuntimeImageInventoryLimitV1{}, fmt.Errorf("runtime payload inventory %s framing limit overflows", root)
		}
		limit.bytes += portableRuntimeImageTarFramingBytesPerEntryV1
		if item.kind == providerstore.ArchiveEntryKindRegular {
			if item.size < 0 || item.size > math.MaxInt64-limit.bytes {
				return portableRuntimeImageInventoryLimitV1{}, fmt.Errorf("runtime payload inventory %s content limit is invalid", root)
			}
			limit.bytes += item.size
		}
	}
	if limit.entries == 0 {
		return portableRuntimeImageInventoryLimitV1{}, fmt.Errorf("runtime payload inventory %s has no selected entries", root)
	}
	return limit, nil
}

func collectPortableRuntimeInventoryV1(payloads *PortableRuntimePayloadsV1) ([]portableRuntimeInventoryEntryV1, error) {
	if payloads == nil || payloads.contextDir == "" {
		return nil, fmt.Errorf("runtime payload inventory requires staged payloads")
	}
	result := []portableRuntimeInventoryEntryV1{}
	seen := map[string]struct{}{}
	for _, copy := range payloads.copies {
		root := filepath.Join(payloads.contextDir, filepath.FromSlash(copy.Source))
		if copy.Destination == "" || !path.IsAbs(copy.Destination) || path.Clean(copy.Destination) != copy.Destination {
			return nil, fmt.Errorf("staged runtime destination %q is not an absolute clean path", copy.Destination)
		}
		err := filepath.WalkDir(root, func(hostPath string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			relative, err := filepath.Rel(root, hostPath)
			if err != nil {
				return err
			}
			imagePath := path.Join(copy.Destination, filepath.ToSlash(relative))
			if _, exists := seen[imagePath]; exists {
				return fmt.Errorf("staged runtime file %s repeats", imagePath)
			}
			seen[imagePath] = struct{}{}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			mode, err := normalizePortableRuntimeModeV1(info.Mode())
			if err != nil {
				return fmt.Errorf("staged runtime payload %s: %w", hostPath, err)
			}
			item := portableRuntimeInventoryEntryV1{path: imagePath, uid: 0, gid: 0, mode: mode}
			switch {
			case info.IsDir():
				item.kind = providerstore.ArchiveEntryKindDirectory
			case info.Mode().IsRegular():
				item.kind = providerstore.ArchiveEntryKindRegular
				item.size = info.Size()
				file, openErr := os.Open(hostPath)
				if openErr != nil {
					return openErr
				}
				hash := sha256.New()
				_, hashErr := io.Copy(hash, file)
				closeErr := file.Close()
				if err := errors.Join(hashErr, closeErr); err != nil {
					return err
				}
				item.digest = canonical.Digest(fmt.Sprintf("sha256:%x", hash.Sum(nil)))
			default:
				return fmt.Errorf("staged runtime payload contains unsupported entry %s", hostPath)
			}
			result = append(result, item)
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("runtime payloads expose no inventory")
	}
	sort.Slice(result, func(i, j int) bool { return result[i].path < result[j].path })
	return result, nil
}

func collectPortableRuntimeFileEvidenceV1(payloads *PortableRuntimePayloadsV1) ([]portableRuntimeFileEvidenceV1, error) {
	inventory, err := collectPortableRuntimeInventoryV1(payloads)
	if err != nil {
		return nil, err
	}
	result := make([]portableRuntimeFileEvidenceV1, 0, len(inventory))
	for _, item := range inventory {
		if item.kind != providerstore.ArchiveEntryKindRegular {
			continue
		}
		result = append(result, portableRuntimeFileEvidenceV1{path: item.path, digest: item.digest, size: item.size, mode: item.mode})
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("runtime payloads expose no file evidence")
	}
	return result, nil
}

func verifyPortableRuntimeInventoryV1(
	ctx context.Context, store providerstore.Store, image InspectedImageCandidate,
	expected []portableRuntimeInventoryEntryV1,
) (resultErr error) {
	if len(expected) == 0 {
		return fmt.Errorf("runtime payload inventory is empty")
	}
	workspace, cleanup, err := preparePortableRuntimeProbeWorkspaceV1(ctx, store, image.Descriptor.Platform)
	if err != nil {
		return err
	}
	preserveWorkspace := false
	defer func() {
		if !preserveWorkspace && !providerHelperCleanupFailed(resultErr) {
			resultErr = errors.Join(resultErr, cleanup())
		}
	}()
	session, err := openPortableRuntimeProbeSessionV1(ctx, image.Descriptor, workspace)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := session.Close(context.WithoutCancel(ctx)); closeErr != nil {
			preserveWorkspace = true
			resultErr = errors.Join(resultErr, closeErr)
		}
	}()
	observed, err := collectPortableRuntimeImageInventoryV1(ctx, session, expected)
	if err != nil {
		return err
	}
	if err := comparePortableRuntimeInventoryV1(observed, expected); err != nil {
		return err
	}
	return verifyPortableRuntimeInventoryOwnershipWithObservedV1(ctx, session, expected, observed)
}

// exportAndVerifyPortableRuntimeInventoryV1 uses Docker's fixed container
// copy operation for each selected destination rather than exporting the
// entire image. Each scoped tar stream is parsed incrementally, so final-image
// inventory is bounded by the selected archive limits and never loaded into
// memory as a whole.
func exportAndVerifyPortableRuntimeInventoryV1(
	ctx context.Context, session *ImageValidationSession, expected []portableRuntimeInventoryEntryV1,
) error {
	observed, err := collectPortableRuntimeImageInventoryV1(ctx, session, expected)
	if err != nil {
		return err
	}
	return comparePortableRuntimeInventoryV1(observed, expected)
}

func comparePortableRuntimeInventoryV1(
	observed map[string]portableRuntimeInventoryEntryV1, expected []portableRuntimeInventoryEntryV1,
) error {
	want := make(map[string]portableRuntimeInventoryEntryV1, len(expected))
	for _, item := range expected {
		if item.path == "" || !path.IsAbs(item.path) || path.Clean(item.path) != item.path {
			return fmt.Errorf("runtime inventory path %q is not an absolute clean path", item.path)
		}
		if _, found := want[item.path]; found {
			return fmt.Errorf("runtime inventory path %s repeats", item.path)
		}
		want[item.path] = item
	}
	if len(observed) != len(want) {
		for imagePath := range want {
			if _, found := observed[imagePath]; !found {
				return fmt.Errorf("final runtime payload inventory is missing %s", imagePath)
			}
		}
		return fmt.Errorf("final runtime payload inventory has unexpected entries")
	}
	for imagePath, expectedItem := range want {
		observedItem, found := observed[imagePath]
		if !found {
			return fmt.Errorf("final runtime payload inventory is missing %s", imagePath)
		}
		// Docker's cp tar stream may contain daemon-host IDs when userns
		// remapping is enabled. Ownership is authoritative only when observed
		// inside the held container below; tar remains authoritative for kind,
		// mode, size, and content digest.
		if observedItem.kind != expectedItem.kind || observedItem.mode != expectedItem.mode ||
			observedItem.size != expectedItem.size || observedItem.digest != expectedItem.digest {
			return fmt.Errorf("runtime inventory entry %s differs from verified offline staging", imagePath)
		}
	}
	return nil
}

func collectPortableRuntimeImageInventoryV1(
	ctx context.Context, session *ImageValidationSession, expected []portableRuntimeInventoryEntryV1,
) (result map[string]portableRuntimeInventoryEntryV1, resultErr error) {
	if session == nil || session.closed {
		return nil, fmt.Errorf("image validation session is not open")
	}
	want := make(map[string]portableRuntimeInventoryEntryV1, len(expected))
	for _, item := range expected {
		if item.path == "" || !path.IsAbs(item.path) || path.Clean(item.path) != item.path {
			return nil, fmt.Errorf("runtime inventory path %q is not an absolute clean path", item.path)
		}
		if _, found := want[item.path]; found {
			return nil, fmt.Errorf("runtime inventory path %s repeats", item.path)
		}
		want[item.path] = item
	}
	roots, err := portableRuntimeInventoryRoots(want)
	if err != nil {
		return nil, err
	}
	observed := map[string]portableRuntimeInventoryEntryV1{}
	for _, root := range roots {
		limit, err := portableRuntimeImageInventoryLimitForRootV1(want, root)
		if err != nil {
			return nil, err
		}
		entries, err := copyPortableRuntimeInventoryRootV1(ctx, session, root, limit)
		if err != nil {
			return nil, err
		}
		for imagePath, item := range entries {
			if _, duplicate := observed[imagePath]; duplicate {
				return nil, fmt.Errorf("final runtime payload inventory repeats %s", imagePath)
			}
			observed[imagePath] = item
		}
	}
	return observed, nil
}

func verifyPortableRuntimeInventoryOwnershipWithObservedV1(
	ctx context.Context, session *ImageValidationSession, expected []portableRuntimeInventoryEntryV1,
	observed map[string]portableRuntimeInventoryEntryV1,
) error {
	if session == nil || session.closed {
		return fmt.Errorf("image validation session is not open")
	}
	if len(observed) == 0 {
		return fmt.Errorf("runtime payload inventory is empty")
	}
	regulars := make([]portableRuntimeInventoryEntryV1, 0, len(expected))
	for _, item := range expected {
		switch item.kind {
		case providerstore.ArchiveEntryKindDirectory:
		case providerstore.ArchiveEntryKindRegular:
			regulars = append(regulars, item)
		default:
			return fmt.Errorf("runtime inventory entry %s has unsupported kind %q", item.path, item.kind)
		}
	}
	if len(regulars) == 0 {
		return fmt.Errorf("runtime payload inventory has no regular files for ownership observation")
	}
	sort.Slice(regulars, func(i, j int) bool { return regulars[i].path < regulars[j].path })
	request := probe.RequestV1{Schema: probe.RequestSchemaV1, Inspections: make([]probe.ExecutableInspectionV1, len(regulars))}
	for index, item := range regulars {
		request.Inspections[index] = probe.ExecutableInspectionV1{
			ID: fmt.Sprintf("runtime_%06d", index), InvocationPath: item.path,
		}
	}
	response, err := session.Probe(ctx, request)
	if err != nil {
		return err
	}
	var rawUID, rawGID int64
	haveRawOwner := false
	for index, item := range regulars {
		observation := response.Observations[index]
		terminal := observation.Terminal
		if terminal.Path != item.path || terminal.Kind != providerstore.ArchiveEntryKindRegular || terminal.UID != "0" || terminal.GID != "0" || terminal.Mode != fmt.Sprintf("%04o", item.mode) {
			return fmt.Errorf("runtime ownership for %s differs from container 0:0 contract", item.path)
		}
		tarItem, found := observed[item.path]
		if !found || tarItem.kind != providerstore.ArchiveEntryKindRegular {
			return fmt.Errorf("runtime ownership has no matching tar file for %s", item.path)
		}
		if !haveRawOwner {
			rawUID, rawGID, haveRawOwner = tarItem.uid, tarItem.gid, true
		} else if tarItem.uid != rawUID || tarItem.gid != rawGID {
			return fmt.Errorf("runtime tar ownership mapping is inconsistent for %s", item.path)
		}
	}
	if !haveRawOwner {
		return fmt.Errorf("runtime payload inventory has no ownership mapping")
	}
	for imagePath, item := range observed {
		if item.uid != rawUID || item.gid != rawGID {
			return fmt.Errorf("runtime tar ownership for %s differs from mapped container 0:0", imagePath)
		}
	}
	return nil
}

func portableRuntimeInventoryRoots(want map[string]portableRuntimeInventoryEntryV1) ([]string, error) {
	roots := map[string]struct{}{}
	for imagePath, item := range want {
		if item.kind != providerstore.ArchiveEntryKindDirectory {
			continue
		}
		ancestor := path.Dir(imagePath)
		for ancestor != "/" {
			if _, found := want[ancestor]; found {
				ancestor = ""
				break
			}
			parent := path.Dir(ancestor)
			if parent == ancestor {
				break
			}
			ancestor = parent
		}
		if ancestor != "" {
			roots[imagePath] = struct{}{}
		}
	}
	if len(roots) == 0 {
		return nil, fmt.Errorf("runtime payload inventory has no selected directory roots")
	}
	result := make([]string, 0, len(roots))
	for root := range roots {
		result = append(result, root)
	}
	sort.Strings(result)
	return result, nil
}

func copyPortableRuntimeInventoryRootV1(
	ctx context.Context, session *ImageValidationSession, root string, limit portableRuntimeImageInventoryLimitV1,
) (result map[string]portableRuntimeInventoryEntryV1, resultErr error) {
	reader, writer := io.Pipe()
	runErr := make(chan error, 1)
	go func() {
		var stderr bytes.Buffer
		err := session.runDockerCommand(CommandSpec{Name: "docker", Args: []string{"cp", session.containerName + ":" + root, "-"}}, RunOptions{
			Context: ctx, Stdout: writer, Stderr: &stderr,
		})
		if err != nil {
			if output := trimmedCommandOutput(stderr.String()); output != "" {
				err = fmt.Errorf("copy runtime payload inventory %s: %w\ncommand output:\n%s", root, err, output)
			} else {
				err = fmt.Errorf("copy runtime payload inventory %s: %w", root, err)
			}
			_ = writer.CloseWithError(err)
		} else {
			_ = writer.Close()
		}
		runErr <- err
	}()
	counting := &portableRuntimeInventoryCountingReader{reader: reader, limit: limit.bytes}
	tarReader := tar.NewReader(io.LimitReader(counting, limit.bytes))
	observed := map[string]portableRuntimeInventoryEntryV1{}
	entryCount := 0
	for {
		header, err := tarReader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			resultErr = fmt.Errorf("read final runtime payload inventory %s: %w", root, err)
			break
		}
		entryCount++
		if entryCount > limit.entries {
			resultErr = fmt.Errorf("final runtime payload inventory %s has unexpected entries beyond %d-entry limit", root, limit.entries)
			break
		}
		imagePath, err := portableRuntimeInventoryTarPathUnderRoot(header.Name, root)
		if err != nil {
			resultErr = err
			break
		}
		if _, duplicate := observed[imagePath]; duplicate {
			resultErr = fmt.Errorf("final runtime payload inventory repeats %s", imagePath)
			break
		}
		mode, err := normalizePortableRuntimeModeV1(os.FileMode(header.Mode))
		if err != nil {
			resultErr = fmt.Errorf("final runtime payload inventory %s: %w", imagePath, err)
			break
		}
		item := portableRuntimeInventoryEntryV1{path: imagePath, uid: int64(header.Uid), gid: int64(header.Gid), mode: mode}
		switch header.Typeflag {
		case tar.TypeDir:
			item.kind = providerstore.ArchiveEntryKindDirectory
		case tar.TypeReg, tar.TypeRegA:
			if header.Size < 0 || header.Size > providerstore.CoreMaxArchiveUnpackedBytes {
				resultErr = fmt.Errorf("final runtime payload file %s has invalid size", imagePath)
				break
			}
			item.kind = providerstore.ArchiveEntryKindRegular
			item.size = header.Size
			hash := sha256.New()
			if _, err := io.Copy(hash, tarReader); err != nil {
				resultErr = fmt.Errorf("hash final runtime payload file %s: %w", imagePath, err)
				break
			}
			item.digest = canonical.Digest(fmt.Sprintf("sha256:%x", hash.Sum(nil)))
		default:
			item.kind = fmt.Sprintf("tar-type-%d", header.Typeflag)
		}
		if resultErr != nil {
			break
		}
		observed[imagePath] = item
	}
	if resultErr != nil {
		_ = reader.CloseWithError(resultErr)
	} else {
		_ = reader.Close()
	}
	commandErr := <-runErr
	if resultErr == nil && commandErr != nil {
		resultErr = commandErr
	}
	if resultErr != nil {
		return nil, resultErr
	}
	if counting.bytes >= limit.bytes {
		return nil, fmt.Errorf("final runtime payload inventory %s exceeds %d-byte limit", root, limit.bytes)
	}
	return observed, nil
}

type portableRuntimeInventoryCountingReader struct {
	reader io.Reader
	bytes  int64
	limit  int64
}

func (reader *portableRuntimeInventoryCountingReader) Read(p []byte) (int, error) {
	if reader.bytes >= reader.limit {
		return 0, io.ErrUnexpectedEOF
	}
	if remaining := reader.limit - reader.bytes; int64(len(p)) > remaining {
		p = p[:remaining]
	}
	n, err := reader.reader.Read(p)
	reader.bytes += int64(n)
	return n, err
}

func portableRuntimeInventoryTarPath(name string) (string, error) {
	if name == "" || strings.ContainsRune(name, 0) || strings.Contains(name, `\`) {
		return "", fmt.Errorf("final runtime payload inventory contains invalid path %q", name)
	}
	trimmed := strings.TrimPrefix(name, "/")
	if trimmed == "" || trimmed == "." {
		return "/", nil
	}
	if trimmed == ".." || strings.HasPrefix(trimmed, "../") || strings.Contains(trimmed, "/../") {
		return "", fmt.Errorf("final runtime payload inventory contains escaping path %q", name)
	}
	return path.Join("/", trimmed), nil
}

func portableRuntimeInventoryTarPathUnderRoot(name string, root string) (string, error) {
	if root == "" || !path.IsAbs(root) || path.Clean(root) != root {
		return "", fmt.Errorf("runtime inventory root %q is not an absolute clean path", root)
	}
	normalized, err := portableRuntimeInventoryTarPath(name)
	if err != nil {
		return "", err
	}
	if normalized == "/" {
		return root, nil
	}
	if normalized == root || strings.HasPrefix(normalized, root+"/") {
		return normalized, nil
	}
	trimmed := strings.TrimPrefix(normalized, "/")
	base := path.Base(root)
	if trimmed == base {
		return root, nil
	}
	if strings.HasPrefix(trimmed, base+"/") {
		trimmed = strings.TrimPrefix(trimmed, base+"/")
	}
	imagePath := path.Join(root, trimmed)
	if imagePath != root && !strings.HasPrefix(imagePath, root+"/") {
		return "", fmt.Errorf("final runtime payload inventory path %q escapes %s", name, root)
	}
	return imagePath, nil
}
