package dockerdeploy

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/providers"
	pythonprovider "github.com/omry/reploy/internal/providers/python"
	"github.com/omry/reploy/internal/providerstore"
)

// PublishBuiltPythonSourceWheels closes the writable source-build output,
// publishes only its validated wheels, and exposes those exact store objects
// through the resolver's read-only input for the later dependency resolver.
func PublishBuiltPythonSourceWheels(
	ctx context.Context,
	store providerstore.Store,
	prepared PreparedPythonResolverArtifacts,
	component string,
	snapshots []PreparedPythonSourceSnapshot,
	reusable []providerstore.ArtifactDescriptor,
) (sources []providers.ResolvedSourceInput, effective []providerstore.ArtifactDescriptor, err error) {
	if ctx == nil {
		return nil, nil, fmt.Errorf("publish Python source wheels requires a context")
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if err := blueprint.ValidateProviderIdentifier("Python source wheel component", component); err != nil {
		return nil, nil, err
	}
	if err := validatePreparedPythonResolverArtifactLayout(prepared); err != nil {
		return nil, nil, err
	}
	if snapshots == nil || reusable == nil {
		return nil, nil, fmt.Errorf("Python source snapshots and reusable wheels must use arrays")
	}
	if err := validatePreparedPythonSourceSnapshots(prepared, snapshots); err != nil {
		return nil, nil, err
	}

	entries, err := os.ReadDir(prepared.OutputHostDir)
	if err != nil {
		return nil, nil, fmt.Errorf("read Python source wheel output: %w", err)
	}
	if len(entries) != len(snapshots) {
		names := make([]string, len(entries))
		for index, entry := range entries {
			names[index] = entry.Name()
		}
		return nil, nil, fmt.Errorf(
			"Python source build produced %d output entries for %d source snapshots: %q",
			len(entries), len(snapshots), names,
		)
	}

	type builtWheel struct {
		filename   string
		path       string
		descriptor providerstore.ArtifactDescriptor
		metadata   pythonprovider.SourceWheelMetadataV1
	}
	builtByDistribution := make(map[string]builtWheel, len(entries))
	for _, entry := range entries {
		filename := entry.Name()
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || !strings.HasSuffix(strings.ToLower(filename), ".whl") {
			return nil, nil, fmt.Errorf("Python source build output %q must be a regular wheel file", filename)
		}
		filenamePath := filepath.Join(prepared.OutputHostDir, filename)
		descriptor, metadata, err := pythonprovider.DescribeSourceWheelFileV1(filenamePath, path.Join("wheels", filename))
		if err != nil {
			return nil, nil, fmt.Errorf("inspect Python source build output %q: %w", filename, err)
		}
		if _, found := builtByDistribution[metadata.Distribution]; found {
			return nil, nil, fmt.Errorf("Python source build produced multiple wheels for distribution %q", metadata.Distribution)
		}
		builtByDistribution[metadata.Distribution] = builtWheel{
			filename: filename, path: filenamePath, descriptor: descriptor, metadata: metadata,
		}
	}

	sources = make([]providers.ResolvedSourceInput, 0, len(snapshots))
	built := make([]builtWheel, 0, len(snapshots))
	for _, snapshot := range snapshots {
		wheel, found := builtByDistribution[snapshot.Distribution]
		if !found {
			return nil, nil, fmt.Errorf("Python source build produced no wheel for distribution %q", snapshot.Distribution)
		}
		source, err := pythonprovider.NewResolvedSourceInputV1(
			component, snapshot.Distribution, snapshot.SourceManifestDigest, wheel.descriptor, wheel.metadata,
		)
		if err != nil {
			return nil, nil, err
		}
		sources = append(sources, source)
		built = append(built, wheel)
		delete(builtByDistribution, snapshot.Distribution)
	}
	if len(builtByDistribution) != 0 {
		for distribution := range builtByDistribution {
			return nil, nil, fmt.Errorf("Python source build produced an undeclared distribution %q", distribution)
		}
	}

	effectiveByFilename := make(map[string]providerstore.ArtifactDescriptor, len(reusable)+len(built))
	for _, artifact := range reusable {
		if err := artifact.Validate(); err != nil {
			return nil, nil, fmt.Errorf("Python source wheel reusable artifact: %w", err)
		}
		filename := filepath.Base(filepath.FromSlash(artifact.LogicalPath))
		if artifact.Kind != "wheel" || !strings.HasSuffix(strings.ToLower(filename), ".whl") {
			return nil, nil, fmt.Errorf("Python source wheel reusable artifact %q must be a wheel", artifact.LogicalPath)
		}
		if _, found := effectiveByFilename[filename]; found {
			return nil, nil, fmt.Errorf("Python source wheel reusable artifacts contain duplicate filename %q", filename)
		}
		effectiveByFilename[filename] = artifact
	}
	for _, wheel := range built {
		effectiveByFilename[wheel.filename] = wheel.descriptor
	}

	for _, wheel := range built {
		file, err := os.Open(wheel.path)
		if err != nil {
			return nil, nil, err
		}
		published, publishErr := store.PublishExpected(ctx, wheel.descriptor, file)
		closeErr := file.Close()
		if publishErr != nil || closeErr != nil {
			return nil, nil, errors.Join(publishErr, closeErr)
		}
		if published != wheel.descriptor {
			return nil, nil, fmt.Errorf("published Python source wheel %q changed identity", wheel.filename)
		}
	}

	if err := os.Chmod(prepared.InputHostDir, 0o700); err != nil {
		return nil, nil, fmt.Errorf("make Python resolver input writable for source wheels: %w", err)
	}
	defer func() {
		if protectErr := os.Chmod(prepared.InputHostDir, 0o500); protectErr != nil {
			err = errors.Join(err, fmt.Errorf("restore Python resolver input protection: %w", protectErr))
			sources = nil
			effective = nil
		}
	}()
	for _, wheel := range built {
		destination := filepath.Join(prepared.InputHostDir, wheel.filename)
		if removeErr := os.Remove(destination); removeErr != nil && !os.IsNotExist(removeErr) {
			return nil, nil, fmt.Errorf("replace staged Python source wheel %q: %w", wheel.filename, removeErr)
		}
		blob, err := store.InspectArtifactPath(wheel.descriptor)
		if err != nil {
			return nil, nil, err
		}
		if err := os.Link(blob, destination); err != nil {
			return nil, nil, fmt.Errorf("stage Python source wheel %q: %w", wheel.filename, err)
		}
		if err := os.Remove(wheel.path); err != nil {
			return nil, nil, fmt.Errorf("clear Python source build output %q: %w", wheel.filename, err)
		}
	}

	effective = make([]providerstore.ArtifactDescriptor, 0, len(effectiveByFilename))
	for _, artifact := range effectiveByFilename {
		effective = append(effective, artifact)
	}
	sort.Slice(effective, func(left int, right int) bool {
		return effective[left].LogicalPath < effective[right].LogicalPath
	})
	return sources, effective, nil
}

func validatePreparedPythonSourceSnapshots(prepared PreparedPythonResolverArtifacts, snapshots []PreparedPythonSourceSnapshot) error {
	for index, snapshot := range snapshots {
		if snapshot.Distribution == "" || pythonprovider.NormalizeDistributionName(snapshot.Distribution) != snapshot.Distribution {
			return fmt.Errorf("Python source snapshot %d has noncanonical distribution %q", index, snapshot.Distribution)
		}
		if index > 0 && snapshots[index-1].Distribution >= snapshot.Distribution {
			return fmt.Errorf("Python source snapshots must be unique and sorted by distribution")
		}
		wantHost := filepath.Join(prepared.InputHostDir, pythonSourceSnapshotDirectory, snapshot.Distribution)
		wantContainer := path.Join(prepared.InputContainerDir, pythonSourceSnapshotDirectory, snapshot.Distribution)
		if snapshot.HostDir != wantHost || snapshot.ContainerDir != wantContainer {
			return fmt.Errorf("Python source snapshot %q does not use the prepared resolver input layout", snapshot.Distribution)
		}
		info, err := os.Lstat(snapshot.HostDir)
		if err != nil {
			return fmt.Errorf("inspect Python source snapshot %q: %w", snapshot.Distribution, err)
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("Python source snapshot %q must be a real directory", snapshot.Distribution)
		}
		if err := snapshot.SourceManifestDigest.Validate(); err != nil {
			return fmt.Errorf("Python source snapshot %q manifest digest: %w", snapshot.Distribution, err)
		}
	}
	return nil
}
