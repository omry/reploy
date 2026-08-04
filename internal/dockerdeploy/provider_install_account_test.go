package dockerdeploy

import (
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers/registry"
	"github.com/omry/reploy/internal/providerstore"
)

func TestPrepareProviderInstallAccountChecksBulkDiskBeforeCreatingMissingAccount(t *testing.T) {
	missing := errors.New("unknown user")
	events := []string{}
	resolveCalls := 0
	input := providerInstallRunInputV1{
		DestinationDeploymentDir: t.TempDir(),
		Install:                  providerInstallOptionsV1{Scope: InstallScopeSystem},
	}
	got, err := prepareProviderInstallAccountWithV1(
		t.Context(),
		blueprint.SystemAccount{User: "service", Group: "service", OnMissing: "create"},
		providerstore.Store{},
		CurrentBuild{},
		input,
		providerInstallAccountBackendV1{
			resolve: func(values map[string]string) (resolvedInstallOwner, error) {
				resolveCalls++
				events = append(events, "resolve")
				if values[reployInstallOwnerEnv] != "service:service" || values[reployInstallOwnerOnMissing] != "create" {
					t.Fatalf("account values = %#v", values)
				}
				if resolveCalls == 1 {
					return resolvedInstallOwner{}, missing
				}
				return resolvedInstallOwner{Spec: "service:service", UID: 991, GID: 992, ContainerUser: "991:992"}, nil
			},
			creationReadiness: func(_ map[string]string, resolveErr error) (string, error) {
				events = append(events, "check-create")
				if !errors.Is(resolveErr, missing) {
					t.Fatalf("resolve error = %v", resolveErr)
				}
				return "service:service", nil
			},
			bulkDiskRequirements: func(providerstore.Store, CurrentBuild, string) ([]providerInstallDiskRequirementV1, error) {
				events = append(events, "measure-bulk")
				return []providerInstallDiskRequirementV1{{Path: input.DestinationDeploymentDir, Bytes: 123}}, nil
			},
			preflight: func(requirements []providerInstallDiskRequirementV1) error {
				events = append(events, "check-disk")
				if len(requirements) != 1 || requirements[0].Bytes != 123 {
					t.Fatalf("disk requirements = %#v", requirements)
				}
				return nil
			},
			create: func(map[string]string) error {
				events = append(events, "create")
				return nil
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if got.Install.SystemUser != "service" || got.Install.SystemGroup != "service" || got.Install.SystemUID != 991 || got.Install.SystemGID != 992 {
		t.Fatalf("resolved install account = %#v", got.Install)
	}
	wantEvents := []string{"resolve", "check-create", "measure-bulk", "check-disk", "create", "resolve"}
	if !reflect.DeepEqual(events, wantEvents) {
		t.Fatalf("events=%v want=%v", events, wantEvents)
	}
}

func TestInspectProviderInstallAccountReportsMissingCreateWithoutIDs(t *testing.T) {
	missing := errors.New("unknown user")
	inspection, err := inspectProviderInstallAccountWithV1(
		InstallScopeSystem,
		blueprint.SystemAccount{User: "service", Group: "service", OnMissing: "create"},
		providerInstallAccountInspectionBackendV1{
			resolve: func(map[string]string) (resolvedInstallOwner, error) {
				return resolvedInstallOwner{}, missing
			},
			creationReadiness: func(values map[string]string, resolveErr error) (string, error) {
				if !errors.Is(resolveErr, missing) || values[reployInstallOwnerOnMissing] != "create" {
					t.Fatalf("creation readiness input = %#v / %v", values, resolveErr)
				}
				return "service:service", nil
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if inspection.User != "service" || inspection.Group != "service" || !inspection.WillCreate || inspection.UID != nil || inspection.GID != nil {
		t.Fatalf("missing account inspection = %#v", inspection)
	}
}

func TestInspectProviderInstallAccountReportsExistingNumericIdentity(t *testing.T) {
	inspection, err := inspectProviderInstallAccountWithV1(
		InstallScopeSystem,
		blueprint.SystemAccount{User: "service", Group: "service", OnMissing: "create"},
		providerInstallAccountInspectionBackendV1{
			resolve: func(map[string]string) (resolvedInstallOwner, error) {
				return resolvedInstallOwner{UID: 991, GID: 992}, nil
			},
			creationReadiness: func(map[string]string, error) (string, error) {
				t.Fatal("existing account checked creation readiness")
				return "", nil
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if inspection.WillCreate || inspection.UID == nil || *inspection.UID != 991 || inspection.GID == nil || *inspection.GID != 992 {
		t.Fatalf("existing account inspection = %#v", inspection)
	}
}

func TestInspectProviderInstallAccountRejectsMissingFailPolicy(t *testing.T) {
	want := errors.New("unknown user")
	_, err := inspectProviderInstallAccountWithV1(
		InstallScopeSystem,
		blueprint.SystemAccount{User: "service", Group: "service", OnMissing: "fail"},
		providerInstallAccountInspectionBackendV1{
			resolve: func(map[string]string) (resolvedInstallOwner, error) {
				return resolvedInstallOwner{}, want
			},
			creationReadiness: func(map[string]string, error) (string, error) {
				return "", want
			},
		},
	)
	if !errors.Is(err, want) || !strings.Contains(err.Error(), "resolve system install account") {
		t.Fatalf("missing fail-policy error = %v", err)
	}
}

func TestPrepareProviderInstallAccountDoesNotCreateWhenBulkDiskPreflightFails(t *testing.T) {
	want := errors.New("insufficient disk space")
	created := false
	_, err := prepareProviderInstallAccountWithV1(
		t.Context(),
		blueprint.SystemAccount{User: "service", Group: "service", OnMissing: "create"},
		providerstore.Store{},
		CurrentBuild{},
		providerInstallRunInputV1{DestinationDeploymentDir: t.TempDir(), Install: providerInstallOptionsV1{Scope: InstallScopeSystem}},
		providerInstallAccountBackendV1{
			resolve: func(map[string]string) (resolvedInstallOwner, error) {
				return resolvedInstallOwner{}, errors.New("missing")
			},
			creationReadiness: func(map[string]string, error) (string, error) { return "service:service", nil },
			bulkDiskRequirements: func(providerstore.Store, CurrentBuild, string) ([]providerInstallDiskRequirementV1, error) {
				return []providerInstallDiskRequirementV1{}, nil
			},
			preflight: func([]providerInstallDiskRequirementV1) error { return want },
			create: func(map[string]string) error {
				created = true
				return nil
			},
		},
	)
	if !errors.Is(err, want) || created || !strings.Contains(err.Error(), "before creating system install account") {
		t.Fatalf("error=%v created=%v", err, created)
	}
}

func TestPrepareProviderInstallAccountReusesExistingAccountWithoutCreationPreflight(t *testing.T) {
	unexpected := func(string) {
		t.Fatal("existing account triggered account creation preparation")
	}
	input := providerInstallRunInputV1{
		DestinationDeploymentDir: t.TempDir(),
		Install:                  providerInstallOptionsV1{Scope: InstallScopeSystem},
	}
	got, err := prepareProviderInstallAccountWithV1(
		t.Context(),
		blueprint.SystemAccount{User: "service", Group: "service", OnMissing: "create"},
		providerstore.Store{},
		CurrentBuild{},
		input,
		providerInstallAccountBackendV1{
			resolve: func(map[string]string) (resolvedInstallOwner, error) {
				return resolvedInstallOwner{UID: 991, GID: 992}, nil
			},
			creationReadiness: func(map[string]string, error) (string, error) {
				unexpected("readiness")
				return "", nil
			},
			bulkDiskRequirements: func(providerstore.Store, CurrentBuild, string) ([]providerInstallDiskRequirementV1, error) {
				unexpected("measurement")
				return nil, nil
			},
			preflight: func([]providerInstallDiskRequirementV1) error {
				unexpected("preflight")
				return nil
			},
			create: func(map[string]string) error {
				unexpected("create")
				return nil
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if got.Install.SystemUID != 991 || got.Install.SystemGID != 992 {
		t.Fatalf("reused install account = %#v", got.Install)
	}
}

func TestPrepareProviderInstallAccountIgnoresSystemAccountForUserScope(t *testing.T) {
	input := providerInstallRunInputV1{
		Install: providerInstallOptionsV1{
			Scope: InstallScopeUser, SystemUser: "stale", SystemGroup: "stale", SystemUID: 991, SystemGID: 992,
		},
	}
	got, err := prepareProviderInstallAccountWithV1(t.Context(), blueprint.SystemAccount{}, providerstore.Store{}, CurrentBuild{}, input, providerInstallAccountBackendV1{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Install.SystemUser != "" || got.Install.SystemGroup != "" || got.Install.SystemUID != 0 || got.Install.SystemGID != 0 {
		t.Fatalf("user-scope system account fields = %#v", got.Install)
	}
}

func TestProviderInstallAccountBulkDiskRequirementsMeasuresLockedClosure(t *testing.T) {
	sourceDir := t.TempDir()
	destinationDir := t.TempDir()
	operation, sourceStore, source := installedBuildPublicationSourceFixtureAtDir(t, sourceDir)
	if err := operation.Unlock(); err != nil {
		t.Fatal(err)
	}
	_, wantBytes, err := deploy.InspectBuildLockStoreClosure(source.Lock, sourceStore, registry.ValidateRequirementProfileV1, registry.ValidateResolvedBundlePayloadV1)
	if err != nil {
		t.Fatal(err)
	}
	requirements, err := providerInstallAccountBulkDiskRequirementsV1(sourceStore, source, destinationDir)
	if err != nil {
		t.Fatal(err)
	}
	want := []providerInstallDiskRequirementV1{{Path: filepath.Join(destinationDir, ".reploy", providerstore.StoreDirName), Bytes: wantBytes}}
	if !reflect.DeepEqual(requirements, want) {
		t.Fatalf("requirements=%#v want=%#v", requirements, want)
	}
}
