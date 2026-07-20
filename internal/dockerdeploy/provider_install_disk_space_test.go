package dockerdeploy

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPreflightProviderInstallDiskSpaceGroupsRequirementsByFilesystem(t *testing.T) {
	root := t.TempDir()
	opt := filepath.Join(root, "opt")
	etc := filepath.Join(root, "etc")
	for _, directory := range []string{opt, etc} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	lookups := 0
	err := preflightProviderInstallDiskSpaceWithV1([]providerInstallDiskRequirementV1{
		{Path: filepath.Join(opt, "deployment", "state.json"), Bytes: 40},
		{Path: filepath.Join(opt, "deployment", "compose.yaml"), Bytes: 60},
		{Path: filepath.Join(etc, "systemd", "demo.service"), Bytes: 20},
	}, func(path string) (providerInstallFilesystemSpaceV1, error) {
		lookups++
		if path == opt {
			return providerInstallFilesystemSpaceV1{Key: "opt", Available: 100}, nil
		}
		if path == etc {
			return providerInstallFilesystemSpaceV1{Key: "etc", Available: 20}, nil
		}
		t.Fatalf("lookup path = %q", path)
		return providerInstallFilesystemSpaceV1{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if lookups != 3 {
		t.Fatalf("filesystem lookups = %d, want 3", lookups)
	}
}

func TestPreflightProviderInstallDiskSpaceRejectsCombinedShortfall(t *testing.T) {
	root := t.TempDir()
	err := preflightProviderInstallDiskSpaceWithV1([]providerInstallDiskRequirementV1{
		{Path: filepath.Join(root, "a"), Bytes: 60},
		{Path: filepath.Join(root, "b"), Bytes: 41},
	}, func(string) (providerInstallFilesystemSpaceV1, error) {
		return providerInstallFilesystemSpaceV1{Key: "same", Available: 100}, nil
	})
	if err == nil || !strings.Contains(err.Error(), "need 101 bytes, have 100 bytes") {
		t.Fatalf("error = %v", err)
	}
}

func TestPreflightProviderInstallDiskSpaceRejectsInvalidInputAndOverflow(t *testing.T) {
	root := t.TempDir()
	lookup := func(string) (providerInstallFilesystemSpaceV1, error) {
		return providerInstallFilesystemSpaceV1{Key: "same", Available: math.MaxUint64}, nil
	}
	if err := preflightProviderInstallDiskSpaceWithV1(nil, lookup); err == nil {
		t.Fatal("expected nil requirement rejection")
	}
	if err := preflightProviderInstallDiskSpaceWithV1([]providerInstallDiskRequirementV1{{Path: "relative", Bytes: 1}}, lookup); err == nil {
		t.Fatal("expected relative path rejection")
	}
	err := preflightProviderInstallDiskSpaceWithV1([]providerInstallDiskRequirementV1{
		{Path: filepath.Join(root, "a"), Bytes: math.MaxUint64},
		{Path: filepath.Join(root, "b"), Bytes: 1},
	}, lookup)
	if err == nil || !strings.Contains(err.Error(), "overflow") {
		t.Fatalf("error = %v", err)
	}
}
