package dockerdeploy

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
	aptprovider "github.com/omry/reploy/internal/providers/apt"
	"github.com/omry/reploy/internal/providerstore"
	"github.com/omry/reploy/internal/toolcatalog"
	"github.com/omry/reploy/internal/toolrequest"
)

func writeSourceBuilderTestRecipe(t *testing.T, distribution string, requires string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range map[string]string{
		"setup.py":                "from setuptools import setup\n",
		"pyproject.toml":          "[tool.ruff]\n",
		LocalSourceRecipeFilename: "schema: 1\nproject: " + distribution + "\ntype: python\nbuild: setuptools-legacy\nrequires: " + requires + "\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func stubSourceBuilderTarget(t *testing.T, target toolcatalog.TargetIdentityV1, calls *int) {
	t.Helper()
	previous := observeSourceBuilderTargetV1
	t.Cleanup(func() { observeSourceBuilderTargetV1 = previous })
	observeSourceBuilderTargetV1 = func(context.Context, providerstore.Store, deploy.ImageDescriptor) (toolcatalog.TargetIdentityV1, error) {
		if calls != nil {
			*calls++
		}
		return target, nil
	}
}

func sourceBuilderJavaTarget(osID string, version string) toolcatalog.TargetIdentityV1 {
	return toolcatalog.TargetIdentityV1{
		Platform: "linux/amd64", OSReleaseID: osID, VersionID: version,
		OCIArchitecture: "amd64", NativeArchitecture: "amd64", PackageManager: "apt",
	}
}

func planSourceBuilderJavaForTest(t *testing.T, distributions ...string) (*SourceBuilderPortableToolPlanV1, PlanSourceBuilderPortableToolsInputV1) {
	t.Helper()
	fixture := newPreparedPythonGraphReuseFixture(t)
	recipes := make([]SourceBuilderSelectedRecipeV1, 0, len(distributions))
	for _, distribution := range distributions {
		dir := writeSourceBuilderTestRecipe(t, distribution, "[tool:java==21]")
		recipe, err := ReadPythonLocalSourceRecipeV1(dir, distribution)
		if err != nil {
			t.Fatal(err)
		}
		recipes = append(recipes, SourceBuilderSelectedRecipeV1{
			Distribution: distribution, Recipe: recipe,
		})
	}
	stubSourceBuilderTarget(t, sourceBuilderJavaTarget("debian", "12"), nil)
	input := PlanSourceBuilderPortableToolsInputV1{
		Store: fixture.store, Base: testProbeImageDescriptor(t, "linux/amd64"), ProviderPlan: fixture.request.Plan,
		SelectedRecipes: recipes, BlueprintDigest: rendererDigest("b"), ReployVersion: "0.7.0.dev1",
	}
	plan, err := PlanSourceBuilderPortableToolsV1(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if plan == nil {
		t.Fatal("Java recipes planned no portable tools")
	}
	return plan, input
}

func TestPlanSourceBuilderPortableToolsV1PlansNothingWithoutRecipeRequirements(t *testing.T) {
	fixture := newPreparedPythonGraphReuseFixture(t)
	observed := 0
	stubSourceBuilderTarget(t, sourceBuilderJavaTarget("debian", "12"), &observed)
	plainDir := writeSourceBuilderTestRecipe(t, "plain", "[]")
	plain, err := ReadPythonLocalSourceRecipeV1(plainDir, "plain")
	if err != nil {
		t.Fatal(err)
	}
	plan, err := PlanSourceBuilderPortableToolsV1(context.Background(), PlanSourceBuilderPortableToolsInputV1{
		Store: fixture.store, Base: testProbeImageDescriptor(t, "linux/amd64"), ProviderPlan: fixture.request.Plan,
		SelectedRecipes: []SourceBuilderSelectedRecipeV1{
			{Distribution: "plain", Recipe: plain},
			{Distribution: "unrecipe", Recipe: PythonLocalSourceRecipeV1{Requirements: []toolrequest.CanonicalRequirementGroupV1{}}},
		},
		BlueprintDigest: rendererDigest("b"), ReployVersion: "0.7.0.dev1",
	})
	if err != nil || plan != nil || observed != 0 {
		t.Fatalf("plan = %#v, err = %v, target observations = %d", plan, err, observed)
	}
	if _, err := PlanSourceBuilderPortableToolsV1(context.Background(), PlanSourceBuilderPortableToolsInputV1{SelectedRecipes: nil}); err == nil || !strings.Contains(err.Error(), "must use an array") {
		t.Fatalf("nil overrides error = %v", err)
	}
}

func TestPlanSourceBuilderPortableToolsV1ResolvesEachRecipeScopeAgainstTheEmbeddedCatalog(t *testing.T) {
	plan, input := planSourceBuilderJavaForTest(t, "alpha", "beta")
	if len(plan.Plan.Tools) != 2 || plan.Plan.Tools[0].Scope != "source-builder:alpha" || plan.Plan.Tools[1].Scope != "source-builder:beta" {
		t.Fatalf("plan tools = %#v", plan.Plan.Tools)
	}
	for _, entry := range plan.Plan.Tools {
		if entry.Provenance.Tool != "java" || entry.Provenance.Version != "21" || entry.Provenance.Revision != "1" ||
			len(entry.Exports) != 2 || entry.Exports[0].Name != "java" || entry.Exports[1].Name != "javac" ||
			len(entry.ValidationProfiles) != 1 || entry.Runtime != nil {
			t.Fatalf("plan entry = %#v", entry)
		}
	}
	if len(plan.Closures) != 2 || plan.Closures[0].Identity != plan.Plan.Tools[0].SelectedClosureDigest {
		t.Fatalf("closures = %#v", plan.Closures)
	}
	if plan.Target != sourceBuilderJavaTarget("debian", "12") || plan.Snapshot.Digest == "" ||
		!strings.Contains(plan.Snapshot.CanonicalJSON, `"os_release_id":"debian"`) ||
		!strings.Contains(plan.Snapshot.CanonicalJSON, string(input.BlueprintDigest)) ||
		!strings.Contains(plan.Snapshot.CanonicalJSON, `"version":"0.7.0"`) {
		t.Fatalf("target/snapshot = %#v / %s", plan.Target, plan.Snapshot.CanonicalJSON)
	}
	for _, distribution := range []string{"alpha", "beta"} {
		recipe, found := plan.RecipeFor(distribution)
		if !found || recipe.Scope != "source-builder:"+distribution || recipe.Digest.Validate() != nil ||
			len(recipe.Groups) != 1 || recipe.Groups[0].Tool != "java" || recipe.Groups[0].Context != "build" {
			t.Fatalf("recipe %s = %#v (found=%v)", distribution, recipe, found)
		}
	}
	if len(plan.DAG.Domains) != 2 {
		t.Fatalf("DAG domains = %#v", plan.DAG.Domains)
	}
	owner := plan.DAG.Domains[0].Filesystem.Owner
	for _, domain := range plan.DAG.Domains {
		if domain.Filesystem.ID != "source-builder/filesystem" || domain.Exports.ID != "source-builder/exports" ||
			domain.PackageManager.Owner != owner || domain.Exports.Owner != owner {
			t.Fatalf("domain = %#v", domain)
		}
	}
	if err := providers.ValidatePortableToolProviderDAGV1(plan.DAG); err != nil {
		t.Fatal(err)
	}
	if _, found := plan.RecipeFor("gamma"); found {
		t.Fatal("unplanned distribution reported a recipe")
	}
}

func TestPlanSourceBuilderPortableToolsV1FailsClosedBeforeAcquisitionForUnsupportedTargets(t *testing.T) {
	fixture := newPreparedPythonGraphReuseFixture(t)
	stubSourceBuilderTarget(t, sourceBuilderJavaTarget("ubuntu", "24.04"), nil)
	dir := writeSourceBuilderTestRecipe(t, "alpha", "[tool:java==21]")
	recipe, readErr := ReadPythonLocalSourceRecipeV1(dir, "alpha")
	if readErr != nil {
		t.Fatal(readErr)
	}
	_, err := PlanSourceBuilderPortableToolsV1(context.Background(), PlanSourceBuilderPortableToolsInputV1{
		Store: fixture.store, Base: testProbeImageDescriptor(t, "linux/amd64"), ProviderPlan: fixture.request.Plan,
		SelectedRecipes: []SourceBuilderSelectedRecipeV1{{Distribution: "alpha", Recipe: recipe}},
		BlueprintDigest: rendererDigest("b"), ReployVersion: "0.7.0.dev1",
	})
	if err == nil || !strings.Contains(err.Error(), "no target leaf") {
		t.Fatalf("error = %v, want unsupported target rejection", err)
	}
}

func TestPlanSourceBuilderPortableToolsV1RequiresAPythonNodeAndAReleaseVersion(t *testing.T) {
	fixture := newPreparedPythonGraphReuseFixture(t)
	stubSourceBuilderTarget(t, sourceBuilderJavaTarget("debian", "12"), nil)
	dir := writeSourceBuilderTestRecipe(t, "alpha", "[tool:java==21]")
	recipe, err := ReadPythonLocalSourceRecipeV1(dir, "alpha")
	if err != nil {
		t.Fatal(err)
	}
	input := PlanSourceBuilderPortableToolsInputV1{
		Store: fixture.store, Base: testProbeImageDescriptor(t, "linux/amd64"), ProviderPlan: fixture.request.Plan,
		SelectedRecipes: []SourceBuilderSelectedRecipeV1{{Distribution: "alpha", Recipe: recipe}},
		BlueprintDigest: rendererDigest("b"), ReployVersion: "latest",
	}
	if _, err := PlanSourceBuilderPortableToolsV1(context.Background(), input); err == nil || !strings.Contains(err.Error(), "not a release coordinate") {
		t.Fatalf("version error = %v", err)
	}
	input.ReployVersion = "0.7.0.dev1"
	input.ProviderPlan = providers.ProviderPlanV1{Nodes: []providers.NodeSpec{{ID: "base", Provider: blueprint.ComponentTypeBase}}}
	if _, err := PlanSourceBuilderPortableToolsV1(context.Background(), input); err == nil || !strings.Contains(err.Error(), "Python provider node") {
		t.Fatalf("owner error = %v", err)
	}
	previous := observeSourceBuilderTargetV1
	t.Cleanup(func() { observeSourceBuilderTargetV1 = previous })
	observeSourceBuilderTargetV1 = func(context.Context, providerstore.Store, deploy.ImageDescriptor) (toolcatalog.TargetIdentityV1, error) {
		return toolcatalog.TargetIdentityV1{}, errors.New("base observation failed")
	}
	input.ProviderPlan = fixture.request.Plan
	if _, err := PlanSourceBuilderPortableToolsV1(context.Background(), input); err == nil || !strings.Contains(err.Error(), "base observation failed") {
		t.Fatalf("observation error = %v", err)
	}
}

func TestSourceBuilderTargetIdentityV1MapsTheObservedBaseProfile(t *testing.T) {
	platform, err := blueprint.ParsePlatform("linux/amd64")
	if err != nil {
		t.Fatal(err)
	}
	observed := APTBaseValidation{Profile: aptprovider.BaseProfileEvidenceV1{
		OSRelease:          []aptprovider.OSReleaseFieldV1{{Name: "ID", Value: "debian"}, {Name: "ID_LIKE", Value: ""}, {Name: "VERSION_ID", Value: "12"}},
		NativeArchitecture: "amd64",
	}}
	target, err := sourceBuilderTargetIdentityV1(platform, observed)
	if err != nil || target != sourceBuilderJavaTarget("debian", "12") {
		t.Fatalf("target = %#v, err = %v", target, err)
	}
	observed.Profile.OSRelease = observed.Profile.OSRelease[:1]
	if _, err := sourceBuilderTargetIdentityV1(platform, observed); err == nil || !strings.Contains(err.Error(), "VERSION_ID") {
		t.Fatalf("missing version error = %v", err)
	}
}

func TestSourceBuilderDomainOwnerV1SelectsTheFirstPythonNode(t *testing.T) {
	plan := providers.ProviderPlanV1{Nodes: []providers.NodeSpec{
		{ID: "base", Provider: blueprint.ComponentTypeBase},
		{ID: "python/zeta", Provider: blueprint.ComponentTypePython},
		{ID: "apt", Provider: blueprint.ComponentTypeAPT},
		{ID: "python/alpha", Provider: blueprint.ComponentTypePython},
	}}
	owner, err := sourceBuilderDomainOwnerV1(plan)
	if err != nil || owner != "python/alpha" {
		t.Fatalf("owner = %q, err = %v", owner, err)
	}
	if _, err := sourceBuilderDomainOwnerV1(providers.ProviderPlanV1{Nodes: plan.Nodes[:1]}); err == nil {
		t.Fatal("plan without a Python node selected an owner")
	}
}

func TestObserveSourceBuilderTargetIdentityV1ProbesTheBaseThenClosesAndCleansUp(t *testing.T) {
	store, err := providerstore.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	base := testProbeImageDescriptor(t, "linux/amd64")
	order := []string{}
	previousProbe, previousAPT, previousOpen, previousProfile := prepareSourceBuilderProbeWorkspaceV1, prepareSourceBuilderAPTWorkspaceV1, openSourceBuilderValidationSessionV1, probeSourceBuilderBaseProfileV1
	t.Cleanup(func() {
		prepareSourceBuilderProbeWorkspaceV1, prepareSourceBuilderAPTWorkspaceV1, openSourceBuilderValidationSessionV1, probeSourceBuilderBaseProfileV1 = previousProbe, previousAPT, previousOpen, previousProfile
	})
	workspace := testPreparedProbeWorkspace(t, base.Platform, t.TempDir())
	prepareSourceBuilderProbeWorkspaceV1 = func(_ context.Context, _ providerstore.Store, platform blueprint.Platform) (PreparedProbeWorkspace, func() error, error) {
		order = append(order, "probe-workspace")
		if platform != base.Platform {
			t.Fatalf("probe workspace platform = %s", platform.Canonical)
		}
		return workspace, func() error { order = append(order, "cleanup-probe-workspace"); return nil }, nil
	}
	aptWorkspace := PreparedAPTResolverWorkspace{HostDir: t.TempDir(), ContainerDir: "/apt"}
	prepareSourceBuilderAPTWorkspaceV1 = func(providerstore.Store) (PreparedAPTResolverWorkspace, func(), error) {
		order = append(order, "apt-workspace")
		return aptWorkspace, func() { order = append(order, "cleanup-apt-workspace") }, nil
	}
	session := &ImageValidationSession{descriptor: base, containerName: "observe-test", runDocker: func(spec CommandSpec, _ RunOptions) error {
		order = append(order, "docker:"+spec.Args[0])
		return nil
	}}
	openSourceBuilderValidationSessionV1 = func(_ context.Context, got deploy.ImageDescriptor, gotWorkspace PreparedProbeWorkspace, gotAPT PreparedAPTResolverWorkspace) (*ImageValidationSession, error) {
		order = append(order, "open")
		if !reflect.DeepEqual(got, base) || !reflect.DeepEqual(gotWorkspace, workspace) || !reflect.DeepEqual(gotAPT, aptWorkspace) {
			t.Fatalf("session opened with %#v / %#v / %#v", got, gotWorkspace, gotAPT)
		}
		return session, nil
	}
	probeSourceBuilderBaseProfileV1 = func(_ context.Context, got *ImageValidationSession) (APTBaseValidation, error) {
		order = append(order, "probe")
		if got != session {
			t.Fatal("probed a different session")
		}
		return APTBaseValidation{Profile: aptprovider.BaseProfileEvidenceV1{
			OSRelease:          []aptprovider.OSReleaseFieldV1{{Name: "ID", Value: "ubuntu"}, {Name: "VERSION_ID", Value: "26.04"}},
			NativeArchitecture: "amd64",
		}}, nil
	}
	target, err := ObserveSourceBuilderTargetIdentityV1(context.Background(), store, base)
	if err != nil || target != sourceBuilderJavaTarget("ubuntu", "26.04") {
		t.Fatalf("target = %#v, err = %v", target, err)
	}
	if !reflect.DeepEqual(order, []string{"probe-workspace", "apt-workspace", "open", "probe", "docker:rm", "cleanup-apt-workspace", "cleanup-probe-workspace"}) {
		t.Fatalf("order = %#v", order)
	}
	order = nil
	session.closed = false
	probeSourceBuilderBaseProfileV1 = func(context.Context, *ImageValidationSession) (APTBaseValidation, error) {
		order = append(order, "probe")
		return APTBaseValidation{}, errors.New("os-release unreadable")
	}
	if _, err := ObserveSourceBuilderTargetIdentityV1(context.Background(), store, base); err == nil || !strings.Contains(err.Error(), "os-release unreadable") {
		t.Fatalf("error = %v", err)
	}
	if !reflect.DeepEqual(order, []string{"probe-workspace", "apt-workspace", "open", "probe", "docker:rm", "cleanup-apt-workspace", "cleanup-probe-workspace"}) {
		t.Fatalf("failure order = %#v, want the container closed and workspaces removed", order)
	}
	if _, err := ObserveSourceBuilderTargetIdentityV1(context.Background(), store, deploy.ImageDescriptor{}); err == nil {
		t.Fatal("invalid base descriptor was observed")
	}
	order = nil
	session.closed = false
	probeSourceBuilderBaseProfileV1 = func(context.Context, *ImageValidationSession) (APTBaseValidation, error) {
		order = append(order, "probe")
		return APTBaseValidation{Profile: aptprovider.BaseProfileEvidenceV1{
			OSRelease:          []aptprovider.OSReleaseFieldV1{{Name: "ID", Value: "debian"}, {Name: "VERSION_ID", Value: "12"}},
			NativeArchitecture: "amd64",
		}}, nil
	}
	session.runDocker = func(spec CommandSpec, options RunOptions) error {
		order = append(order, "docker:"+spec.Args[0])
		if spec.Args[0] == "rm" {
			_, _ = options.Stderr.Write([]byte("daemon unavailable\n"))
			return errors.New("exit status 1")
		}
		return nil
	}
	_, err = ObserveSourceBuilderTargetIdentityV1(context.Background(), store, base)
	if err == nil || !providerHelperCleanupFailed(err) {
		t.Fatalf("container removal failure = %v, want a helper cleanup failure", err)
	}
	if !reflect.DeepEqual(order, []string{"probe-workspace", "apt-workspace", "open", "probe", "docker:rm"}) {
		t.Fatalf("cleanup-failure order = %#v, want both workspaces retained for the abandoned container", order)
	}
}

func TestSourceBuilderPlanRecipeForHandlesAbsentPlans(t *testing.T) {
	var plan *SourceBuilderPortableToolPlanV1
	if _, found := plan.RecipeFor("demo"); found {
		t.Fatal("nil plan reported a recipe")
	}
}
