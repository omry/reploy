//go:build linux

package providerstore

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestPrivilegedStorePreservesOperatorAccess(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires root in a disposable user namespace")
	}
	for _, outcome := range []string{"success", "failure", "cancelled"} {
		t.Run(outcome, func(t *testing.T) {
			base, err := os.MkdirTemp("", "reploy-hf-store-")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.RemoveAll(base) })
			if err := os.Chmod(base, 0o755); err != nil {
				t.Fatal(err)
			}
			source := filepath.Join(base, "source")
			if err := os.Mkdir(source, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Chown(source, 1000, 1000); err != nil {
				t.Fatal(err)
			}
			store, err := NewStore(source)
			if err != nil {
				t.Fatal(err)
			}
			store = store.WithWorkspaceOwnerRestoration()
			var workspace, blob string
			var descriptor ArtifactDescriptor
			func() {
				defer func() {
					if err := store.RestoreWorkspaceOwners(); err != nil {
						t.Error(err)
					}
				}()
				workspace, err = store.NewWorkspace("retained-*")
				if err != nil {
					t.Fatal(err)
				}
				nested := filepath.Join(workspace, "nested")
				if err := os.Mkdir(nested, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(nested, "result"), []byte(outcome), 0o600); err != nil {
					t.Fatal(err)
				}
				descriptor, err = store.Publish(t.Context(), "demo.deb", "deb", strings.NewReader("payload"))
				if err != nil {
					t.Fatal(err)
				}
				if err := store.recordAcquisitionFailure("root-test", descriptor, ArtifactSource{ID: "test", SHA256: descriptor.SHA256}, acquisitionObservation{AcquisitionAttempt: AcquisitionAttempt{Outcome: "transport-failure", Attempt: 1}}, 1); err != nil {
					t.Fatal(err)
				}
				blob, err = store.BlobPath(descriptor.SHA256)
				if err != nil {
					t.Fatal(err)
				}
				if outcome == "cancelled" {
					ctx, cancel := context.WithCancel(t.Context())
					cancel()
					if _, err := store.Publish(ctx, "cancel.whl", "wheel", strings.NewReader("partial")); !errors.Is(err, context.Canceled) {
						t.Errorf("cancelled publish: %v", err)
					}
				}
			}()
			for path, mode := range map[string]os.FileMode{
				filepath.Join(source, ".reploy"):             0o755,
				store.Root():                                 0o755,
				filepath.Join(store.Root(), "tmp"):           0o755,
				workspace:                                    0o700,
				filepath.Join(workspace, "nested"):           0o700,
				filepath.Join(workspace, "nested", "result"): 0o600,
				blob: 0o444,
			} {
				info, err := os.Lstat(path)
				if err != nil {
					t.Fatal(err)
				}
				stat := info.Sys().(*syscall.Stat_t)
				if stat.Uid != 1000 || stat.Gid != 1000 || info.Mode().Perm() != mode {
					t.Errorf("%s: %d:%d %o", path, stat.Uid, stat.Gid, info.Mode().Perm())
				}
			}
			if err := filepath.WalkDir(store.Root(), func(path string, entry os.DirEntry, err error) error {
				if err != nil {
					return err
				}
				info, err := entry.Info()
				if err != nil {
					return err
				}
				stat := info.Sys().(*syscall.Stat_t)
				if stat.Uid != 1000 || stat.Gid != 1000 {
					t.Errorf("cache or diagnostic owner %s: %d:%d", path, stat.Uid, stat.Gid)
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			self, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			content, err := os.ReadFile(self)
			if err != nil {
				t.Fatal(err)
			}
			binary := filepath.Join(base, "operator.test")
			if err := os.WriteFile(binary, content, 0o755); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(binary, "-test.run=^TestStoreOperatorChild$", "-test.v")
			encoded, err := json.Marshal(descriptor)
			if err != nil {
				t.Fatal(err)
			}
			cmd.Env = append(os.Environ(), "REPLOY_STORE_TEST_WORKSPACE="+workspace, "REPLOY_STORE_TEST_BLOB="+blob, "REPLOY_STORE_TEST_SOURCE="+source, "REPLOY_STORE_TEST_DESCRIPTOR="+string(encoded))
			cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: 1000, Gid: 1000, Groups: []uint32{1000}}}
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Errorf("operator access: %v\n%s", err, output)
			}
		})
	}
}

func TestStoreOperatorChild(t *testing.T) {
	workspace := os.Getenv("REPLOY_STORE_TEST_WORKSPACE")
	if workspace == "" {
		t.Skip("subprocess helper")
	}
	if os.Geteuid() != 1000 {
		t.Fatal("must be the ordinary operator")
	}
	for _, path := range []string{filepath.Join(workspace, "nested", "result"), os.Getenv("REPLOY_STORE_TEST_BLOB")} {
		if _, err := os.ReadFile(path); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(workspace, "operator-result"), []byte("usable"), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(os.Getenv("REPLOY_STORE_TEST_SOURCE"))
	if err != nil {
		t.Fatal(err)
	}
	var descriptor ArtifactDescriptor
	if err := json.Unmarshal([]byte(os.Getenv("REPLOY_STORE_TEST_DESCRIPTOR")), &descriptor); err != nil {
		t.Fatal(err)
	}
	stamp, err := store.artifactVerificationPath(descriptor.SHA256)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(stamp); err != nil {
		t.Fatalf("operator cannot refresh verification cache: %v", err)
	}
	if err := store.VerifyCachedDeb(descriptor); err != nil {
		t.Fatal(err)
	}
	if err := store.recordAcquisitionFailure("operator-test", descriptor, ArtifactSource{ID: "test", SHA256: descriptor.SHA256}, acquisitionObservation{AcquisitionAttempt: AcquisitionAttempt{Outcome: "transport-failure", Attempt: 1}}, 1); err != nil {
		t.Fatalf("operator cannot record acquisition failure: %v", err)
	}
}

func TestPrivilegedStoreRestoresDeletedWorkspace(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires mapped root")
	}
	base, err := os.MkdirTemp("", "reploy-hf-deleted-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(base)
	if err := os.Chown(base, 1000, 1000); err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(base)
	if err != nil {
		t.Fatal(err)
	}
	store = store.WithWorkspaceOwnerRestoration()
	workspace, err := store.NewWorkspace("removed-*")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(workspace); err != nil {
		t.Fatal(err)
	}
	if err := store.RestoreWorkspaceOwners(); err != nil {
		t.Fatalf("removed workspace should require no transfer: %v", err)
	}
	if err := store.RestoreWorkspaceOwners(); err != nil {
		t.Fatal(err)
	}
}
