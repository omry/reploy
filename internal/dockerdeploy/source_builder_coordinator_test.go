package dockerdeploy

import (
	"context"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providerstore"
)

func stubSourceBuilderCoordinator(t *testing.T, planned *[][]SourceBuilderSelectedRecipeV1) {
	t.Helper()
	previousPlan := planSelectedSourceBuilderPortableToolsV1
	previousMaterialize := materializeSelectedSourceBuilderPortableToolsV1
	previousPrepare := prepareSelectedSourceBuilderEnvironmentV1
	t.Cleanup(func() {
		planSelectedSourceBuilderPortableToolsV1 = previousPlan
		materializeSelectedSourceBuilderPortableToolsV1 = previousMaterialize
		prepareSelectedSourceBuilderEnvironmentV1 = previousPrepare
	})
	planSelectedSourceBuilderPortableToolsV1 = func(_ context.Context, input PlanSourceBuilderPortableToolsInputV1) (*SourceBuilderPortableToolPlanV1, error) {
		selection := append([]SourceBuilderSelectedRecipeV1{}, input.SelectedRecipes...)
		*planned = append(*planned, selection)
		return &SourceBuilderPortableToolPlanV1{Recipes: map[string]SourceBuilderRecipeIdentityV1{}}, nil
	}
	materializeSelectedSourceBuilderPortableToolsV1 = func(_ context.Context, store providerstore.Store, plan *SourceBuilderPortableToolPlanV1) (*SourceBuilderPortableToolsV1, error) {
		workspace, err := store.NewWorkspace("coordinator-test-*")
		if err != nil {
			return nil, err
		}
		return &SourceBuilderPortableToolsV1{
			Plan: plan, workspace: workspace,
			Lock: providers.PortableToolLockV1{Schema: providers.PortableToolLockSchemaV1},
		}, nil
	}
	prepareSelectedSourceBuilderEnvironmentV1 = func(_ context.Context, _ providerstore.Store, tools *SourceBuilderPortableToolsV1, upstream deploy.ImageDescriptor, _ RunOptions) (*SourceBuilderEnvironmentV1, error) {
		return &SourceBuilderEnvironmentV1{Upstream: upstream, Recipes: tools.Plan.Recipes, removed: true}, nil
	}
}

func TestSourceBuilderCoordinatorPlansOnlySelectedImmutableRecipesAndAccumulatesTheirUnion(t *testing.T) {
	store, err := providerstore.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	planned := [][]SourceBuilderSelectedRecipeV1{}
	stubSourceBuilderCoordinator(t, &planned)
	coordinator := NewSourceBuilderCoordinatorV1(SourceBuilderCoordinatorInputV1{Store: store})
	alphaDir := writeSourceBuilderTestRecipe(t, "alpha", "[tool:java==21]")
	betaDir := writeSourceBuilderTestRecipe(t, "beta", "[tool:java==21]")
	alpha, err := ReadPythonLocalSourceRecipeV1(alphaDir, "alpha")
	if err != nil {
		t.Fatal(err)
	}
	beta, err := ReadPythonLocalSourceRecipeV1(betaDir, "beta")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := coordinator.Prepare(t.Context(), deploy.ImageDescriptor{}, []SourceBuilderSelectedRecipeV1{{Distribution: "beta", Recipe: beta}}); err != nil {
		t.Fatal(err)
	}
	if _, err := coordinator.Prepare(t.Context(), deploy.ImageDescriptor{}, []SourceBuilderSelectedRecipeV1{{Distribution: "alpha", Recipe: alpha}}); err != nil {
		t.Fatal(err)
	}
	if len(planned) != 2 || len(planned[0]) != 1 || planned[0][0].Distribution != "beta" ||
		len(planned[1]) != 2 || planned[1][0].Distribution != "alpha" || planned[1][1].Distribution != "beta" {
		t.Fatalf("planned selected recipes = %#v", planned)
	}
	workspaces := []string{coordinator.tools[0].workspace, coordinator.tools[1].workspace}
	if coordinator.PortableToolLock() == nil {
		t.Fatal("coordinator did not retain the selected portable-tool lock")
	}
	if err := coordinator.Cleanup(); err != nil {
		t.Fatal(err)
	}
	for _, workspace := range workspaces {
		if _, err := os.Stat(workspace); !os.IsNotExist(err) {
			t.Fatalf("coordinator workspace remained at %s: %v", workspace, err)
		}
	}
}

func TestSourceBuilderCoordinatorRejectsAChangedRecipeIdentityUnconditionally(t *testing.T) {
	store, err := providerstore.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	planned := [][]SourceBuilderSelectedRecipeV1{}
	stubSourceBuilderCoordinator(t, &planned)
	coordinator := NewSourceBuilderCoordinatorV1(SourceBuilderCoordinatorInputV1{Store: store})
	requiredDir := writeSourceBuilderTestRecipe(t, "demo", "[tool:java==21]")
	plainDir := writeSourceBuilderTestRecipe(t, "demo", "[]")
	required, err := ReadPythonLocalSourceRecipeV1(requiredDir, "demo")
	if err != nil {
		t.Fatal(err)
	}
	plain, err := ReadPythonLocalSourceRecipeV1(plainDir, "demo")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := coordinator.Prepare(t.Context(), deploy.ImageDescriptor{}, []SourceBuilderSelectedRecipeV1{{Distribution: "demo", Recipe: required}}); err != nil {
		t.Fatal(err)
	}
	_, err = coordinator.Prepare(t.Context(), deploy.ImageDescriptor{}, []SourceBuilderSelectedRecipeV1{{Distribution: "demo", Recipe: plain}})
	if err == nil || !strings.Contains(err.Error(), "differs across provider nodes") {
		t.Fatalf("changed recipe error = %v", err)
	}
	if len(planned) != 1 || !reflect.DeepEqual(planned[0][0].Recipe, required) {
		t.Fatalf("changed recipe reached planning: %#v", planned)
	}
}
