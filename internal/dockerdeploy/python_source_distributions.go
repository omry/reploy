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

	"github.com/omry/reploy/internal/canonical"
	"github.com/omry/reploy/internal/providers"
	pythonprovider "github.com/omry/reploy/internal/providers/python"
	"github.com/omry/reploy/internal/providerstore"
)

const pythonSourceDistributionDirectory = "source-artifacts"

type PreparedPythonSourceDistribution struct {
	Distribution           string
	Version                string
	ArchiveRoot            string
	HostDir                string
	ContainerDir           string
	SourceInputDigest      canonical.Digest
	BuildEnvironmentDigest canonical.Digest
	BuilderProfile         string
	BuildSettings          providers.CanonicalProviderData
	Artifact               providerstore.ArtifactDescriptor
}

type PythonSourceBuildIdentityV1 struct {
	BuildEnvironmentDigest canonical.Digest
	BuilderProfile         string
	BuildSettings          providers.CanonicalProviderData
}

// PublishBuiltPythonSourceDistributions validates and retains the exact sdists
// before replacing source projections with securely extracted closed inputs.
func PublishBuiltPythonSourceDistributions(
	ctx context.Context,
	store providerstore.Store,
	prepared PreparedPythonResolverArtifacts,
	snapshots []PreparedPythonSourceSnapshot,
	buildEnvironmentDigest canonical.Digest,
) (distributions []PreparedPythonSourceDistribution, err error) {
	identities := make(map[string]PythonSourceBuildIdentityV1, len(snapshots))
	for _, snapshot := range snapshots {
		identities[snapshot.Distribution] = PythonSourceBuildIdentityV1{
			BuildEnvironmentDigest: buildEnvironmentDigest,
			BuilderProfile:         pythonprovider.SourceBuilderProfileV1,
			BuildSettings:          pythonprovider.CanonicalSourceBuildSettingsV1(),
		}
	}
	return PublishBuiltPythonSourceDistributionsWithIdentities(
		ctx, store, prepared, snapshots, identities,
	)
}

// PublishBuiltPythonSourceDistributionsWithIdentities retains each selected
// source's recipe-owned build protocol and exact builder environment.
func PublishBuiltPythonSourceDistributionsWithIdentities(
	ctx context.Context,
	store providerstore.Store,
	prepared PreparedPythonResolverArtifacts,
	snapshots []PreparedPythonSourceSnapshot,
	identities map[string]PythonSourceBuildIdentityV1,
) (distributions []PreparedPythonSourceDistribution, err error) {
	if ctx == nil {
		return nil, fmt.Errorf("publish Python source distributions requires a context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validatePreparedPythonResolverArtifactLayout(prepared); err != nil {
		return nil, err
	}
	if snapshots == nil {
		return nil, fmt.Errorf("Python source snapshots must use an array")
	}
	if identities == nil {
		return nil, fmt.Errorf("Python source build identities must use a map")
	}
	if err := validatePreparedPythonSourceSnapshots(prepared, snapshots); err != nil {
		return nil, err
	}
	if len(identities) != len(snapshots) {
		return nil, fmt.Errorf("Python source build identities must exactly match selected snapshots")
	}
	for _, snapshot := range snapshots {
		identity, found := identities[snapshot.Distribution]
		if !found {
			return nil, fmt.Errorf("Python source build identity for %q is missing", snapshot.Distribution)
		}
		if err := validatePythonSourceBuildIdentityV1(identity); err != nil {
			return nil, fmt.Errorf("Python source build identity for %q: %w", snapshot.Distribution, err)
		}
	}
	entries, err := os.ReadDir(prepared.OutputHostDir)
	if err != nil {
		return nil, fmt.Errorf("read Python source distribution output: %w", err)
	}
	if len(entries) != len(snapshots) {
		names := make([]string, len(entries))
		for index, entry := range entries {
			names[index] = entry.Name()
		}
		return nil, fmt.Errorf(
			"Python local-source build must produce exactly one sdist per source; got %d outputs for %d sources: %q",
			len(entries), len(snapshots), names,
		)
	}

	type builtDistribution struct {
		filename   string
		hostPath   string
		descriptor providerstore.ArtifactDescriptor
		metadata   pythonprovider.SourceDistributionMetadataV1
	}
	builtByDistribution := make(map[string]builtDistribution, len(entries))
	for _, entry := range entries {
		filename := entry.Name()
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 ||
			!strings.HasSuffix(strings.ToLower(filename), ".tar.gz") {
			return nil, fmt.Errorf(
				"Python local-source build output %q must be one regular .tar.gz sdist; direct-wheel fallback is not supported",
				filename,
			)
		}
		hostPath := filepath.Join(prepared.OutputHostDir, filename)
		descriptor, metadata, err := pythonprovider.DescribeSourceDistributionFileV1(
			hostPath, path.Join("sdists", filename),
		)
		if err != nil {
			return nil, fmt.Errorf("validate Python source distribution %q: %w", filename, err)
		}
		if _, found := builtByDistribution[metadata.Distribution]; found {
			return nil, fmt.Errorf(
				"Python local-source build produced multiple sdists for distribution %q",
				metadata.Distribution,
			)
		}
		builtByDistribution[metadata.Distribution] = builtDistribution{
			filename: filename, hostPath: hostPath, descriptor: descriptor, metadata: metadata,
		}
	}

	ordered := make([]builtDistribution, 0, len(snapshots))
	for _, snapshot := range snapshots {
		built, found := builtByDistribution[snapshot.Distribution]
		if !found {
			return nil, fmt.Errorf(
				"Python local-source build produced no sdist for declared distribution %q",
				snapshot.Distribution,
			)
		}
		ordered = append(ordered, built)
		delete(builtByDistribution, snapshot.Distribution)
	}
	if len(builtByDistribution) != 0 {
		for distribution := range builtByDistribution {
			return nil, fmt.Errorf(
				"Python local-source build produced an undeclared distribution %q", distribution,
			)
		}
	}
	for _, built := range ordered {
		file, err := os.Open(built.hostPath)
		if err != nil {
			return nil, err
		}
		published, publishErr := store.PublishExpected(ctx, built.descriptor, file)
		closeErr := file.Close()
		if publishErr != nil || closeErr != nil {
			return nil, errors.Join(publishErr, closeErr)
		}
		if published != built.descriptor {
			return nil, fmt.Errorf("published Python source distribution %q changed identity", built.filename)
		}
	}

	if err := os.Chmod(prepared.InputHostDir, 0o700); err != nil {
		return nil, fmt.Errorf("make Python resolver input writable for retained source distributions: %w", err)
	}
	distributionRoot := filepath.Join(prepared.InputHostDir, pythonSourceDistributionDirectory)
	defer func() {
		if protectErr := os.Chmod(distributionRoot, 0o500); protectErr != nil && !errors.Is(protectErr, os.ErrNotExist) {
			err = errors.Join(err, fmt.Errorf("protect retained Python source distribution root: %w", protectErr))
			distributions = nil
		}
		if protectErr := os.Chmod(prepared.InputHostDir, 0o500); protectErr != nil {
			err = errors.Join(err, fmt.Errorf("restore Python resolver input protection: %w", protectErr))
			distributions = nil
		}
	}()
	snapshotRoot := filepath.Join(prepared.InputHostDir, pythonSourceSnapshotDirectory)
	makePythonResolverWorkspaceRemovable(snapshotRoot)
	if err := os.RemoveAll(snapshotRoot); err != nil {
		return nil, fmt.Errorf("remove provisional Python source projections: %w", err)
	}
	if err := os.Mkdir(distributionRoot, 0o700); err != nil {
		return nil, fmt.Errorf("create retained Python source distribution root: %w", err)
	}

	distributions = make([]PreparedPythonSourceDistribution, 0, len(snapshots))
	for index, snapshot := range snapshots {
		built := ordered[index]
		identity := identities[snapshot.Distribution]
		hostDir := filepath.Join(distributionRoot, snapshot.Distribution)
		if err := os.Mkdir(hostDir, 0o700); err != nil {
			return nil, fmt.Errorf("create extracted Python source distribution %q: %w", snapshot.Distribution, err)
		}
		blob, err := store.InspectArtifactPath(built.descriptor)
		if err != nil {
			return nil, err
		}
		metadata, err := pythonprovider.ExtractSourceDistributionFileV1(blob, hostDir)
		if err != nil {
			return nil, fmt.Errorf("extract retained Python source distribution %q: %w", built.filename, err)
		}
		if metadata != built.metadata {
			return nil, fmt.Errorf("retained Python source distribution %q changed metadata", built.filename)
		}
		if err := protectPythonSourceDistribution(hostDir); err != nil {
			return nil, err
		}
		if err := os.Remove(built.hostPath); err != nil {
			return nil, fmt.Errorf("clear Python source distribution output %q: %w", built.filename, err)
		}
		distributions = append(distributions, PreparedPythonSourceDistribution{
			Distribution: snapshot.Distribution, Version: metadata.Version, ArchiveRoot: metadata.Root,
			HostDir: hostDir,
			ContainerDir: path.Join(
				prepared.InputContainerDir, pythonSourceDistributionDirectory, snapshot.Distribution,
			),
			SourceInputDigest:      snapshot.SourceInputDigest,
			BuildEnvironmentDigest: identity.BuildEnvironmentDigest,
			BuilderProfile:         identity.BuilderProfile,
			BuildSettings:          identity.BuildSettings,
			Artifact:               built.descriptor,
		})
	}
	return distributions, nil
}

func validatePreparedPythonSourceDistributions(
	prepared PreparedPythonResolverArtifacts,
	distributions []PreparedPythonSourceDistribution,
) error {
	for index, distribution := range distributions {
		if distribution.Distribution == "" ||
			pythonprovider.NormalizeDistributionName(distribution.Distribution) != distribution.Distribution {
			return fmt.Errorf(
				"prepared Python source distribution %d has noncanonical name %q",
				index, distribution.Distribution,
			)
		}
		if index > 0 && distributions[index-1].Distribution >= distribution.Distribution {
			return fmt.Errorf("prepared Python source distributions must be unique and sorted")
		}
		wantHost := filepath.Join(
			prepared.InputHostDir, pythonSourceDistributionDirectory, distribution.Distribution,
		)
		wantContainer := path.Join(
			prepared.InputContainerDir, pythonSourceDistributionDirectory, distribution.Distribution,
		)
		if distribution.HostDir != wantHost || distribution.ContainerDir != wantContainer {
			return fmt.Errorf(
				"prepared Python source distribution %q does not use the resolver input layout",
				distribution.Distribution,
			)
		}
		info, err := os.Lstat(distribution.HostDir)
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("prepared Python source distribution %q must be a real directory", distribution.Distribution)
		}
		if err := distribution.SourceInputDigest.Validate(); err != nil {
			return fmt.Errorf("prepared Python source distribution %q input digest: %w", distribution.Distribution, err)
		}
		if err := distribution.BuildEnvironmentDigest.Validate(); err != nil {
			return fmt.Errorf("prepared Python source distribution %q build environment digest: %w", distribution.Distribution, err)
		}
		if err := validatePythonSourceBuildIdentityV1(PythonSourceBuildIdentityV1{
			BuildEnvironmentDigest: distribution.BuildEnvironmentDigest,
			BuilderProfile:         distribution.BuilderProfile,
			BuildSettings:          distribution.BuildSettings,
		}); err != nil {
			return fmt.Errorf("prepared Python source distribution %q build identity: %w", distribution.Distribution, err)
		}
		if err := distribution.Artifact.Validate(); err != nil {
			return fmt.Errorf("prepared Python source distribution %q artifact: %w", distribution.Distribution, err)
		}
		if distribution.Artifact.Kind != "sdist" {
			return fmt.Errorf("prepared Python source distribution %q artifact must be an sdist", distribution.Distribution)
		}
		if distribution.Version == "" || distribution.ArchiveRoot == "" {
			return fmt.Errorf("prepared Python source distribution %q metadata is incomplete", distribution.Distribution)
		}
	}
	return nil
}

func validatePythonSourceBuildIdentityV1(identity PythonSourceBuildIdentityV1) error {
	if err := identity.BuildEnvironmentDigest.Validate(); err != nil {
		return fmt.Errorf("build environment digest: %w", err)
	}
	return pythonprovider.ValidateSourceBuildIdentityV2(identity.BuilderProfile, identity.BuildSettings)
}

func protectPythonSourceDistribution(root string) error {
	directories := []string{}
	err := filepath.WalkDir(root, func(filename string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			directories = append(directories, filename)
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("retained Python source distribution contains unsupported file %q", filename)
		}
		return os.Chmod(filename, info.Mode().Perm()&^0o222)
	})
	if err != nil {
		return err
	}
	sort.Sort(sort.Reverse(sort.StringSlice(directories)))
	for _, directory := range directories {
		if err := os.Chmod(directory, 0o555); err != nil {
			return err
		}
	}
	return nil
}
