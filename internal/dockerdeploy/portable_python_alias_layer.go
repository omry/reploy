package dockerdeploy

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
	pythonprovider "github.com/omry/reploy/internal/providers/python"
	"github.com/omry/reploy/internal/providerstore"
)

// portablePythonAliasSpecV1 identifies one selected Python command export.
// Target is a canonical path in the final image. Destination is the
// application-facing path that the overlay publishes. BindingIdentity binds
// the destination to the selected portable-tool record; callers derive it
// from the complete binding before entering materialization.
//
// Output and Facts are used only to bind the final-image probe observation to
// the selected output. They are intentionally carried by this internal seam so
// alias validation cannot accidentally create evidence for an unrelated
// component or command.
type portablePythonAliasSpecV1 struct {
	Name            string
	Component       string
	Destination     string
	Target          string
	BindingIdentity string
	Output          providers.QualifiedOutput
	Facts           providers.CanonicalProviderData
}

const portablePythonAliasArchiveV1 = "reploy-python-aliases.tar"

// These seams keep the alias layer's Docker build, inspection, and final-image
// probe independently testable. The production defaults all use the existing
// command and evidence paths used by the provider materializer.
var (
	buildPortablePythonAliasLayerV1          = buildSourceBuilderLayerV1
	inspectPortablePythonAliasLayerV1        = InspectBuiltImageCandidate
	collectPortablePythonAliasEvidenceV1     = CollectFullImageExecutableEvidence
	inspectPortablePythonAliasDestinationsV1 = inspectPortablePythonAliasDestinations
)

// buildAndValidatePortablePythonAliasLayerV1 builds an alias-only overlay on
// source and returns it only after the overlay image has been inspected and
// every published link has been resolved in that exact image. The source
// candidate must already have passed materialization acceptance. The returned
// candidate remains caller-owned and must be retained or removed by the
// materialization acceptance transaction.
func buildAndValidatePortablePythonAliasLayerV1(
	ctx context.Context,
	store providerstore.Store,
	source InspectedImageCandidate,
	aliases []portablePythonAliasSpecV1,
	platform blueprint.Platform,
	options RunOptions,
) (result BuiltImageCandidate, inspected InspectedImageCandidate, resultErr error) {
	if ctx == nil {
		return BuiltImageCandidate{}, InspectedImageCandidate{}, fmt.Errorf("build Python alias layer requires a context")
	}
	if err := ctx.Err(); err != nil {
		return BuiltImageCandidate{}, InspectedImageCandidate{}, err
	}
	if err := platform.Validate(); err != nil {
		return BuiltImageCandidate{}, InspectedImageCandidate{}, fmt.Errorf("build Python alias layer platform: %w", err)
	}
	if err := ValidateInspectedImageCandidateIdentity(source); err != nil {
		return BuiltImageCandidate{}, InspectedImageCandidate{}, fmt.Errorf("build Python alias layer source: %w", err)
	}
	if source.Descriptor.Platform != platform {
		return BuiltImageCandidate{}, InspectedImageCandidate{}, fmt.Errorf("build Python alias layer source platform %s does not match %s", source.Descriptor.Platform.Canonical, platform.Canonical)
	}
	if len(aliases) == 0 {
		return BuiltImageCandidate{}, source, nil
	}
	if buildPortablePythonAliasLayerV1 == nil || inspectPortablePythonAliasLayerV1 == nil || collectPortablePythonAliasEvidenceV1 == nil || inspectPortablePythonAliasDestinationsV1 == nil {
		return BuiltImageCandidate{}, InspectedImageCandidate{}, fmt.Errorf("build Python alias layer requires build, inspection, and final-image probe backends")
	}

	aliases, err := normalizePortablePythonAliasSpecsV1(aliases)
	if err != nil {
		return BuiltImageCandidate{}, InspectedImageCandidate{}, err
	}
	workspace, err := store.NewWorkspace("alias-*")
	if err != nil {
		return BuiltImageCandidate{}, InspectedImageCandidate{}, err
	}
	stagingRoot := filepath.Join(workspace, "staging")
	contextDir := filepath.Join(workspace, "context")
	if err := os.Mkdir(stagingRoot, 0o700); err != nil {
		_ = os.RemoveAll(workspace)
		return BuiltImageCandidate{}, InspectedImageCandidate{}, fmt.Errorf("create Python alias staging root: %w", err)
	}
	if err := os.Mkdir(contextDir, 0o700); err != nil {
		_ = os.RemoveAll(workspace)
		return BuiltImageCandidate{}, InspectedImageCandidate{}, fmt.Errorf("create Python alias build context: %w", err)
	}
	// The staging tree is used only for the root-anchored primitive. The Docker
	// context contains the generated archive alone, so no host staging path or
	// generated target can leak into the image.
	defer func() {
		if cleanupErr := os.RemoveAll(workspace); cleanupErr != nil {
			cleanupErr = fmt.Errorf("remove Python alias staging workspace: %w", cleanupErr)
			if resultErr == nil {
				resultErr = cleanupErr
			} else {
				resultErr = errors.Join(resultErr, cleanupErr)
			}
		}
	}()

	claims := make(portablePythonAliasClaimsV1, len(aliases))
	destinations := make([]string, 0, len(aliases))
	for _, alias := range aliases {
		destinations = append(destinations, alias.Destination)
	}
	missingParents, err := inspectPortablePythonAliasDestinationsV1(ctx, store, source.Descriptor, destinations)
	if err != nil {
		return BuiltImageCandidate{}, InspectedImageCandidate{}, fmt.Errorf("check Python alias source-image destinations: %w", err)
	}
	published := make([]portablePythonAliasSpecV1, 0, len(aliases))
	for _, alias := range aliases {
		destination := filepath.Join(stagingRoot, filepath.FromSlash(strings.TrimPrefix(alias.Destination, "/")))
		newAlias, err := publishPortablePythonAliasV1(
			stagingRoot, destination, alias.Target, alias.BindingIdentity, claims,
		)
		if err != nil {
			return BuiltImageCandidate{}, InspectedImageCandidate{}, fmt.Errorf("publish Python alias %q: %w", alias.Name, err)
		}
		if newAlias {
			published = append(published, alias)
		}
	}
	if len(published) == 0 {
		// A fresh staging root cannot contain an already-owned alias. This branch
		// is reachable only when the input contained exact duplicates; no layer
		// is required for an empty overlay.
		return BuiltImageCandidate{}, source, nil
	}
	if err := writePortablePythonAliasArchiveV1(contextDir, stagingRoot, published, missingParents); err != nil {
		return BuiltImageCandidate{}, InspectedImageCandidate{}, err
	}
	dockerfile, err := portablePythonAliasDockerfileV1()
	if err != nil {
		return BuiltImageCandidate{}, InspectedImageCandidate{}, err
	}
	options.Context = ctx
	candidate, err := buildPortablePythonAliasLayerV1(ctx, store, source.Descriptor, contextDir, dockerfile, options)
	if err != nil {
		return candidate, InspectedImageCandidate{}, fmt.Errorf("build Python alias layer: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return candidate, InspectedImageCandidate{}, err
	}
	inspected, err = inspectPortablePythonAliasLayerV1(ctx, candidate, platform)
	if err != nil {
		return candidate, InspectedImageCandidate{}, fmt.Errorf("inspect Python alias layer: %w", err)
	}
	if inspected.Image.ConfigDigest != candidate.ImageID {
		return candidate, InspectedImageCandidate{}, fmt.Errorf("Python alias layer inspection image %s does not match built candidate %s", inspected.Image.ConfigDigest, candidate.ImageID)
	}
	if err := validatePortablePythonAliasLayerImageV1(source, inspected, platform); err != nil {
		return candidate, InspectedImageCandidate{}, err
	}
	if err := validatePortablePythonAliasFinalImageEvidenceV1(ctx, store, inspected.Descriptor, published); err != nil {
		return candidate, InspectedImageCandidate{}, err
	}
	return candidate, inspected, nil
}

func normalizePortablePythonAliasSpecsV1(input []portablePythonAliasSpecV1) ([]portablePythonAliasSpecV1, error) {
	result := append([]portablePythonAliasSpecV1{}, input...)
	for index := range result {
		alias := &result[index]
		if alias.Name == "" || !utf8.ValidString(alias.Name) || strings.ContainsAny(alias.Name, "\x00\r\n") {
			return nil, fmt.Errorf("Python alias name must be nonempty valid text")
		}
		if err := validatePortablePythonAliasDestinationV1(alias.Destination); err != nil {
			return nil, err
		}
		if strings.ContainsAny(alias.Target, "\x00\r\n") {
			return nil, fmt.Errorf("Python alias %q target contains unsafe control characters", alias.Name)
		}
		if alias.Output.Name == "" {
			alias.Output.Name = alias.Name
		}
		if alias.Output.Component == "" {
			alias.Output.Component = alias.Component
		}
		if alias.Output.Name == "" || alias.Output.Component == "" {
			return nil, fmt.Errorf("Python alias %q requires a qualified output", alias.Name)
		}
		if alias.Output.Name != alias.Name {
			return nil, fmt.Errorf("Python alias %q output name %q does not match the selected CLI name", alias.Name, alias.Output.Name)
		}
		runtimeRoot, err := pythonprovider.RuntimeRootV1(alias.Output.Component)
		if err != nil {
			return nil, fmt.Errorf("Python alias %q output component: %w", alias.Name, err)
		}
		wantTarget := path.Join(runtimeRoot, "bin", alias.Name)
		if alias.Target != wantTarget {
			return nil, fmt.Errorf("Python alias %q target %q does not match owning runtime path %q", alias.Name, alias.Target, wantTarget)
		}
		if alias.Facts.Schema == "" || alias.Facts.Value == nil {
			alias.Facts = providers.CanonicalProviderData{
				Schema: "portable-python-alias-v1",
				Value: canonical.Object{
					"binding": alias.BindingIdentity, "destination": alias.Destination, "target": alias.Target,
				},
			}
		}
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].Destination != result[right].Destination {
			return result[left].Destination < result[right].Destination
		}
		if result[left].Target != result[right].Target {
			return result[left].Target < result[right].Target
		}
		return result[left].BindingIdentity < result[right].BindingIdentity
	})
	unique := result[:0]
	for _, alias := range result {
		if len(unique) == 0 || unique[len(unique)-1].Destination != alias.Destination {
			unique = append(unique, alias)
			continue
		}
		previous := unique[len(unique)-1]
		if previous.Target != alias.Target || previous.BindingIdentity != alias.BindingIdentity {
			return nil, fmt.Errorf("Python alias destination %q has conflicting target or binding identity", alias.Destination)
		}
		// Exact target and binding identity is the only supported deduplication.
	}
	return unique, nil
}

func validatePortablePythonAliasDestinationV1(destination string) error {
	if destination == "" || !utf8.ValidString(destination) || strings.ContainsAny(destination, "\x00\r\n") || strings.ContainsRune(destination, '\\') || !path.IsAbs(destination) || path.Clean(destination) != destination || destination == "/" {
		return fmt.Errorf("Python alias destination %q must be a non-root absolute clean Linux path", destination)
	}
	for _, component := range strings.Split(strings.TrimPrefix(destination, "/"), "/") {
		if component == "" || component == "." || component == ".." {
			return fmt.Errorf("Python alias destination %q has an unsafe path component", destination)
		}
	}
	return nil
}

func validatePortablePythonAliasLayerImageV1(source, candidate InspectedImageCandidate, platform blueprint.Platform) error {
	if err := ValidateInspectedImageCandidateIdentity(candidate); err != nil {
		return fmt.Errorf("validate Python alias layer image: %w", err)
	}
	if err := source.Config.Validate(); err != nil {
		return fmt.Errorf("validate Python alias source image config: %w", err)
	}
	if err := candidate.Config.Validate(); err != nil {
		return fmt.Errorf("validate Python alias layer image config: %w", err)
	}
	if candidate.Descriptor.Platform != platform {
		return fmt.Errorf("Python alias layer image platform %s does not match %s", candidate.Descriptor.Platform.Canonical, platform.Canonical)
	}
	if !reflect.DeepEqual(candidate.Config, source.Config) {
		return fmt.Errorf("Python alias layer changed the source image configuration")
	}
	return nil
}

func portablePythonAliasDockerfileV1() ([]byte, error) {
	archive, err := json.Marshal([]string{portablePythonAliasArchiveV1, "/"})
	if err != nil {
		return nil, fmt.Errorf("encode Python alias archive operand: %w", err)
	}
	if bytes.ContainsAny(archive, "\r\n") {
		return nil, fmt.Errorf("Python alias archive operand contains a line break")
	}
	var output bytes.Buffer
	fmt.Fprintf(&output, "# syntax=%s\n", MaterializationDockerfileSyntax)
	output.WriteString("ARG REPLOY_BASE_IMAGE=scratch\n")
	output.WriteString("FROM ${REPLOY_BASE_IMAGE}\n")
	fmt.Fprintf(&output, "ADD --chown=0:0 %s\n", archive)
	return output.Bytes(), nil
}

func writePortablePythonAliasArchiveV1(contextDir, stagingRoot string, aliases []portablePythonAliasSpecV1, missingParents []string) error {
	missing := make(map[string]struct{}, len(missingParents))
	for _, parent := range missingParents {
		if err := validatePortablePythonAliasDestinationV1(parent); err != nil {
			return fmt.Errorf("Python alias source-image missing parent %q: %w", parent, err)
		}
		missing[strings.TrimPrefix(parent, "/")] = struct{}{}
	}
	archivePath := filepath.Join(contextDir, portablePythonAliasArchiveV1)
	archive, err := os.OpenFile(archivePath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create Python alias archive: %w", err)
	}
	writer := tar.NewWriter(archive)
	closeArchive := func() error {
		return errors.Join(writer.Close(), archive.Sync(), archive.Close())
	}
	expected := make(map[string]portablePythonAliasSpecV1, len(aliases))
	for _, alias := range aliases {
		expected[strings.TrimPrefix(alias.Destination, "/")] = alias
	}
	parents := make([]string, 0, len(aliases))
	links := make([]struct {
		name   string
		target string
	}, 0, len(aliases))
	walkErr := filepath.WalkDir(stagingRoot, func(current string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if current == stagingRoot {
			return nil
		}
		relative, err := filepath.Rel(stagingRoot, current)
		if err != nil || relative == "." || filepath.IsAbs(relative) {
			return fmt.Errorf("derive Python alias archive path %q: %w", current, err)
		}
		name := filepath.ToSlash(relative)
		if entry.IsDir() {
			parents = append(parents, name)
			return nil
		}
		if entry.Type()&os.ModeSymlink == 0 {
			return fmt.Errorf("Python alias staging root contains an unexpected non-symlink %q", name)
		}
		alias, exists := expected[name]
		if !exists {
			return fmt.Errorf("Python alias staging root contains an unexpected link %q", name)
		}
		target, err := os.Readlink(current)
		if err != nil {
			return fmt.Errorf("read staged Python alias %q: %w", alias.Name, err)
		}
		target = normalizePortablePythonAliasTargetV1(target)
		if target != alias.Target {
			return fmt.Errorf("staged Python alias %q resolves to %q, want %q", alias.Name, target, alias.Target)
		}
		links = append(links, struct {
			name   string
			target string
		}{name: name, target: target})
		delete(expected, name)
		return nil
	})
	if walkErr != nil {
		_ = closeArchive()
		_ = os.Remove(archivePath)
		return fmt.Errorf("inspect Python alias staging tree: %w", walkErr)
	}
	if len(expected) != 0 {
		_ = closeArchive()
		_ = os.Remove(archivePath)
		return fmt.Errorf("Python alias staging tree is missing %d published link(s)", len(expected))
	}
	orderedParents := make([]string, 0, len(parents))
	for _, parent := range parents {
		if _, exists := missing[parent]; exists {
			orderedParents = append(orderedParents, parent)
		}
	}
	sort.Slice(orderedParents, func(left, right int) bool {
		leftDepth, rightDepth := strings.Count(orderedParents[left], "/"), strings.Count(orderedParents[right], "/")
		if leftDepth != rightDepth {
			return leftDepth < rightDepth
		}
		return orderedParents[left] < orderedParents[right]
	})
	var writeErr error
	for _, parent := range orderedParents {
		writeErr = writer.WriteHeader(&tar.Header{
			Name: parent, Mode: 0o755, Typeflag: tar.TypeDir,
			ModTime: time.Unix(0, 0).UTC(), Format: tar.FormatPAX,
		})
		if writeErr != nil {
			break
		}
	}
	if writeErr == nil {
		sort.Slice(links, func(left, right int) bool { return links[left].name < links[right].name })
		for _, link := range links {
			writeErr = writer.WriteHeader(&tar.Header{
				Name: link.name, Linkname: link.target,
				Mode: 0o777, Typeflag: tar.TypeSymlink,
				ModTime: time.Unix(0, 0).UTC(), Format: tar.FormatPAX,
			})
			if writeErr != nil {
				break
			}
		}
	}
	closeErr := closeArchive()
	if err := errors.Join(writeErr, closeErr); err != nil {
		_ = os.Remove(archivePath)
		return fmt.Errorf("write Python alias archive: %w", err)
	}
	return nil
}

func validatePortablePythonAliasFinalImageEvidenceV1(
	ctx context.Context,
	store providerstore.Store,
	descriptor deploy.ImageDescriptor,
	aliases []portablePythonAliasSpecV1,
) error {
	checks := make([]FullImageExecutableProbe, 0, len(aliases))
	for index, alias := range aliases {
		checks = append(checks, FullImageExecutableProbe{
			ID:             fmt.Sprintf("python_alias_%06d", index),
			InvocationPath: alias.Destination,
			Binding: ProbeExecutableBinding{
				Output: alias.Output,
				Facts:  alias.Facts,
			},
		})
	}
	evidence, err := collectPortablePythonAliasEvidenceV1(ctx, store, descriptor, checks)
	if err != nil {
		return fmt.Errorf("validate Python alias final-image evidence: %w", err)
	}
	if len(evidence) != len(aliases) {
		return fmt.Errorf("validate Python alias final-image evidence returned %d observations, want %d", len(evidence), len(aliases))
	}
	for index, alias := range aliases {
		observation := evidence[index]
		if observation.InvocationPath != alias.Destination {
			return fmt.Errorf("Python alias %q final invocation path is %q, want %q", alias.Name, observation.InvocationPath, alias.Destination)
		}
		if len(observation.LinkChain) == 0 {
			return fmt.Errorf("Python alias %q final image is not a symlink to its generated script", alias.Name)
		}
		first := observation.LinkChain[0]
		if first.Path != alias.Destination {
			return fmt.Errorf("Python alias %q first link path is %q, want %q", alias.Name, first.Path, alias.Destination)
		}
		if first.Target != alias.Target {
			return fmt.Errorf("Python alias %q link target is %q, want %q", alias.Name, first.Target, alias.Target)
		}
		if first.ResolvedPath != alias.Target {
			return fmt.Errorf("Python alias %q resolves to %q, want %q", alias.Name, first.ResolvedPath, alias.Target)
		}
		if observation.Terminal.Path != alias.Target {
			return fmt.Errorf("Python alias %q terminal path is %q, want %q", alias.Name, observation.Terminal.Path, alias.Target)
		}
		if strings.Contains(observation.Terminal.Path, string(filepath.Separator)+".reploy") {
			return fmt.Errorf("Python alias %q final terminal path leaks a host staging prefix", alias.Name)
		}
	}
	return nil
}

// inspectPortablePythonAliasDestinations checks source paths before ADD can
// merge anything. Existing real directories are allowed; their metadata is
// omitted from the archive. Missing parents are returned for 0755 entries.
func inspectPortablePythonAliasDestinations(
	ctx context.Context,
	store providerstore.Store,
	upstream deploy.ImageDescriptor,
	destinations []string,
) (result []string, resultErr error) {
	if ctx == nil {
		return nil, fmt.Errorf("inspect Python alias destinations requires a context")
	}
	if err := upstream.Validate(); err != nil {
		return nil, fmt.Errorf("inspect Python alias destinations upstream: %w", err)
	}
	if len(destinations) == 0 {
		return nil, fmt.Errorf("inspect Python alias destinations requires at least one destination")
	}
	for _, destination := range destinations {
		if err := validatePortablePythonAliasDestinationV1(destination); err != nil {
			return nil, err
		}
	}
	workspace, cleanupWorkspace, err := prepareSourceBuilderProbeWorkspaceV1(ctx, store, upstream.Platform)
	if err != nil {
		return nil, err
	}
	defer func() {
		if cleanupErr := cleanupWorkspace(); cleanupErr != nil {
			resultErr = errors.Join(resultErr, cleanupErr)
			result = nil
		}
	}()
	session, err := openSourceBuilderCollisionSessionV1(ctx, upstream, workspace)
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := session.Close(context.WithoutCancel(ctx)); closeErr != nil {
			resultErr = errors.Join(resultErr, closeErr)
			result = nil
		}
	}()

	// This shell body is fixed. Paths are positional data and cannot select a
	// command or a shell operation. It prints only missing ancestors.
	const checkScript = "for candidate in \"$@\"; do\n" +
		"  test ! -e \"$candidate\" && test ! -L \"$candidate\" || exit 1\n" +
		"  parent=\"${candidate%/*}\"\n" +
		"  test -n \"$parent\" || parent=/\n" +
		"  while test \"$parent\" != /; do\n" +
		"    test ! -L \"$parent\" || exit 2\n" +
		"    if test -e \"$parent\"; then\n" +
		"      test -d \"$parent\" || exit 3\n" +
		"    else\n" +
		"      printf '%s\\n' \"$parent\"\n" +
		"    fi\n" +
		"    next=\"${parent%/*}\"\n" +
		"    test -n \"$next\" || next=/\n" +
		"    parent=\"$next\"\n" +
		"  done\n" +
		"done"
	args := []string{
		"exec", "--user", "0:0", "--workdir", "/", session.containerName,
		"/bin/sh", "-c", checkScript, "reploy-validation",
	}
	args = append(args, destinations...)
	var stdout, stderr bytes.Buffer
	if err := session.runDockerCommand(CommandSpec{Name: "docker", Args: args}, RunOptions{
		Context: ctx, Stdout: &stdout, Stderr: &stderr,
	}); err != nil {
		return nil, imageValidationCommandError("Python alias source-image path safety", upstream.Platform.Canonical, stderr.String(), err)
	}
	seen := make(map[string]struct{})
	for _, line := range strings.Split(strings.TrimSuffix(stdout.String(), "\n"), "\n") {
		if line == "" {
			continue
		}
		if err := validatePortablePythonAliasDestinationV1(line); err != nil {
			return nil, fmt.Errorf("Python alias source-image path safety emitted invalid parent %q: %w", line, err)
		}
		if _, exists := seen[line]; exists {
			continue
		}
		seen[line] = struct{}{}
		result = append(result, line)
	}
	sort.Slice(result, func(left, right int) bool {
		leftDepth, rightDepth := strings.Count(result[left], "/"), strings.Count(result[right], "/")
		if leftDepth != rightDepth {
			return leftDepth < rightDepth
		}
		return result[left] < result[right]
	})
	return result, nil
}
