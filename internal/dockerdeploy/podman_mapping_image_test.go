package dockerdeploy

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers/registry"
)

func mappedImageInspectionFixtureV1(image string) map[string]any {
	return map[string]any{"Id": image, "Os": "linux", "Architecture": "amd64", "Config": map[string]any{},
		"RootFS": map[string]any{"Type": "layers", "Layers": []string{string(rendererDigest("6")), string(rendererDigest("e")), string(rendererDigest("f"))}}}
}

func TestMappedPodmanImageRetainsPortableCompanionV1(t *testing.T) {
	for _, missing := range []bool{false, true} {
		t.Run(map[bool]string{false: "complete", true: "missing companion"}[missing], func(t *testing.T) {
			f := newPortableVerificationFixtureV1(t, true)
			pairs, err := ProjectEnvironmentOwnedReferencesV1(f.current.Generation, f.current.Lock, "demo", f.dir)
			if err != nil || len(pairs) != 2 {
				t.Fatal("portable fixture", err)
			}
			requests := 0
			client := newPodmanApplicationClient(t, func(w http.ResponseWriter, r *http.Request) {
				requests++
				if r.Method != http.MethodGet {
					t.Error("image admission sent an effectful request")
				}
				selected := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v1.44/images/"), "/json")
				descriptor := f.final.Descriptor
				if selected == pairs[1].Reference || selected == string(pairs[1].Image.ConfigDigest) {
					if missing {
						w.WriteHeader(404)
						return
					}
					descriptor = f.portable.Descriptor
				}
				layers := []string{}
				for _, digest := range descriptor.RootFSDiffIDs {
					layers = append(layers, string(digest))
				}
				json.NewEncoder(w).Encode(map[string]any{"Id": descriptor.ConfigDigest, "Os": "linux", "Architecture": "amd64", "Config": map[string]any{}, "RootFS": map[string]any{"Layers": layers}})
			})
			p := &preparedMappedPodmanCreateV1{ops: deploy.IdentityMappingOperationsV1{Operation: f.operation}, state: f.current.State, endpoint: client.endpoint}
			image, err := p.verifyImage(t.Context(), client)
			if missing {
				if err == nil || image != "" || requests != 4 {
					t.Fatalf("missing companion admitted: %q requests=%d err=%v", image, requests, err)
				}
			} else if err != nil || image != string(f.current.Lock.FinalImage.ConfigDigest) || requests != 6 {
				t.Fatalf("portable binding failed: %q requests=%d err=%v", image, requests, err)
			}
		})
	}
}

func TestMappedPodmanImageRefreshRejectsChangesV1(t *testing.T) {
	dir, operation, _, lock, state := currentBuildFixture(t, true)
	defer operation.Unlock()
	changed := false
	client := newPodmanApplicationClient(t, func(w http.ResponseWriter, r *http.Request) {
		image := string(lock.FinalImage.ConfigDigest)
		if changed {
			image = string(rendererDigest("a"))
		}
		json.NewEncoder(w).Encode(mappedImageInspectionFixtureV1(image))
	})
	p := &preparedMappedPodmanCreateV1{ops: deploy.IdentityMappingOperationsV1{Operation: operation}, state: state, endpoint: client.endpoint}
	if _, err := p.verifyImage(t.Context(), client); err != nil {
		t.Fatal(err)
	}
	changed = true
	if _, err := p.verifyImage(t.Context(), client); err == nil {
		t.Fatal("changed engine image reused old admission")
	}
	changed = false
	// A later recorded generation must not reuse the old invocation object.
	lock.FinalImage.ConfigDigest = rendererDigest("a")
	digest, err := operation.PublishBuildLock(lock, registry.ValidateRequirementProfileV1)
	if err != nil {
		t.Fatal(err)
	}
	current := *state.Current
	current.BuildLockDigest = digest
	state.Current = &current
	if err := operation.CommitStateV1(p.state.Current, state); err != nil {
		t.Fatal(err)
	}
	if _, err := p.verifyImage(t.Context(), client); err == nil {
		t.Fatal("changed generation reused old invocation authority", dir)
	}
}

func TestMappedPodmanImageAdmissionV1(t *testing.T) {
	for _, fault := range []string{"", "missing", "server error", "malformed", "oversize", "config", "platform", "architecture", "rootfs", "missing config", "private environment", "tag race", "missing lock", "stale state", "released lock", "endpoint", "cancelled", "missing generation", "current removed", "invalid blueprint", "nil client", "nil context", "nil preparation", "nil operation", "wrong store"} {
		t.Run(fault, func(t *testing.T) {
			dir, operation, _, lock, state := currentBuildFixture(t, fault != "missing lock")
			t.Cleanup(func() { _ = operation.Unlock() })
			var requests []string
			client := newPodmanApplicationClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || !strings.HasPrefix(r.URL.Path, "/v1.44/images/") || !strings.HasSuffix(r.URL.Path, "/json") {
					t.Error("image admission made an unexpected or effectful request", r.Method, r.URL.Path)
				}
				selected := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v1.44/images/"), "/json")
				requests = append(requests, selected)
				object := mappedImageInspectionFixtureV1(string(lock.FinalImage.ConfigDigest))
				switch fault {
				case "missing":
					w.WriteHeader(404)
					return
				case "server error":
					w.WriteHeader(500)
					w.Write([]byte("private-response-value"))
					return
				case "malformed":
					w.Write([]byte("private-response-value"))
					return
				case "oversize":
					w.Write([]byte(strings.Repeat("x", int(imageHealthConfigLimit)+1)))
					return
				case "config":
					object["Id"] = string(rendererDigest("a"))
				case "platform":
					object["Os"] = "windows"
				case "architecture":
					object["Architecture"] = "arm64"
				case "rootfs":
					object["RootFS"] = map[string]any{"Layers": []string{string(rendererDigest("a"))}}
				case "missing config":
					delete(object, "Config")
				case "private environment":
					object["Config"] = map[string]any{"Env": []string{"private-response-value"}}
				case "tag race":
					if len(requests) == 3 {
						object["Id"] = string(rendererDigest("a"))
					}
				}
				json.NewEncoder(w).Encode(object)
			})
			client.privateErrors = true
			p := &preparedMappedPodmanCreateV1{ops: deploy.IdentityMappingOperationsV1{Operation: operation}, state: state, endpoint: client.endpoint}
			ctx := t.Context()
			switch fault {
			case "stale state":
				p.state.BlueprintSource = "stale selected state"
			case "released lock":
				if err := operation.Unlock(); err != nil {
					t.Fatal(err)
				}
			case "endpoint":
				p.endpoint = "unix:///another.sock"
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "missing generation":
				p.state.Current = nil
			case "current removed":
				state.Current = nil
				if err := operation.CommitStateV1(p.state.Current, state); err != nil {
					t.Fatal(err)
				}
			case "invalid blueprint":
				p.state.Blueprint = "invalid"
			case "nil client":
				client = nil
			case "nil context":
				ctx = nil
			case "nil preparation":
				p = nil
			case "nil operation":
				p.ops.Operation = nil
			case "wrong store":
				if runtime.GOOS == "windows" {
					t.Skip("Windows cannot rename the open operation-lock fixture; mapped Podman admission requires Linux")
				}
				// Metadata ownership cannot be replaced with a link to a foreign
				// tree even while the original descriptor remains locked.
				if err := os.Rename(filepath.Join(dir, ".reploy"), filepath.Join(dir, "saved")); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Join(dir, "saved"), filepath.Join(dir, ".reploy")); err != nil {
					t.Skip("fixture cannot create a directory symlink", err)
				}
			}
			image, err := p.verifyImage(ctx, client)
			if fault == "" {
				if err != nil || image != string(lock.FinalImage.ConfigDigest) || !slices.Equal(requests, []string{state.Current.Reference, image, state.Current.Reference}) {
					t.Fatalf("generation binding failed: image=%q requests=%v err=%v", image, requests, err)
				}
				return
			}
			if err == nil || image != "" || strings.Contains(err.Error(), "private-response-value") {
				t.Fatalf("unsafe image admission: image=%q err=%v", image, err)
			}
		})
	}
}
