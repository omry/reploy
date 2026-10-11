package dockerdeploy

import (
	"os/user"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/omry/reploy/internal/blueprint"
	"github.com/omry/reploy/internal/deploy"
	"github.com/omry/reploy/internal/providers/registry"
)

func TestSourceOperatorRuntimePreservesPublishedAccount(t *testing.T) {
	for _, test := range []struct {
		name, uid, gid string
		wantGID        uint32
		wantError      string
	}{
		{"ordinary", "1000", "1000", 1000, ""},
		{"retained primary group", "1000", "1001", 1001, ""},
		{"previous sudo corruption", "0", "0", 1000, ""},
		{"different operator", "1001", "1001", 0, "differs"},
	} {
		t.Run(test.name, func(t *testing.T) {
			operation, _, current := installedBuildPublicationSourceFixtureAtDir(t, t.TempDir())
			defer operation.Unlock()
			build := current.Lock
			build.RuntimeLayer.Account.UID, build.RuntimeLayer.Account.GID = test.uid, test.gid
			if test.uid == "0" {
				build.RuntimeLayer.Account.Name = "root"
			}
			transaction, err := deploy.ApplicationRuntimeLayerTransactionDigestV1(build.RuntimeLayer.Verifier, build.RuntimeLayer.Account, build.RuntimeLayer.Upstream, build.Platform)
			if err != nil {
				t.Fatal(err)
			}
			build.RuntimeLayer.TransactionDigest = transaction
			digest, err := operation.PublishBuildLock(build, registry.ValidateRequirementProfileV1)
			if err != nil {
				t.Fatal(err)
			}
			state := current.State
			generation := current.Generation
			generation.BuildLockDigest = digest
			state.Current = &generation
			if err := operation.CommitStateV1(&current.Generation, state); err != nil {
				t.Fatal(err)
			}
			oldGroups := installLookupUserGroupIDs
			installLookupUserGroupIDs = func(*user.User) ([]string, error) { return []string{"1000", "1002", "1002"}, nil }
			t.Cleanup(func() { installLookupUserGroupIDs = oldGroups })
			lookup := func(id string) (*user.User, error) {
				if id != "1000" {
					t.Fatalf("lookup UID %q", id)
				}
				return &user.User{Uid: "1000", Gid: "1000", Username: "operator"}, nil
			}
			got, err := sourceOperatorRuntimeV1(operation, 1000, StagedProviderBuildRuntimeV1{Host: blueprint.HostLinux}, lookup)
			if test.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantError) {
					t.Fatalf("got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			wantGroups := []uint32{1002}
			if test.wantGID == 1001 {
				wantGroups = []uint32{1000, 1002}
			}
			if got.Host != blueprint.HostLinux || got.UID != 1000 || got.GID != test.wantGID || !reflect.DeepEqual(got.SupplementaryGIDs, wantGroups) {
				t.Fatalf("runtime %#v", got)
			}
			plan, err := PlanCurrentRuntimeV1(CurrentRuntimePlanInputV1{DeploymentDir: filepath.Dir(filepath.Dir(operation.Path())), Current: CurrentBuild{State: state, Generation: generation, Lock: build}, Runtime: got})
			if err != nil {
				t.Fatal(err)
			}
			account, err := applicationLocalAccountV1(plan.Docker.Sandbox)
			if err != nil {
				t.Fatal(err)
			}
			if test.uid != "0" && account != build.RuntimeLayer.Account {
				t.Fatalf("staged account drifted: %#v vs %#v", account, build.RuntimeLayer.Account)
			}
		})
	}
}
