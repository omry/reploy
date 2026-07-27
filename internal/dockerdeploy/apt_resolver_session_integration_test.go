package dockerdeploy

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/providers"
	aptprovider "github.com/omry/reploy/internal/providers/apt"
	"github.com/omry/reploy/internal/providerstore"
)

func TestAPTResolverBaseProfileDockerIntegration(t *testing.T) {
	if os.Getenv("REPLOY_DOCKER_INTEGRATION") != "1" {
		t.Skip("set REPLOY_DOCKER_INTEGRATION=1 to run Docker integration evidence")
	}
	ctx := context.Background()
	platformName := os.Getenv("REPLOY_APT_INTEGRATION_PLATFORM")
	if platformName == "" {
		platformName = "linux/amd64"
	}
	platform, err := blueprint.ParsePlatform(platformName)
	if err != nil {
		t.Fatal(err)
	}
	debianArchitecture, err := aptprovider.DebianArchitectureForPlatformV1(platform)
	if err != nil {
		t.Fatal(err)
	}
	base := os.Getenv("REPLOY_APT_INTEGRATION_BASE")
	if base == "" {
		base = "debian:12-slim"
	}
	descriptor, _, err := ResolveBase(ctx, base, platform)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "no matching manifest") {
			t.Skipf("%s does not publish %s: %v", base, platform.Canonical, err)
		}
		t.Fatal(err)
	}
	store, err := providerstore.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	probeWorkspace := buildIntegrationProbeWorkspace(t, platform)
	resolverWorkspace, cleanupResolver, err := PrepareAPTResolverWorkspace(store)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanupResolver)
	session, err := OpenAPTResolverSession(ctx, descriptor, probeWorkspace, resolverWorkspace, RunOptions{Stdout: os.Stdout, Stderr: os.Stderr})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close(context.Background()) })
	validation, err := session.ProbeBaseProfile(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if validation.Profile.NativeArchitecture != debianArchitecture || len(validation.Profile.ForeignArchitectures) != 0 || len(validation.Executables) != len(aptprovider.RequiredBaseToolsV1()) {
		t.Fatalf("validation = %#v", validation)
	}
	if err := session.RefreshIndexes(ctx); err != nil {
		t.Fatal(err)
	}
	lists, err := os.ReadDir(filepath.Join(resolverWorkspace.HostDir, "lists"))
	if err != nil || len(lists) <= 1 {
		t.Fatalf("private APT lists were not populated: entries=%d err=%v", len(lists), err)
	}
	request := aptResolverTestRequest(
		t,
		blueprint.APTPackageRequest{Name: "ca-certificates", Exports: map[string]blueprint.ExecutableExport{}},
		blueprint.APTPackageRequest{Name: "hello", Exports: map[string]blueprint.ExecutableExport{
			"hello": {Executable: "/usr/bin/hello"},
		}},
		blueprint.APTPackageRequest{Name: "mawk", Exports: map[string]blueprint.ExecutableExport{
			"awk": {Executable: "/usr/bin/awk"},
		}},
	)
	plan, err := session.PlanPackages(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	foundDownload, foundBase := false, false
	for _, pkg := range plan.Packages {
		if pkg.Name == "hello" && pkg.CurrentVersion == "" && pkg.SelectedVersion != "" {
			foundDownload = true
		}
		if pkg.CurrentVersion != "" && pkg.CurrentVersion == pkg.SelectedVersion {
			foundBase = true
		}
	}
	if !foundDownload || !foundBase {
		t.Fatalf("dependency plan did not contain a mixed closure: %#v", plan)
	}
	baseState, err := session.ReadBasePackageState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(baseState) == 0 {
		t.Fatal("base package state did not contain retained dependencies")
	}
	for _, tuple := range baseState {
		if tuple.Status != aptprovider.InstalledPackageStatusV1 || tuple.Architecture != debianArchitecture && tuple.Architecture != "all" {
			t.Fatalf("invalid base package tuple: %#v", tuple)
		}
	}
	if err := session.DownloadPackages(ctx, request); err != nil {
		t.Fatal(err)
	}
	archives, err := os.ReadDir(filepath.Join(resolverWorkspace.HostDir, "archives"))
	if err != nil {
		t.Fatal(err)
	}
	foundDeb := false
	for _, entry := range archives {
		if !entry.IsDir() && filepath.Ext(entry.Name()) == ".deb" {
			foundDeb = true
		}
	}
	if !foundDeb {
		t.Fatalf("download transaction produced no .deb: %#v", archives)
	}
	inventory, err := session.InventoryArchives(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(inventory) == 0 {
		t.Fatal("download transaction produced no inventoried .deb")
	}
	bundlePackages, err := session.InspectArchives(ctx, []string{})
	if err != nil {
		t.Fatal(err)
	}
	foundHello := false
	for _, pkg := range bundlePackages {
		if pkg.Tuple.Name == "hello" && pkg.Tuple.Version != "" && pkg.Tuple.Architecture == debianArchitecture && pkg.FileListDigest != "" {
			foundHello = true
		}
	}
	if !foundHello {
		t.Fatalf("inspected bundle packages do not contain hello: %#v", bundlePackages)
	}
	bundle, err := session.PublishBundleArtifacts(ctx, store)
	if err != nil {
		t.Fatal(err)
	}
	if len(bundle.BundlePackages) == 0 {
		t.Fatal("published APT bundle contains no bundle-origin packages")
	}
	foundArchitectureAll := false
	for _, pkg := range bundle.BasePackages {
		if pkg.Tuple.Architecture == "all" {
			foundArchitectureAll = true
		}
	}
	for _, pkg := range bundle.BundlePackages {
		if pkg.Tuple.Architecture == "all" {
			foundArchitectureAll = true
		}
		if err := store.VerifyArtifact(pkg.Artifact); err != nil {
			t.Fatal(err)
		}
	}
	if !foundArchitectureAll {
		t.Fatal("published APT bundle did not contain a native-independent package")
	}
	result, reference, err := session.PublishResolvedBundle(ctx, store, aptResolverTestNode(t, request))
	if err != nil {
		t.Fatal(err)
	}
	if reference.Digest != result.Bundle.Identity {
		t.Fatalf("manifest reference = %#v, bundle identity = %s", reference, result.Bundle.Identity)
	}
	if err := session.Close(ctx); err != nil {
		t.Fatal(err)
	}
	var carrier, launcher providers.ValidatedExecutableInput
	for _, executable := range validation.Executables {
		if executable.Role == providers.ExecutableRoleCarrier {
			carrier = executable
		} else if executable.Role == providers.ExecutableRoleEnvironmentLauncher {
			launcher = executable
		}
	}
	transaction, err := (aptprovider.ComponentProvider{}).Materialize(providers.MaterializeInput{
		Bundle: result.Bundle, Profile: result.Profile, AssemblyParent: result.Bundle.Payload.Upstream,
		Carrier: carrier, EnvironmentLauncher: launcher,
		FinalImageConfig: aptIntegrationImageConfig(),
	})
	if err != nil {
		t.Fatal(err)
	}
	previousProbeWorkspace := prepareImageProbeWorkspace
	prepareImageProbeWorkspace = func(context.Context, providerstore.Store, blueprint.Platform) (PreparedProbeWorkspace, func() error, error) {
		return probeWorkspace, func() error { return nil }, nil
	}
	t.Cleanup(func() { prepareImageProbeWorkspace = previousProbeWorkspace })
	materialized, err := BuildAndAcceptMaterializationLayer(
		ctx, store, transaction, result.Bundle, platform,
		(ProviderMaterializationEvidenceRunner{Store: store}).Run,
		RunOptions{Context: ctx, Stdout: os.Stdout, Stderr: os.Stderr},
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		command := exec.Command("docker", "image", "rm", "--force", string(materialized.Image.Digest))
		_ = command.Run()
	})
	installed := runDockerIntegration(
		t, ctx, "run", "--rm", "--platform", platform.Canonical, "--entrypoint", "/bin/sh", string(materialized.Image.Digest), "-c",
		`test ! -e /.reploy-build && dpkg-query --show --showformat='${Status}\t${binary:Package}\n' hello`,
	)
	if strings.TrimSpace(installed) != "install ok installed\thello" {
		t.Fatalf("materialized hello state = %q", installed)
	}
	if len(materialized.Outputs) != 2 || materialized.Outputs[0].Name != "awk" || materialized.Outputs[1].Name != "hello" || materialized.Outputs[0].Evidence.Terminal.Owner == nil || materialized.Outputs[1].Evidence.Terminal.Owner == nil {
		t.Fatalf("materialized APT outputs = %#v", materialized.Outputs)
	}
	if len(materialized.Outputs[0].Evidence.LinkChain) < 2 || materialized.Outputs[0].Evidence.LinkChain[0].Kind != "alternative" || materialized.Outputs[0].Evidence.LinkChain[1].Kind != "alternative" {
		t.Fatalf("materialized awk alternatives = %#v", materialized.Outputs[0].Evidence.LinkChain)
	}
}

func TestAPTResolverRepositoryFailureDockerIntegration(t *testing.T) {
	if os.Getenv("REPLOY_DOCKER_INTEGRATION") != "1" || os.Getenv("REPLOY_APT_INTEGRATION_FAILURES") != "1" {
		t.Skip("set REPLOY_DOCKER_INTEGRATION=1 and REPLOY_APT_INTEGRATION_FAILURES=1 to run APT failure evidence")
	}
	ctx := context.Background()
	platform, err := blueprint.ParsePlatform("linux/amd64")
	if err != nil {
		t.Fatal(err)
	}
	contextDir := t.TempDir()
	dockerfile := `FROM debian:bookworm-slim
RUN rm -f /etc/apt/sources.list /etc/apt/sources.list.d/* \
 && printf 'deb [trusted=yes] http://127.0.0.1:9/debian stable main\n' >/etc/apt/sources.list \
 && printf 'Acquire::Retries "0";\nAcquire::http::Timeout "1";\n' >/etc/apt/apt.conf.d/99-reploy-integration
`
	dockerfilePath := filepath.Join(contextDir, "Dockerfile")
	if err := os.WriteFile(dockerfilePath, []byte(dockerfile), 0o600); err != nil {
		t.Fatal(err)
	}
	imageID := strings.TrimSpace(runDockerIntegration(
		t, ctx, "build", "--quiet", "--pull=false", "--network=none", "-f", dockerfilePath, contextDir,
	))
	t.Cleanup(func() {
		command := exec.Command("docker", "image", "rm", "--force", imageID)
		_ = command.Run()
	})
	inspection, err := runDockerOutput(ctx, "image", "inspect", imageID)
	if err != nil {
		t.Fatal(err)
	}
	descriptor, _, err := parseDockerImageInspection(imageID, platform, []byte(inspection))
	if err != nil {
		t.Fatal(err)
	}
	store, err := providerstore.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	probeWorkspace := buildIntegrationProbeWorkspace(t, platform)
	resolverWorkspace, cleanupResolver, err := PrepareAPTResolverWorkspace(store)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanupResolver)
	session, err := OpenAPTResolverSession(ctx, descriptor, probeWorkspace, resolverWorkspace, RunOptions{Stdout: os.Stdout, Stderr: os.Stderr})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close(context.Background()) })
	if _, err := session.ProbeBaseProfile(ctx); err != nil {
		t.Fatal(err)
	}
	err = session.RefreshIndexes(ctx)
	if err == nil || !strings.Contains(err.Error(), "apt.update_failed") || !strings.Contains(err.Error(), "select or rebuild a base image") || strings.Contains(err.Error(), "127.0.0.1") {
		t.Fatalf("repository failure = %v", err)
	}
}

func TestAPTResolverUpgradeDockerIntegration(t *testing.T) {
	if os.Getenv("REPLOY_DOCKER_INTEGRATION") != "1" || os.Getenv("REPLOY_APT_INTEGRATION_FAILURES") != "1" {
		t.Skip("set REPLOY_DOCKER_INTEGRATION=1 and REPLOY_APT_INTEGRATION_FAILURES=1 to run APT upgrade evidence")
	}
	ctx := context.Background()
	platform, err := blueprint.ParsePlatform("linux/amd64")
	if err != nil {
		t.Fatal(err)
	}
	contextDir := t.TempDir()
	dockerfile := `FROM debian:bookworm-slim
RUN set -eu; \
	mkdir -p /tmp/reploy-v0/DEBIAN /tmp/reploy-v0/usr/bin; \
	printf 'Package: hello\nVersion: 0\nArchitecture: amd64\nMaintainer: Reploy Tests <tests@example.invalid>\nDescription: Deliberately old package for upgrade testing\n' >/tmp/reploy-v0/DEBIAN/control; \
	printf '#!/bin/sh\nprintf "fixture version 0\\n"\n' >/tmp/reploy-v0/usr/bin/hello; \
	chmod 0755 /tmp/reploy-v0/usr/bin/hello; \
	dpkg-deb --build --root-owner-group /tmp/reploy-v0 /tmp/reploy-v0.deb >/dev/null; \
	dpkg -i /tmp/reploy-v0.deb >/dev/null; \
	rm -rf /tmp/reploy-v0 /tmp/reploy-v0.deb
`
	dockerfilePath := filepath.Join(contextDir, "Dockerfile")
	if err := os.WriteFile(dockerfilePath, []byte(dockerfile), 0o600); err != nil {
		t.Fatal(err)
	}
	imageID := strings.TrimSpace(runDockerIntegration(
		t, ctx, "build", "--quiet", "--pull=false", "--network=none", "-f", dockerfilePath, contextDir,
	))
	t.Cleanup(func() {
		command := exec.Command("docker", "image", "rm", "--force", imageID)
		_ = command.Run()
	})
	inspection, err := runDockerOutput(ctx, "image", "inspect", imageID)
	if err != nil {
		t.Fatal(err)
	}
	descriptor, _, err := parseDockerImageInspection(imageID, platform, []byte(inspection))
	if err != nil {
		t.Fatal(err)
	}
	store, err := providerstore.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	probeWorkspace := buildIntegrationProbeWorkspace(t, platform)
	resolverWorkspace, cleanupResolver, err := PrepareAPTResolverWorkspace(store)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanupResolver)
	session, err := OpenAPTResolverSession(ctx, descriptor, probeWorkspace, resolverWorkspace, RunOptions{Stdout: os.Stdout, Stderr: os.Stderr})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close(context.Background()) })
	validation, err := session.ProbeBaseProfile(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := session.RefreshIndexes(ctx); err != nil {
		t.Fatal(err)
	}
	request := aptResolverTestRequest(t, blueprint.APTPackageRequest{
		Name: "hello", Exports: map[string]blueprint.ExecutableExport{},
	})
	plan, err := session.PlanPackages(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	var selectedVersion string
	for _, pkg := range plan.Packages {
		if pkg.Name == "hello" && pkg.CurrentVersion == "0" && pkg.SelectedVersion != "0" {
			selectedVersion = pkg.SelectedVersion
			break
		}
	}
	if selectedVersion == "" {
		t.Fatalf("upgrade plan = %#v", plan)
	}
	if _, err := session.ReadBasePackageState(ctx); err != nil {
		t.Fatal(err)
	}
	if err := session.DownloadPackages(ctx, request); err != nil {
		t.Fatal(err)
	}
	if _, err := session.InventoryArchives(ctx); err != nil {
		t.Fatal(err)
	}
	packages, err := session.InspectArchives(ctx, []string{})
	if err != nil {
		t.Fatal(err)
	}
	if len(packages) != 1 || packages[0].Tuple.Name != "hello" || packages[0].Tuple.Version != selectedVersion || packages[0].BasePredecessor == nil || packages[0].BasePredecessor.Version != "0" {
		t.Fatalf("upgrade bundle packages = %#v", packages)
	}
	if _, err := session.PublishBundleArtifacts(ctx, store); err != nil {
		t.Fatal(err)
	}
	result, _, err := session.PublishResolvedBundle(ctx, store, aptResolverTestNode(t, request))
	if err != nil {
		t.Fatal(err)
	}
	if err := session.Close(ctx); err != nil {
		t.Fatal(err)
	}
	var carrier, launcher providers.ValidatedExecutableInput
	for _, executable := range validation.Executables {
		if executable.Role == providers.ExecutableRoleCarrier {
			carrier = executable
		} else if executable.Role == providers.ExecutableRoleEnvironmentLauncher {
			launcher = executable
		}
	}
	transaction, err := (aptprovider.ComponentProvider{}).Materialize(providers.MaterializeInput{
		Bundle: result.Bundle, Profile: result.Profile, AssemblyParent: result.Bundle.Payload.Upstream,
		Carrier: carrier, EnvironmentLauncher: launcher, FinalImageConfig: aptIntegrationImageConfig(),
	})
	if err != nil {
		t.Fatal(err)
	}
	materialized, err := BuildAndAcceptMaterializationLayer(
		ctx, store, transaction, result.Bundle, platform,
		(ProviderMaterializationEvidenceRunner{Store: store}).Run,
		RunOptions{Context: ctx, Stdout: os.Stdout, Stderr: os.Stderr},
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		command := exec.Command("docker", "image", "rm", "--force", string(materialized.Image.Digest))
		_ = command.Run()
	})
	installedVersion := runDockerIntegration(
		t, ctx, "run", "--rm", "--entrypoint", "/usr/bin/dpkg-query", string(materialized.Image.Digest),
		"--show", "--showformat=${Version}", "hello",
	)
	if strings.TrimSpace(installedVersion) != selectedVersion {
		t.Fatalf("materialized hello version = %q, want %q", installedVersion, selectedVersion)
	}
}

func aptIntegrationImageConfig() providers.ImageConfigPolicy {
	return providers.ImageConfigPolicy{
		User: "0:0", WorkingDir: "/", Environment: []providers.EnvironmentVariable{},
		Entrypoint: []string{}, Command: []string{}, Healthcheck: providers.ImageHealthcheckNone,
		StopSignal: "SIGTERM", Labels: []providers.ImageLabel{},
	}
}
