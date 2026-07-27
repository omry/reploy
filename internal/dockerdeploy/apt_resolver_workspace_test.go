package dockerdeploy

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	aptprovider "github.com/omry/reploy/internal/providers/apt"
	"github.com/omry/reploy/internal/providerstore"
)

func TestPrepareAPTResolverWorkspaceUsesDeploymentStoreAndCleansUp(t *testing.T) {
	deployment := t.TempDir()
	store, err := providerstore.NewStore(deployment)
	if err != nil {
		t.Fatal(err)
	}
	prepared, cleanup, err := PrepareAPTResolverWorkspace(store)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(prepared.HostDir, filepath.Join(deployment, ".reploy", providerstore.StoreDirName, "tmp")+string(os.PathSeparator)) {
		t.Fatalf("workspace = %s", prepared.HostDir)
	}
	if prepared.ContainerDir != aptprovider.ResolverScratchDirectory {
		t.Fatalf("container directory = %s", prepared.ContainerDir)
	}
	for _, relative := range []string{"lists", "lists/partial", "archives", "archives/partial", "output"} {
		info, err := os.Stat(filepath.Join(prepared.HostDir, relative))
		if err != nil || !info.IsDir() || hasPOSIXPermissionBits() && info.Mode().Perm() != 0o700 {
			t.Fatalf("layout %s: info=%v err=%v", relative, info, err)
		}
	}
	config, err := os.ReadFile(filepath.Join(prepared.HostDir, "apt.conf"))
	if err != nil || string(config) != aptprovider.ResolveAdditiveConfigV1 {
		t.Fatalf("config = %q, err = %v", config, err)
	}
	if err := os.WriteFile(filepath.Join(prepared.HostDir, "output", "scratch"), []byte("temporary"), 0o600); err != nil {
		t.Fatal(err)
	}
	cleanup()
	if _, err := os.Lstat(prepared.HostDir); !os.IsNotExist(err) {
		t.Fatalf("workspace survived cleanup: %v", err)
	}
}

func TestSeedAPTResolverArchivesCopiesVerifiedStoreObjects(t *testing.T) {
	deployment := t.TempDir()
	store, err := providerstore.NewStore(deployment)
	if err != nil {
		t.Fatal(err)
	}
	artifact, err := store.Publish(context.Background(), "debs/hello_1.0_amd64.deb", "deb", strings.NewReader("verified deb bytes"))
	if err != nil {
		t.Fatal(err)
	}
	prepared, cleanup, err := PrepareAPTResolverWorkspace(store)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	prepared, err = SeedAPTResolverArchives(context.Background(), store, prepared, []providerstore.ArtifactDescriptor{artifact})
	if err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(prepared.HostDir, "archives", "hello_1.0_amd64.deb")
	if err := providerstore.VerifyArtifactFile(destination, artifact); err != nil {
		t.Fatal(err)
	}
	source, err := store.InspectArtifactPath(artifact)
	if err != nil {
		t.Fatal(err)
	}
	sourceInfo, err := os.Stat(source)
	if err != nil {
		t.Fatal(err)
	}
	destinationInfo, err := os.Stat(destination)
	if err != nil {
		t.Fatal(err)
	}
	if os.SameFile(sourceInfo, destinationInfo) {
		t.Fatal("APT archive seed shares the immutable store inode")
	}
	if err := validateAPTResolverDownloadWorkspace(prepared); err != nil {
		t.Fatal(err)
	}
}

func TestSeedAPTResolverArchivesRejectsAlteredStoreObjectWithoutPartialSeed(t *testing.T) {
	store, err := providerstore.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	artifact, err := store.Publish(context.Background(), "debs/demo.deb", "deb", strings.NewReader("expected"))
	if err != nil {
		t.Fatal(err)
	}
	path, err := store.InspectArtifactPath(artifact)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("corrupt!"), 0o444); err != nil {
		t.Fatal(err)
	}
	prepared, cleanup, err := PrepareAPTResolverWorkspace(store)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	if _, err := SeedAPTResolverArchives(context.Background(), store, prepared, []providerstore.ArtifactDescriptor{artifact}); err == nil || !strings.Contains(err.Error(), "digest") {
		t.Fatalf("err = %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(prepared.HostDir, "archives"))
	if err != nil || len(entries) != 1 || entries[0].Name() != "partial" {
		t.Fatalf("partial seed remained: entries=%#v err=%v", entries, err)
	}
}

func TestAPTResolverDownloadRejectsAlteredSeedCopy(t *testing.T) {
	store, err := providerstore.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	artifact, err := store.Publish(context.Background(), "debs/demo.deb", "deb", strings.NewReader("expected"))
	if err != nil {
		t.Fatal(err)
	}
	prepared, cleanup, err := PrepareAPTResolverWorkspace(store)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	prepared, err = SeedAPTResolverArchives(context.Background(), store, prepared, []providerstore.ArtifactDescriptor{artifact})
	if err != nil {
		t.Fatal(err)
	}
	seed := filepath.Join(prepared.HostDir, "archives", "demo.deb")
	if err := os.Chmod(seed, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(seed, []byte("corrupt!"), 0o444); err != nil {
		t.Fatal(err)
	}
	if err := validateAPTResolverDownloadWorkspace(prepared); err == nil || !strings.Contains(err.Error(), "digest") {
		t.Fatalf("err = %v", err)
	}
}

func TestAPTResolverWorkspaceRejectsNonemptyOutput(t *testing.T) {
	store, err := providerstore.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	prepared, cleanup, err := PrepareAPTResolverWorkspace(store)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	if err := os.WriteFile(filepath.Join(prepared.HostDir, "output", "unexpected"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	err = validatePreparedAPTResolverWorkspace(prepared)
	if err == nil || !strings.Contains(err.Error(), "initially empty") {
		t.Fatalf("err = %v", err)
	}
}

func TestAPTResolverWorkspaceRejectsAlteredConfig(t *testing.T) {
	store, err := providerstore.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	prepared, cleanup, err := PrepareAPTResolverWorkspace(store)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	if err := os.WriteFile(filepath.Join(prepared.HostDir, "apt.conf"), []byte("changed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	err = validatePreparedAPTResolverWorkspace(prepared)
	if err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("err = %v", err)
	}
}

func TestInventoryAPTResolverArchivesDistinguishesUnchangedSeedAndNewContent(t *testing.T) {
	store, err := providerstore.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	seed, err := store.Publish(context.Background(), "debs/old_1_all.deb", "deb", strings.NewReader("seed bytes"))
	if err != nil {
		t.Fatal(err)
	}
	prepared, cleanup, err := PrepareAPTResolverWorkspace(store)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	prepared, err = SeedAPTResolverArchives(context.Background(), store, prepared, []providerstore.ArtifactDescriptor{seed})
	if err != nil {
		t.Fatal(err)
	}
	newPath := filepath.Join(prepared.HostDir, "archives", "hello_2_amd64.deb")
	if err := os.WriteFile(newPath, []byte("new deb bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	inventory, err := InventoryAPTResolverArchives(context.Background(), prepared)
	if err != nil {
		t.Fatal(err)
	}
	if len(inventory) != 2 || inventory[0].Filename != "hello_2_amd64.deb" || inventory[0].UnchangedSeed || inventory[1].Filename != "old_1_all.deb" || !inventory[1].UnchangedSeed {
		t.Fatalf("inventory = %#v", inventory)
	}
	for _, item := range inventory {
		if err := providerstore.VerifyArtifactFile(item.HostPath, item.Artifact); err != nil {
			t.Fatal(err)
		}
	}
}

func TestInventoryAPTResolverArchivesRejectsPartialAndUnexpectedEntries(t *testing.T) {
	tests := []struct {
		name  string
		setup func(t *testing.T, prepared PreparedAPTResolverWorkspace)
		want  string
	}{
		{name: "partial", setup: func(t *testing.T, prepared PreparedAPTResolverWorkspace) {
			if err := os.WriteFile(filepath.Join(prepared.HostDir, "archives", "partial", "x"), []byte("x"), 0o600); err != nil {
				t.Fatal(err)
			}
		}, want: "partial output"},
		{name: "non-deb", setup: func(t *testing.T, prepared PreparedAPTResolverWorkspace) {
			if err := os.WriteFile(filepath.Join(prepared.HostDir, "archives", "notice.txt"), []byte("x"), 0o600); err != nil {
				t.Fatal(err)
			}
		}, want: "unexpected entry"},
		{name: "directory", setup: func(t *testing.T, prepared PreparedAPTResolverWorkspace) {
			if err := os.Mkdir(filepath.Join(prepared.HostDir, "archives", "fake.deb"), 0o700); err != nil {
				t.Fatal(err)
			}
		}, want: "real regular file"},
		{name: "symlink", setup: func(t *testing.T, prepared PreparedAPTResolverWorkspace) {
			if err := os.Symlink("/etc/passwd", filepath.Join(prepared.HostDir, "archives", "fake.deb")); err != nil {
				t.Fatal(err)
			}
		}, want: "real regular file"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store, err := providerstore.NewStore(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			prepared, cleanup, err := PrepareAPTResolverWorkspace(store)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(cleanup)
			test.setup(t, prepared)
			_, err = InventoryAPTResolverArchives(context.Background(), prepared)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("err = %v, want %q", err, test.want)
			}
		})
	}
}

func TestInventoryAPTResolverArchivesAllowsOnlyEmptyRegularAPTLock(t *testing.T) {
	store, err := providerstore.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	prepared, cleanup, err := PrepareAPTResolverWorkspace(store)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	lock := filepath.Join(prepared.HostDir, "archives", "lock")
	if err := os.WriteFile(lock, []byte{}, 0o640); err != nil {
		t.Fatal(err)
	}
	if inventory, err := InventoryAPTResolverArchives(context.Background(), prepared); err != nil || len(inventory) != 0 {
		t.Fatalf("inventory = %#v, err = %v", inventory, err)
	}
	if err := os.WriteFile(lock, []byte("unexpected"), 0o640); err != nil {
		t.Fatal(err)
	}
	if _, err := InventoryAPTResolverArchives(context.Background(), prepared); err == nil || !strings.Contains(err.Error(), "lock") {
		t.Fatalf("err = %v", err)
	}
}

func TestInventoryAPTResolverArchivesHonorsCancellation(t *testing.T) {
	store, err := providerstore.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	prepared, cleanup, err := PrepareAPTResolverWorkspace(store)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := InventoryAPTResolverArchives(ctx, prepared); err != context.Canceled {
		t.Fatalf("err = %v", err)
	}
}
