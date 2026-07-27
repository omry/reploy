package dockerdeploy

import (
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func TestProviderInstallFilesV1CombinesAndSortsSystemdCandidates(t *testing.T) {
	destinationDir := t.TempDir()
	references := fixedPublicationReferences(t, destinationDir, 0xc3)
	plan := providerInstallRunPlanFixture(destinationDir, references)

	files, err := providerInstallFilesV1(plan, "/usr/bin/docker", true)
	if err != nil {
		t.Fatal(err)
	}
	wantPaths := []string{
		plan.Installation.UnitPath,
		filepath.Join(destinationDir, plan.ControlScript),
		filepath.Join(destinationDir, filepath.FromSlash(embeddedRuntimeFileName())),
		filepath.Join(destinationDir, DockerEnvFileName),
		filepath.Join(destinationDir, ComposeFileName),
	}
	sort.Strings(wantPaths)
	if len(files) != len(wantPaths) {
		t.Fatalf("files = %#v", files)
	}
	for index, want := range wantPaths {
		if files[index].Path != want {
			t.Fatalf("file %d path = %q, want %q", index, files[index].Path, want)
		}
	}
}

func TestProviderInstallFilesV1OmitsSystemdForManagedBackend(t *testing.T) {
	destinationDir := t.TempDir()
	references := fixedPublicationReferences(t, destinationDir, 0xc4)
	plan := providerInstallRunPlanFixture(destinationDir, references)
	plan.Backend = installBackendDockerManaged
	plan.Installation.Scope = "user"
	plan.Installation.UnitPath = ""

	files, err := providerInstallFilesV1(plan, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 4 {
		t.Fatalf("files = %#v", files)
	}
}

func TestProviderInstallDockerFilesV1RendersExactSortedCandidates(t *testing.T) {
	destinationDir := t.TempDir()
	references := fixedPublicationReferences(t, destinationDir, 0xa1)
	plan := providerInstallRunPlanFixture(destinationDir, references)
	plan.Rendered.Compose = []byte("name: demo\nservices: {}\n")
	plan.Rendered.Environment = map[string]string{
		"REPLOY_SCOPE": "system",
		"REPLOY_IMAGE": references.Generation,
	}

	files, err := providerInstallDockerFilesV1(plan)
	if err != nil {
		t.Fatal(err)
	}
	want := []providerInstallFileCandidateV1{
		{
			Path:    filepath.Join(destinationDir, DockerEnvFileName),
			Content: []byte("# Private Reploy runtime inputs.\nREPLOY_IMAGE=" + references.Generation + "\nREPLOY_SCOPE=system\n"),
			Mode:    0o644,
		},
		{
			Path:    filepath.Join(destinationDir, ComposeFileName),
			Content: []byte("name: demo\nservices: {}\n"),
			Mode:    0o644,
		},
	}
	if !reflect.DeepEqual(files, want) {
		t.Fatalf("install files = %#v, want %#v", files, want)
	}

	files[1].Content[0] = 'X'
	if plan.Rendered.Compose[0] != 'n' {
		t.Fatal("file candidate aliases rendered Compose content")
	}
}

func TestProviderInstallDockerFilesV1RejectsUnrepresentableEnvironment(t *testing.T) {
	destinationDir := t.TempDir()
	references := fixedPublicationReferences(t, destinationDir, 0xa2)
	plan := providerInstallRunPlanFixture(destinationDir, references)
	plan.Rendered.Environment["REPLOY_IMAGE"] = "valid\nINJECTED=value"

	_, err := providerInstallDockerFilesV1(plan)
	if err == nil || !strings.Contains(err.Error(), "cannot be represented") {
		t.Fatalf("unrepresentable environment error = %v", err)
	}
}

func TestRenderProviderInstallEnvironmentV1RejectsInvalidName(t *testing.T) {
	_, err := renderProviderInstallEnvironmentV1(map[string]string{"lowercase": "value"})
	if err == nil || !strings.Contains(err.Error(), "name") {
		t.Fatalf("invalid environment name error = %v", err)
	}
}
