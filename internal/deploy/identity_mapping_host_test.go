package deploy

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/omry/reploy/internal/identitymapping"
)

func mappingHostState(t *testing.T, dir string) (*OperationLock, StateV1) {
	t.Helper()
	lock, err := AcquireOperationLock(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lock.Unlock() })
	state := StateV1{Schema: StateSchemaV1, Blueprint: stateV1TestBlueprint(t), Platform: stateV1TestPlatform(t), Overlay: EmptyRequestOverlayV1(), BlueprintSource: "blueprint.yaml", Staging: &StagingStateV1{Schema: StagingStateSchemaV1}}
	if err := lock.CommitStateV1(nil, state); err != nil {
		t.Fatal(err)
	}
	return lock, state
}

func TestMappingHostCheckUsesExistingLockAndSelectedDeploymentWithoutWrites(t *testing.T) {
	for _, installed := range []bool{false, true} {
		t.Run(map[bool]string{false: "staged", true: "installed"}[installed], func(t *testing.T) {
			dir := t.TempDir()
			lock, state := mappingHostState(t, dir)
			if installed {
				installation := installationStateV1Fixture(dir)
				installation.Scope = "user"
				state.Staging = nil
				state.Deployment = &DeploymentStateV1{Schema: DeploymentStateSchemaV1, Installation: installation}
				if err := lock.CommitStateV1(nil, state); err != nil {
					t.Fatal(err)
				}
			}
			statePath := filepath.Join(dir, ".reploy", stateFilenameV1)
			before, err := os.ReadFile(statePath)
			if err != nil {
				t.Fatal(err)
			}
			info, err := os.Stat(statePath)
			if err != nil {
				t.Fatal(err)
			}
			calls := 0
			want := identitymapping.HostIdentity{Username: "operator", UID: 1000, GID: 1000}
			intervals := []identitymapping.Interval{{HostStart: 100000, Count: 20}}
			got, err := lock.checkIdentityMappingHostV1(t.Context(), intervals, intervals, func(ctx context.Context, selected string, u, g []identitymapping.Interval) (identitymapping.HostIdentity, error) {
				calls++
				if selected != dir || !reflect.DeepEqual(u, intervals) || !reflect.DeepEqual(g, intervals) {
					t.Fatal("selected authority changed")
				}
				wait, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
				defer cancel()
				other, err := AcquireExistingOperationLock(wait, dir)
				if other != nil || !errors.Is(err, context.DeadlineExceeded) {
					t.Fatal("existing operation lock not held through host check")
				}
				return want, nil
			})
			if err != nil || got != want || calls != 1 {
				t.Fatalf("identity=%+v err=%v calls=%d", got, err, calls)
			}
			after, err := os.ReadFile(statePath)
			if err != nil {
				t.Fatal(err)
			}
			afterInfo, err := os.Stat(statePath)
			if err != nil {
				t.Fatal(err)
			}
			if string(before) != string(after) || info.Mode() != afterInfo.Mode() || !info.ModTime().Equal(afterInfo.ModTime()) {
				t.Fatal("read-only preflight rewrote deployment state")
			}
		})
	}
}

func TestMappingHostCheckRejectsMissingTerminalSystemAndCopiedStateBeforeHostQueries(t *testing.T) {
	for _, name := range []string{"no state", "no owner", "terminal", "system", "copied installation", "malformed state", "released lock", "nil context"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			lock, state := mappingHostState(t, dir)
			ctx := t.Context()
			switch name {
			case "no state":
				if err := os.Remove(filepath.Join(dir, ".reploy", stateFilenameV1)); err != nil {
					t.Fatal(err)
				}
			case "no owner":
				state.Staging = nil
			case "terminal":
				state.TerminalRemoval = true
			case "system", "copied installation":
				installation := installationStateV1Fixture(dir)
				installation.Scope = "system"
				if name == "copied installation" {
					installation.Scope = "user"
					installation.TargetDir = filepath.Join(t.TempDir(), "other")
				}
				state.Staging = nil
				state.Deployment = &DeploymentStateV1{Schema: DeploymentStateSchemaV1, Installation: installation}
			case "malformed state":
				if err := os.WriteFile(filepath.Join(dir, ".reploy", stateFilenameV1), []byte(`{"schema":"state-v1","unknown":true}`), 0o600); err != nil {
					t.Fatal(err)
				}
			case "released lock":
				if err := lock.Unlock(); err != nil {
					t.Fatal(err)
				}
			case "nil context":
				ctx = nil
			}
			if name == "no owner" || name == "terminal" || name == "system" || name == "copied installation" {
				content, err := EncodeStateV1(state)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, ".reploy", stateFilenameV1), content, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			calls := 0
			_, err := lock.checkIdentityMappingHostV1(ctx, nil, nil, func(context.Context, string, []identitymapping.Interval, []identitymapping.Interval) (identitymapping.HostIdentity, error) {
				calls++
				return identitymapping.HostIdentity{}, nil
			})
			if err == nil || calls != 0 {
				t.Fatalf("unproved owner reached host check: err=%v calls=%d", err, calls)
			}
		})
	}
	var lock *OperationLock
	if _, err := lock.CheckIdentityMappingHostV1(t.Context(), nil, nil); err == nil {
		t.Fatal("accepted absent lock")
	}
}

func TestMappingHostCheckPreservesFailureAndRejectsLostLock(t *testing.T) {
	for _, release := range []bool{false, true} {
		dir := t.TempDir()
		lock, _ := mappingHostState(t, dir)
		problem := errors.New("host evidence unavailable")
		_, err := lock.checkIdentityMappingHostV1(t.Context(), nil, nil, func(context.Context, string, []identitymapping.Interval, []identitymapping.Interval) (identitymapping.HostIdentity, error) {
			if release {
				_ = lock.Unlock()
				return identitymapping.HostIdentity{}, nil
			}
			return identitymapping.HostIdentity{}, problem
		})
		if err == nil || !release && !errors.Is(err, problem) {
			t.Fatalf("err=%v", err)
		}
	}
}

func TestMappingHostInspectionCannotInitializeMissingDeployment(t *testing.T) {
	dir := t.TempDir()
	if lock, err := AcquireExistingOperationLock(t.Context(), dir); err == nil || lock != nil {
		t.Fatal("accepted missing state directory")
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatal("inspection created deployment metadata")
	}
}
