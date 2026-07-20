package dockerdeploy

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers"
	"github.com/omry/reploy/internal/providers/registry"
	"github.com/omry/reploy/internal/providerstore"
)

func providerBuildPreparationFixture(t *testing.T) (
	LockedProviderBuildPreparationInputV1,
	LoadedBuildRequestV1,
	CurrentBuild,
	SelectedProviderBase,
	PreparedProviderBase,
) {
	t.Helper()
	dir, operation, store, lock, state := currentBuildFixture(t, true)
	t.Cleanup(func() { operation.Unlock() })
	document := blueprint.Document{
		Blueprint: blueprint.Metadata{Compatibility: blueprint.Compatibility{Platforms: []blueprint.Platform{state.Platform}}},
		Environment: blueprint.Environment{ID: "current-test", Components: map[string]blueprint.Component{
			"base": {
				Type: blueprint.ComponentTypeBase,
				Base: &blueprint.BaseComponent{Image: lock.Base.AuthorReference, Exports: map[string]blueprint.BaseExecutableExport{}},
			},
		}},
	}
	request, err := BuildResolvedRequestV1(document, state.Overlay, state.Platform, []providers.ResolvedSourceInput{})
	if err != nil {
		t.Fatal(err)
	}
	loaded := LoadedBuildRequestV1{State: state, Document: document, Request: request, Current: &lock}
	current := CurrentBuild{State: state, Generation: *state.Current, Lock: lock}
	plan, err := registry.Plan(providers.PlanInput{
		Components: request.Components, Platform: request.Platform,
	})
	if err != nil {
		t.Fatal(err)
	}
	selected := SelectedProviderBase{
		Plan: plan, Descriptor: lock.Base,
		Config: deploy.BaseConfig{
			Schema: deploy.BaseConfigSchemaV1, Environment: []deploy.ConfigEnvironmentVariable{},
			Entrypoint: []string{}, Command: []string{}, OnBuild: []string{}, Volumes: []string{},
		},
	}
	prepared := PreparedProviderBase{
		Plan: selected.Plan, Descriptor: selected.Descriptor, Config: selected.Config,
		Image:   providers.RealizedImageV1{Digest: lock.Base.ManifestDigest, ConfigDigest: lock.Base.ConfigDigest, RootFSSubject: lock.FinalImage.RootFSSubject},
		Catalog: []providers.RealizedOutput{},
	}
	input := LockedProviderBuildPreparationInputV1{
		Operation: operation, Store: store, Environment: "current-test", DeploymentDir: dir,
		Sources: []providers.ResolvedSourceInput{}, DockerPlan: DockerExecutionPlan{},
	}
	return input, loaded, current, selected, prepared
}

func TestPrepareLockedProviderBuildV1StopsBeforeBaseRealizationOnExactReuse(t *testing.T) {
	input, loaded, current, selected, _ := providerBuildPreparationFixture(t)
	order := []string{}
	backend := providerBuildPreparationTestBackend(t, loaded, current, selected, PreparedProviderBase{}, &order)
	backend.matches = func(got CurrentBuild, reuse CurrentBuildReuseInput) (bool, error) {
		order = append(order, "match")
		if !reflect.DeepEqual(got, current) || !reflect.DeepEqual(reuse.Base, selected.Descriptor) || !reflect.DeepEqual(reuse.ResolvedRequest, loaded.Request) {
			t.Fatal("reuse input changed")
		}
		return true, nil
	}
	backend.realizeBase = func(context.Context, providerstore.Store, SelectedProviderBase) (PreparedProviderBase, error) {
		t.Fatal("exact reuse realized base outputs")
		return PreparedProviderBase{}, nil
	}

	result, err := prepareLockedProviderBuildV1(context.Background(), input, backend)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Reused || result.PreparedBase != nil || result.Current == nil || result.ReusableLock == nil {
		t.Fatalf("result = %#v", result)
	}
	if result.Operation != input.Operation || result.Store.Root() != input.Store.Root() || result.Environment != input.Environment || result.DeploymentDir != input.DeploymentDir || !reflect.DeepEqual(result.DockerPlan, input.DockerPlan) {
		t.Fatal("preparation did not bind its operation inputs")
	}
	if !reflect.DeepEqual(order, []string{"recover", "load", "select", "current", "locked-sources", "match"}) {
		t.Fatalf("order = %#v", order)
	}
}

func TestPrepareLockedProviderBuildV1RealizesBaseAfterStaleReuse(t *testing.T) {
	input, loaded, current, selected, prepared := providerBuildPreparationFixture(t)
	order := []string{}
	backend := providerBuildPreparationTestBackend(t, loaded, current, selected, prepared, &order)
	backend.matches = func(CurrentBuild, CurrentBuildReuseInput) (bool, error) {
		order = append(order, "match")
		return false, nil
	}

	result, err := prepareLockedProviderBuildV1(context.Background(), input, backend)
	if err != nil {
		t.Fatal(err)
	}
	if result.Reused || result.PreparedBase == nil || !reflect.DeepEqual(*result.PreparedBase, prepared) || result.ReusableLock == nil {
		t.Fatalf("result = %#v", result)
	}
	if result.FinalImageConfig.User != "0:0" || !reflect.DeepEqual(order, []string{"recover", "load", "select", "current", "locked-sources", "match", "realize"}) {
		t.Fatalf("config/order = %#v/%#v", result.FinalImageConfig, order)
	}
}

func TestPrepareLockedProviderBuildV1NoCacheSkipsAllCurrentLockReuse(t *testing.T) {
	input, loaded, current, selected, prepared := providerBuildPreparationFixture(t)
	input.NoCache = true
	order := []string{}
	backend := providerBuildPreparationTestBackend(t, loaded, current, selected, prepared, &order)
	backend.matches = func(CurrentBuild, CurrentBuildReuseInput) (bool, error) {
		t.Fatal("no-cache evaluated exact reuse")
		return false, nil
	}

	result, err := prepareLockedProviderBuildV1(context.Background(), input, backend)
	if err != nil {
		t.Fatal(err)
	}
	if result.Reused || result.PreparedBase == nil || result.Current == nil || result.ReusableLock != nil {
		t.Fatalf("result = %#v", result)
	}
	if !reflect.DeepEqual(order, []string{"recover", "load", "select", "current", "realize"}) {
		t.Fatalf("order = %#v", order)
	}
}

func TestPrepareLockedProviderBuildV1RealizesBaseForUnbuiltState(t *testing.T) {
	input, loaded, current, selected, prepared := providerBuildPreparationFixture(t)
	order := []string{}
	backend := providerBuildPreparationTestBackend(t, loaded, current, selected, prepared, &order)
	backend.validateCurrent = func(context.Context, *deploy.OperationLock, providerstore.Store, string, string) (CurrentBuild, bool, error) {
		order = append(order, "current")
		return CurrentBuild{}, false, nil
	}
	backend.matches = func(CurrentBuild, CurrentBuildReuseInput) (bool, error) {
		t.Fatal("unbuilt state evaluated exact reuse")
		return false, nil
	}

	result, err := prepareLockedProviderBuildV1(context.Background(), input, backend)
	if err != nil {
		t.Fatal(err)
	}
	if result.Reused || result.PreparedBase == nil || result.Current != nil || result.ReusableLock != nil {
		t.Fatalf("result = %#v", result)
	}
	if !reflect.DeepEqual(order, []string{"recover", "load", "select", "current", "realize"}) {
		t.Fatalf("order = %#v", order)
	}
}

func TestPrepareLockedProviderBuildV1StopsWhenRecoveryFails(t *testing.T) {
	input, loaded, current, selected, prepared := providerBuildPreparationFixture(t)
	want := errors.New("recovery failed")
	backend := providerBuildPreparationTestBackend(t, loaded, current, selected, prepared, &[]string{})
	backend.recover = func(context.Context, *deploy.OperationLock, providerstore.Store, *deploy.EnvironmentGenerationState, string, string, providers.RequirementProfileOwnerValidator, providers.ResolvedBundleOwnerValidator) (bool, error) {
		return false, want
	}
	backend.load = func(*deploy.OperationLock, []providers.ResolvedSourceInput) (LoadedBuildRequestV1, error) {
		t.Fatal("failed recovery loaded build inputs")
		return LoadedBuildRequestV1{}, nil
	}
	_, err := prepareLockedProviderBuildV1(context.Background(), input, backend)
	if !errors.Is(err, want) {
		t.Fatalf("error = %v", err)
	}
}

func TestPrepareLockedProviderBuildV1RejectsInstalledDeploymentBeforeBackendWork(t *testing.T) {
	input, loaded, current, selected, prepared := providerBuildPreparationFixture(t)
	_, _, err := input.Operation.SetInstallationStateV1(deploy.InstallationStateV1{
		Schema: deploy.InstallationSchemaV1, Status: deploy.InstallationStatusReady,
		TargetDir: input.DeploymentDir, Scope: "system", Service: "demo",
		UnitPath: "/etc/systemd/system/demo.service", InstanceID: "demo-1", ComposeProject: "demo-1",
		ContainerName: "demo", NetworkName: "demo", Ports: []deploy.InstallationPortBindingV1{},
	})
	if err != nil {
		t.Fatal(err)
	}
	order := []string{}
	backend := providerBuildPreparationTestBackend(t, loaded, current, selected, prepared, &order)

	_, err = prepareLockedProviderBuildV1(t.Context(), input, backend)
	if err == nil || !strings.Contains(err.Error(), "installed deployment") || !strings.Contains(err.Error(), "staged deployment") {
		t.Fatalf("installed source error = %v", err)
	}
	if len(order) != 0 {
		t.Fatalf("backend work ran for installed source: %v", order)
	}
}

func providerBuildPreparationTestBackend(
	t *testing.T,
	loaded LoadedBuildRequestV1,
	current CurrentBuild,
	selected SelectedProviderBase,
	prepared PreparedProviderBase,
	order *[]string,
) providerBuildPreparationBackend {
	t.Helper()
	return providerBuildPreparationBackend{
		recover: func(_ context.Context, _ *deploy.OperationLock, _ providerstore.Store, generation *deploy.EnvironmentGenerationState, _ string, _ string, validateProfile providers.RequirementProfileOwnerValidator, validateBundle providers.ResolvedBundleOwnerValidator) (bool, error) {
			*order = append(*order, "recover")
			if generation == nil || validateProfile == nil || validateBundle == nil {
				t.Fatal("recovery did not receive current generation and validators")
			}
			return true, nil
		},
		load: func(_ *deploy.OperationLock, sources []providers.ResolvedSourceInput) (LoadedBuildRequestV1, error) {
			*order = append(*order, "load")
			if sources == nil {
				t.Fatal("sources became nil")
			}
			return loaded, nil
		},
		selectBase: func(_ context.Context, request providers.ResolvedRequestV1) (SelectedProviderBase, error) {
			*order = append(*order, "select")
			if !reflect.DeepEqual(request, loaded.Request) {
				t.Fatal("selected base for different request")
			}
			return selected, nil
		},
		validateCurrent: func(context.Context, *deploy.OperationLock, providerstore.Store, string, string) (CurrentBuild, bool, error) {
			*order = append(*order, "current")
			return current, true, nil
		},
		lockedSources: func(lock deploy.BuildLockV1) ([]providers.ResolvedSourceInput, error) {
			*order = append(*order, "locked-sources")
			if !reflect.DeepEqual(lock, current.Lock) {
				t.Fatal("selected sources loaded from a different lock")
			}
			return []providers.ResolvedSourceInput{}, nil
		},
		matches: func(CurrentBuild, CurrentBuildReuseInput) (bool, error) {
			return false, nil
		},
		realizeBase: func(_ context.Context, _ providerstore.Store, got SelectedProviderBase) (PreparedProviderBase, error) {
			*order = append(*order, "realize")
			if !reflect.DeepEqual(got, selected) {
				t.Fatal("realized different selected base")
			}
			return prepared, nil
		},
	}
}
